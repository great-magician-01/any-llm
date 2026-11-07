package logger

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func slogNewDiscard() *slog.Logger {
	return slog.New(newHandler(nil, LevelInfo, nil, "", false))
}

func TestParseLevel(t *testing.T) {
	cases := []struct {
		in   string
		want Level
	}{
		{"debug", LevelDebug},
		{"INFO", LevelInfo},
		{"Warn", LevelWarn},
		{"error", LevelError},
	}
	for _, c := range cases {
		got, err := parseLevel(c.in)
		if err != nil {
			t.Fatalf("parseLevel(%q) err: %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("parseLevel(%q)=%v want %v", c.in, got, c.want)
		}
	}
	if _, err := parseLevel("bogus"); err == nil {
		t.Fatal("expected error for bogus level")
	}
}

func TestInitWritesToConsoleAndFile(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "sub", "app.log")
	if err := Init(Options{Level: LevelInfo, FilePath: logPath}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = Close()
		SetDefault(slogNewDiscard())
	}()

	Info("hello", "k", "v")
	Warn("warn-msg")

	if err := Close(); err != nil {
		t.Fatal(err)
	}

	// Close 之后 LogFilePath 仍应返回最近一次的路径。
	actualPath := LogFilePath()
	if actualPath == "" {
		t.Fatal("LogFilePath returned empty")
	}

	expectedPath := filepath.Join(
		filepath.Dir(logPath),
		time.Now().Format("2006-01-02"),
		filepath.Base(logPath),
	)
	if actualPath != expectedPath {
		t.Fatalf("LogFilePath=%q want %q", actualPath, expectedPath)
	}

	data, err := os.ReadFile(actualPath)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "hello") {
		t.Fatalf("file log missing 'hello': %s", s)
	}
	if !strings.Contains(s, "warn-msg") {
		t.Fatalf("file log missing 'warn-msg': %s", s)
	}
	if !strings.Contains(s, "k=v") {
		t.Fatalf("file log missing key=value: %s", s)
	}
}

func TestInitRejectsBadFilePath(t *testing.T) {
	// Base(...) 解析成 ".." 的病态配置应报错而不是半初始化
	// （logwriter 拒绝 "."/".."/含分隔符的文件名前缀）。
	if err := Init(Options{Level: LevelInfo, FilePath: "logs/.."}); err == nil {
		t.Fatal("expected error for '..' file name")
	}
	// 失败的 Init 必须保留上一个（可能仍在用的）配置——这由
	// TestInitFailureKeepsPreviousLogger 在受控状态下验证。
	_ = Close()
	SetDefault(slogNewDiscard())
}

func TestInitFailureKeepsPreviousLogger(t *testing.T) {
	good := filepath.Join(t.TempDir(), "app.log")
	if err := Init(Options{Level: LevelInfo, FilePath: good}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = Close()
		SetDefault(slogNewDiscard())
	}()

	// 文件名前缀 ".." 会被 logwriter 拒绝；失败后旧实例必须原样可用
	// （Join 会清洗路径，这里必须字符串拼接）。
	if err := Init(Options{Level: LevelInfo, FilePath: good + "/.."}); err == nil {
		t.Fatal("expected error for '..' prefix")
	}
	if LogFilePath() == "" {
		t.Fatal("previous file config lost after failed Init")
	}
	Info("still-works")
	if err := Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(LogFilePath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "still-works") {
		t.Fatalf("old logger not usable after failed Init: %s", data)
	}
}

// 跨天轮转的目录布局与切换时机由 logwriter 自身的测试套件覆盖
// （logwriter 仓库 internal/writer/file_test.go）；这里只锁定本包负责的
// 路径拼接规则（见 TestInitWritesToConsoleAndFile）。

// Init 必须对坏日志路径快速失败：logwriter 懒打开文件，没有预检时不可写的
// ANY_LLM_LOG_FILE 会让服务器带病启动、文件日志静默全丢（旧实现在 Init 内
// 建目录开文件，启动即报错）。
func TestInitFastFailsOnUnwritableLogPath(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Init(Options{Level: LevelInfo, FilePath: filepath.Join(blocker, "sub", "app.log")})
	if err == nil {
		_ = Close()
		t.Fatal("Init with a log path under a regular file should fail fast")
	}
	if !strings.Contains(err.Error(), "create log dir") && !strings.Contains(err.Error(), "open log file") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Init 成功后当天日志文件已被预检创建：LogFilePath 指向的路径确实存在
// （与旧 dailyFileWriter 行为一致）。
func TestInitPreflightCreatesLogFile(t *testing.T) {
	dir := t.TempDir()
	if err := Init(Options{Level: LevelInfo, FilePath: filepath.Join(dir, "app.log")}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = Close()
		SetDefault(slogNewDiscard())
	}()
	if _, err := os.Stat(LogFilePath()); err != nil {
		t.Fatalf("log file should exist right after Init: %v", err)
	}
}
