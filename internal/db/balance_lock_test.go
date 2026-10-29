package db

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

func TestCheckMySQLBalanceLockResult(t *testing.T) {
	tests := []struct {
		name    string
		result  sql.NullInt64
		wantErr string
	}{
		{name: "success", result: sql.NullInt64{Int64: 1, Valid: true}},
		{name: "timeout or not owner", result: sql.NullInt64{Int64: 0, Valid: true}, wantErr: "returned 0"},
		{name: "server error", result: sql.NullInt64{}, wantErr: "returned NULL"},
		{name: "unexpected value", result: sql.NullInt64{Int64: 2, Valid: true}, wantErr: "returned 2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkMySQLBalanceLockResult("GET_LOCK", tt.result)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("result %+v: unexpected error %v", tt.result, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("result %+v: err=%v, want containing %q", tt.result, err, tt.wantErr)
			}
		})
	}
}

func TestAcquireBalanceLockSQLiteIsLocalOnly(t *testing.T) {
	// A closed pool proves the SQLite path issues no SQL and needs no dedicated
	// connection: process-local serialization remains LockBalanceUpstream's job.
	d, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	unlock, err := AcquireBalanceLock(context.Background(), d, 7)
	if err != nil {
		t.Fatalf("SQLite balance lock must be a no-op: %v", err)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	if err := unlock(); err != nil { // release is deliberately idempotent
		t.Fatal(err)
	}
}

func TestMySQLBalanceLockNameFitsServerLimit(t *testing.T) {
	for _, id := range []int64{-1 << 63, 0, 1, 1<<63 - 1} {
		name := mysqlBalanceLockName(id)
		if len(name) > 64 {
			t.Errorf("lock name for %d is %d bytes, MySQL caps names at 64", id, len(name))
		}
	}
	if mysqlBalanceLockName(1) == mysqlBalanceLockName(2) {
		t.Fatal("different upstream ids must use different MySQL lock names")
	}
}
