// Package logger 提供 any-llm 的日志门面：对外保持 slog 语义
// （Default()/FileOnly() 返回 *slog.Logger），底层由 logwriter 承担队列化
// 写入与按天分目录的文件轮转。输出格式由 entryFormatter 复刻，与旧实现
// 逐字节一致。
package logger

import (
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/great-magician-01/logwriter"
)

type Level = slog.Level

const (
	LevelDebug = slog.LevelDebug
	LevelInfo  = slog.LevelInfo
	LevelWarn  = slog.LevelWarn
	LevelError = slog.LevelError
)

var (
	mu             sync.Mutex
	logger         *slog.Logger
	fileOnlyLogger *slog.Logger
	lw             *logwriter.Logger // nil 表示未 Init 或已 Close
	fileDir        string            // 文件输出的目录与文件名前缀；保留到下一次
	filePrefix     string            // Init（含关闭后），供 LogFilePath 使用
)

func init() {
	logger = slog.New(newHandler(nil, LevelInfo, nil, "", false))
	fileOnlyLogger = slog.New(newHandler(nil, LevelInfo, nil, "", true))
}

func Default() *slog.Logger {
	mu.Lock()
	defer mu.Unlock()
	return logger
}

func SetDefault(l *slog.Logger) {
	mu.Lock()
	defer mu.Unlock()
	logger = l
	if h, ok := l.Handler().(*handler); ok {
		fileOnlyLogger = slog.New(h.withFileOnly())
	}
}

// FileOnly returns a *slog.Logger that writes only to file outputs,
// skipping console (stdout) outputs. If no file output is configured,
// records are discarded. Useful for chatty streaming logs that would
// flood the console but are still useful in the log file.
func FileOnly() *slog.Logger {
	mu.Lock()
	defer mu.Unlock()
	return fileOnlyLogger
}

// Close 排干 logwriter 队列、落盘并关闭文件后返回，之后记录被丢弃。
func Close() error {
	mu.Lock()
	defer mu.Unlock()
	if lw != nil {
		err := lw.Close()
		lw = nil
		return err
	}
	return nil
}

func parseLevel(s string) (Level, error) {
	switch s {
	case "debug", "DEBUG", "Debug":
		return LevelDebug, nil
	case "info", "INFO", "Info":
		return LevelInfo, nil
	case "warn", "WARN", "Warn", "warning", "WARNING":
		return LevelWarn, nil
	case "error", "ERROR", "Error":
		return LevelError, nil
	default:
		return 0, fmt.Errorf("unknown log level: %q (want debug/info/warn/error)", s)
	}
}

func LevelFromString(s string) (Level, error) { return parseLevel(s) }

type Options struct {
	Level    Level
	FilePath string
}

// Init 装配 logwriter 实例并重建两个 slog 门面。FilePath 非空时启用文件
// 输出，布局与旧实现一致：<Dir(FilePath)>/<2006-01-02>/<Base(FilePath)>；
// logwriter 会给文件名恒定追加 .log，故无 .log 后缀的基础名会自动补上。
// 空串表示关闭文件输出。重复 Init 先建新实例、成功后再排干并关闭旧的，
// 失败则保留旧实例不受影响。
func Init(opts Options) error {
	mu.Lock()
	defer mu.Unlock()

	cfg := logwriter.Config{
		Level:            toLogwriterLevel(opts.Level),
		Console:          true,
		ConsoleWriter:    os.Stdout, // 调用时读取，测试可替换
		ConsoleFormatter: entryFormatter(true),
		FileFormatter:    entryFormatter(false),
		OnError:          reportBackgroundError,
		DirectoryByDay:   true,
	}
	if opts.FilePath != "" {
		// logwriter 恒定给文件名追加 .log，这里剥掉配置里已有的后缀；剥完
		// 为空/`.`/`..` 的病态基础名（".log"、"..log"）显式拒绝——logwriter
		// 会把空前缀静默替换成 "app"、把 "."/".." 直接报错，两者都会让实际
		// 写入的文件偏离配置（空前缀还会让 LogFilePath 报出幻影路径）。
		prefix := strings.TrimSuffix(filepath.Base(opts.FilePath), ".log")
		if prefix == "" || prefix == "." || prefix == ".." {
			return fmt.Errorf("invalid log file name in %q: base name after trimming \".log\" must not be empty, \".\" or \"..\"", opts.FilePath)
		}
		cfg.File = true
		cfg.Directory = filepath.Dir(opts.FilePath)
		cfg.FilePrefix = prefix
	}

	n, err := logwriter.New(cfg)
	if err != nil {
		return err
	}
	if cfg.File {
		// logwriter 懒打开文件（首次写入才建目录/开文件），坏路径会拖到启动
		// 之后才暴露成静默丢日志（唯一信号是 OnError 的 10s 限频 stderr 行）。
		// 这里恢复旧实现的快速失败：建当天目录并探测打开一次文件——顺带保证
		// 启动后 LogFilePath 指向的文件确实存在（与旧 dailyFileWriter 一致）。
		// 失败则关掉新实例、保留旧实例不受影响。
		prefix := cfg.FilePrefix
		if prefix == "" {
			prefix = "app" // 与 logwriter.New 的缺省一致
		}
		dayDir := filepath.Join(cfg.Directory, time.Now().Format("2006-01-02"))
		if err := os.MkdirAll(dayDir, 0o755); err != nil {
			_ = n.Close()
			return fmt.Errorf("create log dir: %w", err)
		}
		probe, err := os.OpenFile(filepath.Join(dayDir, prefix+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			_ = n.Close()
			return fmt.Errorf("open log file: %w", err)
		}
		_ = probe.Close()
	}
	if lw != nil {
		_ = lw.Close()
	}
	lw = n
	fileDir = cfg.Directory
	filePrefix = cfg.FilePrefix

	logger = slog.New(newHandler(n, opts.Level, nil, "", false))
	fileOnlyLogger = slog.New(newHandler(n, opts.Level, nil, "", true))
	slog.SetDefault(logger)
	log.SetFlags(0)
	log.SetOutput(io.Discard)
	return nil
}

// LogFilePath 返回当前日志文件路径（Close 之后仍返回最近一次的路径）；
// 未配置文件输出时为空串。日期取当前时刻的预测值：logwriter 按条目时间
// 落盘并轮转，午夜前后或轮转失败期间报告的路径可能与实际文件偏差一天；
// Init 的预检保证启动时刻该文件存在。
func LogFilePath() string {
	mu.Lock()
	defer mu.Unlock()
	if fileDir == "" {
		return ""
	}
	return filepath.Join(fileDir, time.Now().Format("2006-01-02"), filePrefix+".log")
}

// reportBackgroundError 把 logwriter 的后台错误（写文件失败、轮转失败、
// Formatter panic 等）限频转发到 stderr。必须用独立的锁：Close 持有包级
// mu 等待队列排干，期间消费者可能调到这里，拿 mu 会死锁。
var errReport struct {
	sync.Mutex
	last time.Time
}

func reportBackgroundError(err error) {
	errReport.Lock()
	defer errReport.Unlock()
	if time.Since(errReport.last) < 10*time.Second {
		return
	}
	errReport.last = time.Now()
	fmt.Fprintf(os.Stderr, "logger: %v\n", err)
}

func Debug(msg string, args ...any) { Default().Debug(msg, args...) }
func Info(msg string, args ...any)  { Default().Info(msg, args...) }
func Warn(msg string, args ...any)  { Default().Warn(msg, args...) }
func Error(msg string, args ...any) { Default().Error(msg, args...) }

func Infof(format string, args ...any)  { Default().Info(fmt.Sprintf(format, args...)) }
func Warnf(format string, args ...any)  { Default().Warn(fmt.Sprintf(format, args...)) }
func Errorf(format string, args ...any) { Default().Error(fmt.Sprintf(format, args...)) }
func Debugf(format string, args ...any) { Default().Debug(fmt.Sprintf(format, args...)) }
