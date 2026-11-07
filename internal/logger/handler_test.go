package logger

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/great-magician-01/logwriter"
)

// dayDirLogFiles 读取 dir 下按天分目录布局（<dir>/<日期>/<文件>.log）里的
// 全部日志文件内容。
func dayDirLogFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var contents []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			data, err := os.ReadFile(filepath.Join(dir, e.Name(), f.Name()))
			if err != nil {
				return nil, err
			}
			contents = append(contents, string(data))
		}
	}
	return contents, nil
}

// newBufferLogger 建一条 slog→adapter→logwriter 的完整链路，控制台写入 buf、
// 格式用无色版（与文件输出一致），方便断言。
func newBufferLogger(t *testing.T, level slog.Level) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	l, err := logwriter.New(logwriter.Config{
		Level:            toLogwriterLevel(level),
		Console:          true,
		ConsoleWriter:    &buf,
		ConsoleFormatter: entryFormatter(false),
	})
	if err != nil {
		t.Fatalf("logwriter.New: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return slog.New(newHandler(l, level, nil, "", false)), &buf
}

// TestHandlerEndToEnd 覆盖 slog 属性 → logwriter 字段的类型转换：slog.Int
// 进来是 int，必须渲染成十进制而不是 fmt 默认结构；低于级别的记录必须被滤掉。
func TestHandlerEndToEnd(t *testing.T) {
	sl, buf := newBufferLogger(t, LevelInfo)
	sl.Info("req", "method", "POST", "status", 201, "size", int64(42), "duration_ms", 1500)
	sl.Debug("below-level")
	if err := sl.Handler().(*handler).lw.Close(); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	for _, want := range []string{"INF req", "method=POST", "status=201", "size=42", "dur=1.500s"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "below-level") {
		t.Errorf("below-level record leaked:\n%s", got)
	}
}

// TestHandlerGroupAndPreset 锁定 slog 约定：With 的预置属性、WithGroup 的
// 前缀、以及 slog.Group 值的递归拍平。
func TestHandlerGroupAndPreset(t *testing.T) {
	sl, buf := newBufferLogger(t, LevelInfo)
	sl.With("service", "gateway").Info("preset")
	sl.WithGroup("req").Info("grouped", "key", "val")
	sl.Info("nested", slog.Group("g", slog.Int("a", 1), slog.Group("h", slog.Int("b", 2))))
	if err := sl.Handler().(*handler).lw.Close(); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	for _, want := range []string{
		"preset service=gateway",
		"grouped req.key=val",
		"nested g.a=1 g.h.b=2",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

// TestHandlerFileOnlyMark 断言 fileOnly handler 的记录带上 logwriter 的
// FileOnly 标记：进得了文件、进不了控制台。
func TestHandlerFileOnlyMark(t *testing.T) {
	dir := t.TempDir()
	l, err := logwriter.New(logwriter.Config{
		Level:            logwriter.InfoLevel,
		Console:          true,
		ConsoleWriter:    &bytes.Buffer{}, // 消费端要非 nil，输出丢这里
		ConsoleFormatter: entryFormatter(false),
		File:             true,
		Directory:        dir,
		FilePrefix:       "app",
		DirectoryByDay:   true,
		FileFormatter:    entryFormatter(false),
	})
	if err != nil {
		t.Fatalf("logwriter.New: %v", err)
	}

	sl := slog.New(newHandler(l, LevelInfo, nil, "", true).withFileOnly())
	sl.Info("only-in-file")
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	files, err := dayDirLogFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || !strings.Contains(files[0], "only-in-file") {
		t.Fatalf("file output = %v, want the record", files)
	}
}

func TestToLogwriterLevel(t *testing.T) {
	cases := map[slog.Level]logwriter.Level{
		slog.LevelDebug:      logwriter.DebugLevel,
		slog.LevelDebug - 1:  logwriter.DebugLevel,
		slog.LevelInfo:       logwriter.InfoLevel,
		slog.LevelWarn + 1:   logwriter.WarnLevel,
		slog.LevelError:      logwriter.ErrorLevel,
		slog.LevelError + 10: logwriter.ErrorLevel,
	}
	for in, want := range cases {
		if got := toLogwriterLevel(in); got != want {
			t.Errorf("toLogwriterLevel(%v)=%v want %v", in, got, want)
		}
	}
}
