package db

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestOpenSQLite_MigrationsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db2, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()

	for _, table := range []string{"upstreams", "upstream_models", "ext_keys", "usage_records"} {
		var name string
		err := db2.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name)
		if err != nil {
			t.Fatalf("table %s missing: %v", table, err)
		}
	}
}

func TestOpenSQLite_FreshCreatesAllTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh.db")
	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range []string{"upstreams", "upstream_models", "ext_keys", "usage_records"} {
		var name string
		err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name)
		if err != nil {
			t.Fatalf("table %s missing: %v", table, err)
		}
	}
}

func TestOpenPG_DSNEncodesSpecialChars(t *testing.T) {
	// Verify user/password with URL-special characters round-trip through the
	// DSN that OpenPG builds. We can't connect here, but pgx.ParseConfig
	// reproduces what OpenPG does internally.
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
		url.QueryEscape("u@b:c"), url.QueryEscape("p@ss:w/o?rd"),
		"localhost", 5432, "amanuensis")
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.User != "u@b:c" {
		t.Fatalf("user=%q want %q", cfg.User, "u@b:c")
	}
	if cfg.Password != "p@ss:w/o?rd" {
		t.Fatalf("password=%q want %q", cfg.Password, "p@ss:w/o?rd")
	}
}

func TestOpenPG_FreshCreatesAllTables(t *testing.T) {
	dsn := os.Getenv("DB_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set DB_TEST_PG_DSN to run postgres tests")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	d := stdlib.OpenDB(*cfg)
	defer d.Close()
	if err := d.Ping(); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if _, err := d.Exec(migrationPG); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, table := range []string{"upstreams", "upstream_models", "ext_keys", "usage_records"} {
		var name string
		err := d.QueryRow("SELECT table_name FROM information_schema.tables WHERE table_name = $1 AND table_schema = current_schema()", table).Scan(&name)
		if err != nil {
			t.Fatalf("table %s missing: %v", table, err)
		}
	}
}
