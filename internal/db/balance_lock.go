package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"
)

const (
	balanceLockAcquireTimeout = 15 * time.Second
	balanceLockReleaseTimeout = 5 * time.Second
)

// AcquireBalanceLock acquires the cross-process lock for one upstream's
// balance-refresh sequence. PG uses a session advisory lock and MySQL uses
// GET_LOCK; both are pinned to a dedicated *sql.Conn so acquisition and release
// cannot hop between pooled sessions. SQLite has no cross-process primitive
// here and deliberately remains a no-op (the caller keeps its local mutex).
//
// The lock does not touch the upstreams table: a row lock would require a
// transaction held across the vendor fetch and would deadlock with the single
// writer goroutine that later records the snapshot.
//
// The returned release function is idempotent. It explicitly unlocks and then
// closes the dedicated Conn. If either acquisition or explicit release fails,
// the physical connection is discarded so a possibly-held session lock cannot
// leak back into the pool.
func AcquireBalanceLock(ctx context.Context, d *sql.DB, upstreamID int64) (unlock func() error, err error) {
	if DialectOf(d) == DialectSQLite {
		return func() error { return nil }, nil
	}

	// Bound both pool-connection acquisition and the server-side wait. The
	// caller's earlier deadline still wins through context propagation.
	acquireCtx, cancel := context.WithTimeout(ctx, balanceLockAcquireTimeout)
	defer cancel()
	conn, err := d.Conn(acquireCtx)
	if err != nil {
		return nil, fmt.Errorf("acquire dedicated balance-lock connection: %w", err)
	}
	acquired := false
	defer func() {
		if !acquired {
			err = errors.Join(err, closeBalanceLockConn(conn, true))
		}
	}()

	dialect := DialectOf(d)
	switch dialect {
	case DialectPostgres:
		// Advisory locks are scoped to the database; the upstream id is the
		// bigint key. No other subsystem in this process uses advisory locks.
		if _, err := conn.ExecContext(acquireCtx, `SELECT pg_advisory_lock($1)`, upstreamID); err != nil {
			return nil, fmt.Errorf("pg_advisory_lock for upstream %d: %w", upstreamID, err)
		}
	case DialectMySQL:
		name := mysqlBalanceLockName(upstreamID)
		var result sql.NullInt64
		// Keep a positive server-side timeout as a second bound in addition to
		// acquireCtx; unlike GET_LOCK(..., -1), this cannot wait forever.
		if err := conn.QueryRowContext(acquireCtx, `SELECT GET_LOCK(?, ?)`, name, int(balanceLockAcquireTimeout/time.Second)).Scan(&result); err != nil {
			return nil, fmt.Errorf("GET_LOCK for upstream %d: %w", upstreamID, err)
		}
		if err := checkMySQLBalanceLockResult("GET_LOCK", result); err != nil {
			return nil, fmt.Errorf("GET_LOCK for upstream %d: %w", upstreamID, err)
		}
	default:
		return nil, fmt.Errorf("unsupported balance-lock dialect %q", dialect)
	}

	acquired = true
	return newBalanceLockUnlock(conn, dialect, upstreamID), nil
}

func mysqlBalanceLockName(upstreamID int64) string {
	// MySQL lock names are server-global and capped at 64 characters. This
	// fixed application prefix plus an int64 stays below that cap. Serializing
	// same-id upstreams in different databases is harmless false sharing.
	return "any-llm:balance-refresh:" + strconv.FormatInt(upstreamID, 10)
}

// checkMySQLBalanceLockResult handles all three documented GET_LOCK /
// RELEASE_LOCK results: 1 means success, 0 means timeout/not-owner, and NULL
// means an error (or a nonexistent lock for RELEASE_LOCK).
func checkMySQLBalanceLockResult(operation string, result sql.NullInt64) error {
	if !result.Valid {
		return fmt.Errorf("%s returned NULL", operation)
	}
	if result.Int64 != 1 {
		return fmt.Errorf("%s returned %d", operation, result.Int64)
	}
	return nil
}

func newBalanceLockUnlock(conn *sql.Conn, dialect Dialect, upstreamID int64) func() error {
	var once sync.Once
	var unlockErr error
	return func() error {
		once.Do(func() {
			// Release must still run when the refresh request was canceled. A
			// fresh bounded context avoids both an immediately-canceled unlock
			// and an unbounded hang on a broken server connection.
			ctx, cancel := context.WithTimeout(context.Background(), balanceLockReleaseTimeout)
			defer cancel()

			switch dialect {
			case DialectPostgres:
				var released bool
				if err := conn.QueryRowContext(ctx, `SELECT pg_advisory_unlock($1)`, upstreamID).Scan(&released); err != nil {
					unlockErr = fmt.Errorf("pg_advisory_unlock for upstream %d: %w", upstreamID, err)
				} else if !released {
					unlockErr = fmt.Errorf("pg_advisory_unlock for upstream %d returned false", upstreamID)
				}
			case DialectMySQL:
				var result sql.NullInt64
				name := mysqlBalanceLockName(upstreamID)
				if err := conn.QueryRowContext(ctx, `SELECT RELEASE_LOCK(?)`, name).Scan(&result); err != nil {
					unlockErr = fmt.Errorf("RELEASE_LOCK for upstream %d: %w", upstreamID, err)
				} else if err := checkMySQLBalanceLockResult("RELEASE_LOCK", result); err != nil {
					unlockErr = fmt.Errorf("RELEASE_LOCK for upstream %d: %w", upstreamID, err)
				}
			default:
				unlockErr = fmt.Errorf("unsupported balance-lock dialect %q", dialect)
			}
			unlockErr = errors.Join(unlockErr, closeBalanceLockConn(conn, unlockErr != nil))
		})
		return unlockErr
	}
}

// closeBalanceLockConn returns a healthy, explicitly-unlocked connection to
// the pool. On an ambiguous acquire/release outcome discard=true marks the
// physical connection bad first; Conn.Close alone would only return the
// session to the pool and could leave its server-side lock held.
func closeBalanceLockConn(conn *sql.Conn, discard bool) error {
	var discardErr error
	if discard {
		if err := conn.Raw(func(any) error { return driver.ErrBadConn }); err != nil && !errors.Is(err, driver.ErrBadConn) {
			discardErr = fmt.Errorf("discard balance-lock connection: %w", err)
		}
	}
	if err := conn.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
		return errors.Join(discardErr, fmt.Errorf("close balance-lock connection: %w", err))
	}
	return discardErr
}
