package logger

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureStdout 在 fn 执行期间把 os.Stdout 换成管道，返回 fn 期间写到 stdout 的
// 内容。Init 在调用时读取 os.Stdout，所以必须把 Init 放进 fn 里。
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	// 无论 fn 如何退出（含 t.Fatal）都要恢复 stdout 并收走读端。
	defer func() {
		os.Stdout = orig
		_ = r.Close()
	}()
	fn()
	_ = w.Close()
	os.Stdout = orig
	out := <-done
	return out
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// TestInit_LevelFiltering 断言日志级别真的在过滤：Level=warn 时 debug/info
// 记录不得落盘。之前没有任何用例发出过 debug 级记录，Enabled() 实际上是
// 零覆盖——级别配置写错了也不会有人发现。
func TestInit_LevelFiltering(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	if err := Init(Options{Level: LevelWarn, FilePath: path}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = Close()
		SetDefault(slogNewDiscard())
	}()

	Debug("debug-msg")
	Info("info-msg")
	Warn("warn-msg")
	Error("error-msg")

	if err := Close(); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, LogFilePath())
	for _, hidden := range []string{"debug-msg", "info-msg"} {
		if strings.Contains(got, hidden) {
			t.Errorf("%q must be filtered out at level=warn:\n%s", hidden, got)
		}
	}
	for _, shown := range []string{"warn-msg", "error-msg"} {
		if !strings.Contains(got, shown) {
			t.Errorf("%q must be present at level=warn:\n%s", shown, got)
		}
	}
}

// TestInit_DualWriteAndNoANSIInFile 断言同一记录同时进 stdout 与文件，且
// **文件里不能有 ANSI 颜色码**（生产日志要被 grep/采集，颜色码是污染），
// 控制台则应带颜色。
func TestInit_DualWriteAndNoANSIInFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	stdout := captureStdout(t, func() {
		if err := Init(Options{Level: LevelInfo, FilePath: path}); err != nil {
			t.Fatalf("init: %v", err)
		}
		Info("both-sinks", "k", "v")
	})
	if err := Close(); err != nil {
		t.Fatal(err)
	}
	defer SetDefault(slogNewDiscard())

	file := readFile(t, LogFilePath())
	if !strings.Contains(stdout, "both-sinks") {
		t.Errorf("console output missing the record:\n%s", stdout)
	}
	if !strings.Contains(file, "both-sinks") || !strings.Contains(file, "k=v") {
		t.Errorf("file output missing the record:\n%s", file)
	}
	if strings.Contains(file, "\033[") {
		t.Errorf("log file must not contain ANSI color codes:\n%q", file)
	}
	if !strings.Contains(stdout, "\033[") {
		t.Errorf("console output should be colored:\n%q", stdout)
	}
}

// TestFileOnly_SkipsConsole 断言 FileOnly 只写文件：流式原始 SSE 这类噪声
// 日志不应该淹没控制台，但必须留在文件里可查。
func TestFileOnly_SkipsConsole(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	stdout := captureStdout(t, func() {
		if err := Init(Options{Level: LevelInfo, FilePath: path}); err != nil {
			t.Fatalf("init: %v", err)
		}
		FileOnly().Info("file-only-msg")
		Info("console-msg")
	})
	if err := Close(); err != nil {
		t.Fatal(err)
	}
	defer SetDefault(slogNewDiscard())

	if strings.Contains(stdout, "file-only-msg") {
		t.Errorf("FileOnly record leaked to console:\n%s", stdout)
	}
	if !strings.Contains(stdout, "console-msg") {
		t.Errorf("normal record missing from console:\n%s", stdout)
	}
	file := readFile(t, LogFilePath())
	if !strings.Contains(file, "file-only-msg") {
		t.Errorf("FileOnly record missing from file:\n%s", file)
	}
	if !strings.Contains(file, "console-msg") {
		t.Errorf("normal record missing from file:\n%s", file)
	}
}

// TestFileOnly_NoFileConfigured 断言未配置文件输出时 FileOnly 不会 panic、
// 也不会写到控制台（记录被丢弃），并且 LogFilePath 为空串。
func TestFileOnly_NoFileConfigured(t *testing.T) {
	stdout := captureStdout(t, func() {
		if err := Init(Options{Level: LevelInfo}); err != nil {
			t.Fatalf("init: %v", err)
		}
		FileOnly().Info("dropped-msg")
	})
	defer SetDefault(slogNewDiscard())

	if LogFilePath() != "" {
		t.Errorf("LogFilePath=%q want empty when no file output is configured", LogFilePath())
	}
	if strings.Contains(stdout, "dropped-msg") {
		t.Errorf("FileOnly record leaked to console when no file is configured:\n%s", stdout)
	}
}

// TestWithAttrsAndWithGroup 断言 slog 的分组/预置属性约定被遵守：
// With("k","v") 的记录带 k=v；WithGroup("g") 之后的记录属性必须写成 g.k=v
// （原实现只给 WithAttrs 加前缀，记录属性漏了前缀，字段名自相矛盾）。
func TestWithAttrsAndWithGroup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	if err := Init(Options{Level: LevelInfo, FilePath: path}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = Close()
		SetDefault(slogNewDiscard())
	}()

	Default().With("service", "gateway").Info("with-attrs")
	Default().WithGroup("req").Info("with-group", "key", "val")

	if err := Close(); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, LogFilePath())
	if !strings.Contains(got, "service=gateway") {
		t.Errorf("With(...) attribute missing:\n%s", got)
	}
	if !strings.Contains(got, "req.key=val") {
		t.Errorf("WithGroup must prefix record attributes with req.:\n%s", got)
	}
}
