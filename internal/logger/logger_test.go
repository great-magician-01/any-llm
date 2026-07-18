package logger

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func slogNewDiscard() *slog.Logger {
	return slog.New(newHandler(LevelInfo, []output{{w: io.Discard, color: false}}, nil, ""))
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
