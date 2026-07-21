package logger

import (
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
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
	currentHandler *handler
	fileOnlyLogger *slog.Logger
	closer         io.Closer
	logFilePath    string
)

func init() {
	currentHandler = newHandler(LevelInfo, []output{{w: io.Discard, color: false}}, nil, "")
	logger = slog.New(currentHandler)
	fileOnlyLogger = slog.New(newFileOnlyHandler(currentHandler.level, currentHandler.outputs, nil, ""))
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
		currentHandler = h
		fileOnlyLogger = slog.New(newFileOnlyHandler(h.level, h.outputs, nil, ""))
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

func Close() error {
	mu.Lock()
	defer mu.Unlock()
	if closer != nil {
		err := closer.Close()
		closer = nil
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

func Init(opts Options) error {
	mu.Lock()
	defer mu.Unlock()

	level := opts.Level
	outputs := []output{{w: os.Stdout, color: true, console: true}}

	if opts.FilePath != "" {
		dir := filepath.Dir(opts.FilePath)
		filename := filepath.Base(opts.FilePath)
		dateDir := filepath.Join(dir, time.Now().Format("2006-01-02"))
		actualPath := filepath.Join(dateDir, filename)

		if err := os.MkdirAll(dateDir, 0o755); err != nil {
			return fmt.Errorf("create log dir: %w", err)
		}
		f, err := os.OpenFile(actualPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return fmt.Errorf("open log file: %w", err)
		}
		outputs = append(outputs, output{w: f, color: false, console: false})
		closer = f
		logFilePath = actualPath
	}

	h := newHandler(level, outputs, nil, "")
	currentHandler = h
	logger = slog.New(h)
	fileOnlyLogger = slog.New(newFileOnlyHandler(h.level, h.outputs, nil, ""))
	slog.SetDefault(logger)
	log.SetFlags(0)
	log.SetOutput(io.Discard)
	return nil
}

func LogFilePath() string {
	mu.Lock()
	defer mu.Unlock()
	return logFilePath
}

func Debug(msg string, args ...any) { Default().Debug(msg, args...) }
func Info(msg string, args ...any)  { Default().Info(msg, args...) }
func Warn(msg string, args ...any)  { Default().Warn(msg, args...) }
func Error(msg string, args ...any) { Default().Error(msg, args...) }

func Infof(format string, args ...any)  { Default().Info(fmt.Sprintf(format, args...)) }
func Warnf(format string, args ...any)  { Default().Warn(fmt.Sprintf(format, args...)) }
func Errorf(format string, args ...any) { Default().Error(fmt.Sprintf(format, args...)) }
func Debugf(format string, args ...any) { Default().Debug(fmt.Sprintf(format, args...)) }
