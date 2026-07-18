package db

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestWriter(t *testing.T, bufSize int) *Writer {
	t.Helper()
	d, err := OpenSQLite(t.TempDir() + "\\test.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	w := NewWriter(d, bufSize)
	w.Start()
	return w
}

func TestWriterDoSyncBasic(t *testing.T) {
	w := newTestWriter(t, 16)
	defer w.Stop()

	var ran atomic.Bool
	err := w.DoSync(func(d *sql.DB) error {
		ran.Store(true)
		return nil
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ran.Load() {
		t.Fatal("fn not executed")
	}
}

func TestWriterDoSyncError(t *testing.T) {
	w := newTestWriter(t, 16)
	defer w.Stop()

	want := fmt.Errorf("boom")
	err := w.DoSync(func(d *sql.DB) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("err=%v want %v", err, want)
	}
}

func TestWriterStopDuringInflightSync(t *testing.T) {
	// Verify that Stop does not deadlock when DoSync calls are in flight,
	// and that every in-flight call returns either a result or
	// ErrWriterStopped (never blocks forever).
	w := newTestWriter(t, 8)

	// holdOp blocks the worker on a gate so we can stage the race.
	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	w.DoAsync(func(d *sql.DB) error {
		started <- struct{}{}
		<-gate
		return nil
	})
	<-started

	const n = 32
	var wg sync.WaitGroup
	var ok, stopped atomic.Int64
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			err := w.DoSync(func(d *sql.DB) error { return nil })
			if err == nil {
				ok.Add(1)
			} else if errors.Is(err, ErrWriterStopped) {
				stopped.Add(1)
			} else {
				t.Errorf("unexpected err: %v", err)
			}
		}()
	}

	// Let some DoSync queue up, then release the gate and stop concurrently.
	time.Sleep(20 * time.Millisecond)
	close(gate)
	w.Stop()
	wg.Wait()

	if ok.Load()+stopped.Load() != n {
		t.Fatalf("ok=%d stopped=%d want total %d", ok.Load(), stopped.Load(), n)
	}
}

func TestWriterDoSyncAfterStop(t *testing.T) {
	w := newTestWriter(t, 4)
	w.Stop()

	err := w.DoSync(func(d *sql.DB) error { return nil })
	if !errors.Is(err, ErrWriterStopped) {
		t.Fatalf("err=%v want ErrWriterStopped", err)
	}
}

func TestWriterDoAsyncAfterStop(t *testing.T) {
	w := newTestWriter(t, 4)
	w.Stop()

	var ran atomic.Bool
	w.DoAsync(func(d *sql.DB) error { ran.Store(true); return nil })
	if ran.Load() {
		t.Fatal("DoAsync ran after Stop")
	}
}

func TestWriterStopIdempotent(t *testing.T) {
	w := newTestWriter(t, 4)
	w.Stop()
	w.Stop()
	w.Stop()
}

func TestRebindPostgresSkipsLiteralsAndComments(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"SELECT ?", "SELECT $1"},
		{"SELECT ?, ?, ?", "SELECT $1, $2, $3"},
		{"WHERE name='?x?' AND id=?", "WHERE name='?x?' AND id=$1"},
		{"-- comment with ?\nSELECT ?", "-- comment with ?\nSELECT $1"},
		{"/* a ? b */ SELECT ?", "/* a ? b */ SELECT $1"},
		{"SELECT '?' -- trailing ?\nFROM t WHERE id=?", "SELECT '?' -- trailing ?\nFROM t WHERE id=$1"},
		{"SELECT 'it''s ?' || ?", "SELECT 'it''s ?' || $1"},
		{"SELECT ?", "SELECT $1"},
	}
	for i, c := range cases {
		got := rebindPostgres(c.in)
		if got != c.want {
			t.Errorf("case %d:\n  in=%q\n  got=%q\n  want=%q", i, c.in, got, c.want)
		}
	}
}

func TestRebindSQLiteUnchanged(t *testing.T) {
	d, err := OpenSQLite(t.TempDir() + "\\s.db")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	q := "SELECT ?, ?, '?'"
	if got := Rebind(d, q); got != q {
		t.Fatalf("sqlite query changed: got=%q want=%q", got, q)
	}
}
