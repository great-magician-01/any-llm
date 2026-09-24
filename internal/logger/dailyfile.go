package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// dateDirFormat names the per-day subdirectory log files rotate into.
const dateDirFormat = "2006-01-02"

// dailyFileWriter writes log lines to <dir>/<YYYY-MM-DD>/<filename>, rotating
// to the new day's directory on the first Write after local midnight. It is
// safe for concurrent use (the console and file-only handlers share it).
type dailyFileWriter struct {
	mu       sync.Mutex
	dir      string
	filename string
	now      func() time.Time // injectable clock, for tests

	day  string   // date the open file belongs to
	f    *os.File // nil after Close
	path string   // current file path; kept after Close so LogFilePath still works

	warnedRotate bool // rate-limit rotation-failure reports to stderr
}

func newDailyFileWriter(dir, filename string) (*dailyFileWriter, error) {
	w := &dailyFileWriter{dir: dir, filename: filename, now: time.Now}
	if err := w.rotateLocked(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *dailyFileWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return 0, os.ErrClosed
	}
	if day := w.now().Format(dateDirFormat); day != w.day {
		if err := w.rotateLocked(); err != nil {
			// Rotation failed: keep writing to the old file rather than
			// dropping logs. Our caller discards write errors, so report
			// once to stderr.
			if !w.warnedRotate {
				w.warnedRotate = true
				fmt.Fprintf(os.Stderr, "logger: rotate log file failed, continuing with %s: %v\n", w.path, err)
			}
		} else {
			w.warnedRotate = false
		}
	}
	return w.f.Write(p)
}

// rotateLocked opens today's file, then closes the previous one. Callers hold
// w.mu (the constructor is exempt: the writer isn't shared yet). If opening
// the new file fails, the old file stays open.
func (w *dailyFileWriter) rotateLocked() error {
	day := w.now().Format(dateDirFormat)
	dateDir := filepath.Join(w.dir, day)
	if err := os.MkdirAll(dateDir, 0o755); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}
	nf, err := os.OpenFile(filepath.Join(dateDir, w.filename), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	if w.f != nil {
		_ = w.f.Close()
	}
	w.f = nf
	w.day = day
	w.path = nf.Name()
	return nil
}

func (w *dailyFileWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}

// Path returns the current log file path (the last one after Close).
func (w *dailyFileWriter) Path() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.path
}
