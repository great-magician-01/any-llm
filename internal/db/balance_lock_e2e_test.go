package db

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

type balanceLockAttempt struct {
	unlock func() error
	err    error
}

func TestPG_E2E_BalanceLockBlocksAndReleases(t *testing.T) {
	testBalanceLockBlocksAndReleases(t, pgTestDB(t))
}

func TestMySQL_E2E_BalanceLockBlocksAndReleases(t *testing.T) {
	testBalanceLockBlocksAndReleases(t, mysqlTestDB(t))
}

func testBalanceLockBlocksAndReleases(t *testing.T, d *sql.DB) {
	t.Helper()
	upstreamID := time.Now().UnixNano()
	first, err := AcquireBalanceLock(context.Background(), d, upstreamID)
	if err != nil {
		t.Fatalf("acquire first lock: %v", err)
	}
	defer first() // idempotent; also protects failure paths below

	started := make(chan struct{})
	secondDone := make(chan balanceLockAttempt, 1)
	go func() {
		close(started)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		unlock, err := AcquireBalanceLock(ctx, d, upstreamID)
		secondDone <- balanceLockAttempt{unlock: unlock, err: err}
	}()
	<-started

	select {
	case attempt := <-secondDone:
		if attempt.unlock != nil {
			attempt.unlock()
		}
		t.Fatalf("second lock returned before the first was released (err=%v)", attempt.err)
	case <-time.After(150 * time.Millisecond):
		// Expected: the second dedicated session is still waiting.
	}

	if err := first(); err != nil {
		t.Fatalf("release first lock: %v", err)
	}
	var second balanceLockAttempt
	select {
	case second = <-secondDone:
	case <-time.After(3 * time.Second):
		t.Fatal("second lock did not acquire after the first was released")
	}
	if second.err != nil {
		t.Fatalf("acquire second lock after release: %v", second.err)
	}
	if err := second.unlock(); err != nil {
		t.Fatalf("release second lock: %v", err)
	}
	if err := second.unlock(); err != nil {
		t.Fatalf("idempotent second release: %v", err)
	}

	// Reacquire once more to prove the second release returned a clean session
	// to the pool rather than leaving a server-side lock behind.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	third, err := AcquireBalanceLock(ctx, d, upstreamID)
	if err != nil {
		t.Fatalf("reacquire after second release: %v", err)
	}
	if err := third(); err != nil {
		t.Fatalf("release third lock: %v", err)
	}
}

func TestPG_E2E_BalanceLockCanceledWaiterDoesNotPoisonLock(t *testing.T) {
	testBalanceLockCanceledWaiter(t, pgTestDB(t))
}

func TestMySQL_E2E_BalanceLockCanceledWaiterDoesNotPoisonLock(t *testing.T) {
	testBalanceLockCanceledWaiter(t, mysqlTestDB(t))
}

func testBalanceLockCanceledWaiter(t *testing.T, d *sql.DB) {
	t.Helper()
	upstreamID := time.Now().UnixNano()
	first, err := AcquireBalanceLock(context.Background(), d, upstreamID)
	if err != nil {
		t.Fatalf("acquire first lock: %v", err)
	}
	defer first()

	waitCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := AcquireBalanceLock(waitCtx, d, upstreamID); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("canceled waiter err=%v, want context deadline exceeded", err)
	}
	if err := first(); err != nil {
		t.Fatalf("release first lock: %v", err)
	}

	ctx, cancelAfter := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelAfter()
	after, err := AcquireBalanceLock(ctx, d, upstreamID)
	if err != nil {
		t.Fatalf("acquire after canceled waiter and release: %v", err)
	}
	if err := after(); err != nil {
		t.Fatalf("release final lock: %v", err)
	}
}
