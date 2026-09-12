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
	// 新库 upstreams 带启用开关列
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('upstreams') WHERE name='enabled'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("upstreams.enabled missing: n=%d err=%v", n, err)
	}
}

// 旧库（或别的程序在同一 schema 建的同名表）里的 ext_keys 可能缺 label/enabled：
// CREATE TABLE IF NOT EXISTS 不会给已存在的表补列，缺列会潜伏到查询时才以
// 42703 暴露（list ext keys: column "label" does not exist）。迁移必须补上。
func TestOpenSQLite_BackfillsMissingExtKeyColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no-label.db")
	d, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`CREATE TABLE ext_keys (
	    id INTEGER PRIMARY KEY AUTOINCREMENT,
	    key TEXT NOT NULL UNIQUE,
	    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	    last_used_at DATETIME
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO ext_keys (key) VALUES ('all-sk-old')`); err != nil {
		t.Fatal(err)
	}
	d.Close()

	got, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer got.Close()

	var label string
	var enabled int
	if err := got.QueryRow(`SELECT label, enabled FROM ext_keys WHERE key='all-sk-old'`).Scan(&label, &enabled); err != nil {
		t.Fatalf("legacy row after migration: %v", err)
	}
	if label != "" || enabled != 1 {
		t.Fatalf("legacy row label=%q enabled=%d, want \"\" and 1", label, enabled)
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

// 旧库（含 CHECK 约束、外键、内联 UNIQUE）迁移后：约束全部移除、数据完好、
// 软删除列与部分唯一索引就位、会话表已建
func TestOpenSQLite_DropsConstraintsAndKeepsData(t *testing.T) {
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
);
CREATE TABLE IF NOT EXISTS upstream_models (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    upstream_id INTEGER NOT NULL REFERENCES upstreams(id) ON DELETE CASCADE,
    model_name TEXT NOT NULL,
    manual INTEGER NOT NULL DEFAULT 0,
    context_length INTEGER NOT NULL DEFAULT 200000,
    max_output_length INTEGER NOT NULL DEFAULT 200000,
    UNIQUE(upstream_id, model_name)
);
CREATE TABLE IF NOT EXISTS ext_keys (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    key TEXT NOT NULL UNIQUE,
    label TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1,
    daily_token_limit INTEGER NOT NULL DEFAULT 0,
    monthly_token_limit INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at DATETIME
);
CREATE TABLE IF NOT EXISTS usage_records (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ext_key_id INTEGER REFERENCES ext_keys(id),
    upstream_id INTEGER,
    upstream_name TEXT NOT NULL,
    model TEXT NOT NULL,
    in_format TEXT NOT NULL,
    up_format TEXT NOT NULL,
    prompt_tokens INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens INTEGER NOT NULL DEFAULT 0,
    cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
    reasoning_tokens INTEGER NOT NULL DEFAULT 0,
    stream INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'ok',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
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
	if _, err := d.Exec(`INSERT INTO upstream_models (upstream_id, model_name) VALUES (1,'m1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO ext_keys (key, label) VALUES ('all-sk-old','legacy')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO usage_records (ext_key_id, upstream_id, upstream_name, model, in_format, up_format) VALUES (1, 1, 'u1', 'm1', 'anthropic', 'openai')`); err != nil {
		t.Fatal(err)
	}
	d.Close()

	got, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer got.Close()

	// CHECK 已移除：可插入 responses
	if _, err := got.Exec(`INSERT INTO upstreams (name, base_url, api_key, format) VALUES ('u3','http://z','k3','responses')`); err != nil {
		t.Fatalf("insert responses format failed: %v", err)
	}
	// 旧数据完好
	var n int
	if err := got.QueryRow(`SELECT COUNT(*) FROM upstreams WHERE name IN ('u1','u2')`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("old rows: n=%d err=%v", n, err)
	}
	// enabled 列经 extraCols 回填后随重建保留，老行默认启用
	var enabled int
	if err := got.QueryRow(`SELECT enabled FROM upstreams WHERE name='u1'`).Scan(&enabled); err != nil || enabled != 1 {
		t.Fatalf("enabled backfill: v=%d err=%v", enabled, err)
	}
	var id1 int64
	if err := got.QueryRow(`SELECT id FROM upstreams WHERE name='u1'`).Scan(&id1); err != nil || id1 != 1 {
		t.Fatalf("row id preserved: id=%d err=%v", id1, err)
	}
	// join 可见（表本身完好）
	var cnt int
	if err := got.QueryRow(`SELECT COUNT(*) FROM upstream_models m JOIN upstreams u ON u.id = m.upstream_id WHERE m.model_name='m1' AND u.name='u1'`).Scan(&cnt); err != nil || cnt != 1 {
		t.Fatalf("model join after rebuild: cnt=%d err=%v", cnt, err)
	}
	// 所有表 schema 均无 REFERENCES/内联 UNIQUE/CHECK，且软删除表带 is_active
	for table, wantActiveCol := range map[string]bool{
		"upstreams": true, "upstream_models": true, "ext_keys": true, "usage_records": false,
	} {
		var sqlText string
		if err := got.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&sqlText); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(sqlText, "REFERENCES") || strings.Contains(sqlText, "CHECK(") || strings.Contains(sqlText, "UNIQUE") {
			t.Fatalf("%s still has constraints: %s", table, sqlText)
		}
		if strings.Contains(sqlText, "upstreams_bak") || strings.Contains(sqlText, "ext_keys_bak") {
			t.Fatalf("%s references backup table: %s", table, sqlText)
		}
		if wantActiveCol && !strings.Contains(sqlText, "is_active") {
			t.Fatalf("%s missing is_active: %s", table, sqlText)
		}
	}
	// 外键已移除：坏 upstream_id / ext_key_id 不再被拒绝
	if _, err := got.Exec(`INSERT INTO upstream_models (upstream_id, model_name) VALUES (999, 'm2')`); err != nil {
		t.Fatalf("no FK expected, insert bad upstream_id: %v", err)
	}
	if _, err := got.Exec(`INSERT INTO usage_records (ext_key_id, upstream_name, model, in_format, up_format) VALUES (999, 'x', 'y', 'z', 'w')`); err != nil {
		t.Fatalf("no FK expected, insert bad ext_key_id: %v", err)
	}
	// 部分唯一索引存在；软删除后同名可重建
	for _, idx := range []string{"idx_upstreams_name", "idx_upstream_models_uid_name", "idx_ext_keys_key"} {
		var name string
		if err := got.QueryRow(`SELECT name FROM sqlite_master WHERE type='index' AND name=?`, idx).Scan(&name); err != nil {
			t.Fatalf("partial unique index %s missing: %v", idx, err)
		}
	}
	if _, err := got.Exec(`UPDATE upstreams SET is_active = 0 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := got.Exec(`INSERT INTO upstreams (name, base_url, api_key, format) VALUES ('u1','http://x2','k1b','openai')`); err != nil {
		t.Fatalf("re-create same name after soft delete: %v", err)
	}
	// 活跃行唯一性仍被强制
	if _, err := got.Exec(`INSERT INTO upstreams (name, base_url, api_key, format) VALUES ('u1','http://x3','k1c','openai')`); err == nil {
		t.Fatal("duplicate active name should violate partial unique index")
	}
	// 会话表已建
	var tname string
	if err := got.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='response_sessions'`).Scan(&tname); err != nil || tname != "response_sessions" {
		t.Fatalf("response_sessions missing: %q err=%v", tname, err)
	}
}

// allowed_models 列就位的三条路径：新库 CREATE TABLE 自带；旧库（无 UNIQUE）
// 走 ALTER 回填；旧库（带内联 UNIQUE）走软删除重建，列随 spec 就位。
// 旧数据保留，默认空串（= 不限制）。
func TestOpenSQLite_BackfillsAllowedModels(t *testing.T) {
	// 新库：CREATE TABLE 已含列
	fresh := filepath.Join(t.TempDir(), "fresh.db")
	fd, err := OpenSQLite(fresh)
	if err != nil {
		t.Fatal(err)
	}
	defer fd.Close()
	if _, err := fd.Exec(`INSERT INTO ext_keys (key, label) VALUES ('all-sk-new','brand')`); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name   string
		unique bool // 旧表是否带内联 UNIQUE（触发软删除重建路径）
	}{
		{"alter", false},
		{"rebuild", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "old.db")
			d, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			keyDDL := "key TEXT NOT NULL"
			if tc.unique {
				keyDDL += " UNIQUE"
			}
			if _, err := d.Exec(`CREATE TABLE ext_keys (
			    id INTEGER PRIMARY KEY AUTOINCREMENT,
			    ` + keyDDL + `,
			    label TEXT NOT NULL DEFAULT '',
			    enabled INTEGER NOT NULL DEFAULT 1,
			    daily_token_limit INTEGER NOT NULL DEFAULT 0,
			    monthly_token_limit INTEGER NOT NULL DEFAULT 0,
			    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			    last_used_at DATETIME,
			    is_active INTEGER NOT NULL DEFAULT 1
			)`); err != nil {
				t.Fatal(err)
			}
			if _, err := d.Exec(`INSERT INTO ext_keys (key, label) VALUES ('all-sk-old','legacy')`); err != nil {
				t.Fatal(err)
			}
			d.Close()

			got, err := OpenSQLite(path)
			if err != nil {
				t.Fatal(err)
			}
			defer got.Close()

			var n int
			if err := got.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('ext_keys') WHERE name='allowed_models'`).Scan(&n); err != nil || n != 1 {
				t.Fatalf("allowed_models missing: n=%d err=%v", n, err)
			}
			var allowed string
			if err := got.QueryRow(`SELECT allowed_models FROM ext_keys WHERE key='all-sk-old'`).Scan(&allowed); err != nil || allowed != "" {
				t.Fatalf("allowed_models backfill: v=%q err=%v", allowed, err)
			}
			// 回填后可直接写入白名单
			if _, err := got.Exec(`UPDATE ext_keys SET allowed_models='["a/b"]' WHERE key='all-sk-old'`); err != nil {
				t.Fatalf("write allowed_models: %v", err)
			}
			if err := got.QueryRow(`SELECT allowed_models FROM ext_keys WHERE key='all-sk-old'`).Scan(&allowed); err != nil || allowed != `["a/b"]` {
				t.Fatalf("allowed_models roundtrip: v=%q err=%v", allowed, err)
			}
		})
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
