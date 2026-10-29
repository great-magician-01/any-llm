package upstream

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

func TestLockBalanceUpstreamAcquireFailureReleasesLocalMutex(t *testing.T) {
	const upstreamID int64 = 9173
	// DialectOf still identifies a closed pool as MySQL, while d.Conn reports
	// "sql: database is closed" before any network access. This exercises the
	// composite lock's database-acquisition failure path without a server.
	broken, err := sql.Open("mysql", "unused:unused@tcp(127.0.0.1:1)/unused")
	if err != nil {
		t.Fatal(err)
	}
	if err := broken.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := LockBalanceUpstream(context.Background(), broken, upstreamID); err == nil || !strings.Contains(err.Error(), "sql: database is closed") {
		t.Fatalf("acquire on closed MySQL pool: err=%v, want database-closed error", err)
	}

	local, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { local.Close() })
	acquired := make(chan error, 1)
	go func() {
		unlock, err := LockBalanceUpstream(context.Background(), local, upstreamID)
		if err == nil {
			err = unlock()
		}
		acquired <- err
	}()
	select {
	case err := <-acquired:
		if err != nil {
			t.Fatalf("local lock after database acquisition failure: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("database acquisition failure left the process-local mutex locked")
	}
}
