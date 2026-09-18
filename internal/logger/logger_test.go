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

func TestDailyFileWriterRotatesAcrossDays(t *testing.T) {
	dir := t.TempDir()
	w, err := newDailyFileWriter(dir, "app.log")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	// Pin the clock so the test is independent of when it runs.
	day1 := time.Now()
	w.mu.Lock()
	w.now = func() time.Time { return day1 }
	w.mu.Unlock()

	if _, err := w.Write([]byte("day1\n")); err != nil {
		t.Fatal(err)
	}
	firstPath := w.Path()
	wantFirst := filepath.Join(dir, day1.Format(dateDirFormat), "app.log")
	if firstPath != wantFirst {
		t.Fatalf("Path=%q want %q", firstPath, wantFirst)
	}

	// Cross into the next local day: the next Write must rotate.
	day2 := day1.Add(24 * time.Hour)
	w.mu.Lock()
	w.now = func() time.Time { return day2 }
	w.mu.Unlock()

	if _, err := w.Write([]byte("day2\n")); err != nil {
		t.Fatal(err)
	}
	secondPath := w.Path()
	wantSecond := filepath.Join(dir, day2.Format(dateDirFormat), "app.log")
	if secondPath != wantSecond {
		t.Fatalf("Path=%q want %q", secondPath, wantSecond)
	}
	if secondPath == firstPath {
		t.Fatal("no rotation across days")
	}

	data1, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data1) != "day1\n" {
		t.Fatalf("old file content=%q", data1)
	}
	data2, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data2) != "day2\n" {
		t.Fatalf("new file content=%q", data2)
	}

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("x")); err != os.ErrClosed {
		t.Fatalf("write after close err=%v", err)
	}
	// Path still reports the last file after Close.
	if p := w.Path(); p != secondPath {
		t.Fatalf("Path after close=%q want %q", p, secondPath)
	}
}
