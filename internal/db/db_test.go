package db

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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

// 旧库（含 CHECK 约束）迁移后：约束移除、数据完好、会话表已建
func TestOpenSQLite_DropsFormatCheckAndKeepsData(t *testing.T) {
	oldSchema := `
CREATE TABLE IF NOT EXISTS upstreams (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    base_url TEXT NOT NULL,
    api_key TEXT NOT NULL,
    format TEXT NOT NULL CHECK(format IN ('openai','anthropic')),
    daily_token_limit INTEGER NOT NULL DEFAULT 0,
    monthly_token_limit INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);`
	path := filepath.Join(t.TempDir(), "old.db")
	d, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(oldSchema); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO upstreams (name, base_url, api_key, format) VALUES ('u1','http://x','k1','openai')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO upstreams (name, base_url, api_key, format) VALUES ('u2','http://y','k2','anthropic')`); err != nil {
		t.Fatal(err)
	}
	d.Close()

	got, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer got.Close()

	// 约束已移除：可插入 responses
	if _, err := got.Exec(`INSERT INTO upstreams (name, base_url, api_key, format) VALUES ('u3','http://z','k3','responses')`); err != nil {
		t.Fatalf("insert responses format failed: %v", err)
	}
	// 旧数据完好
	var n int
	if err := got.QueryRow(`SELECT COUNT(*) FROM upstreams WHERE name IN ('u1','u2')`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("old rows: n=%d err=%v", n, err)
	}
	var id1 int64
	if err := got.QueryRow(`SELECT id FROM upstreams WHERE name='u1'`).Scan(&id1); err != nil || id1 != 1 {
		t.Fatalf("row id preserved: id=%d err=%v", id1, err)
	}
	// 会话表已建
	var tname string
	if err := got.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='response_sessions'`).Scan(&tname); err != nil || tname != "response_sessions" {
		t.Fatalf("response_sessions missing: %q err=%v", tname, err)
	}
}

// 新库本来就没 CHECK，迁移幂等
func TestOpenSQLite_FreshDBHasNoCheck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh.db")
	d, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec(`INSERT INTO upstreams (name, base_url, api_key, format) VALUES ('u','http://z','k','responses')`); err != nil {
		t.Fatalf("fresh db rejected responses format: %v", err)
	}
	// 表结构里没有 CHECK
	var sqlText string
	if err := d.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='upstreams'`).Scan(&sqlText); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sqlText, "CHECK") {
		t.Fatalf("upstreams still has CHECK: %s", sqlText)
	}
}
