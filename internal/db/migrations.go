package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

const migrationSQLite = `
CREATE TABLE IF NOT EXISTS upstreams (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    base_url TEXT NOT NULL,
    api_key TEXT NOT NULL,
    format TEXT NOT NULL,
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
);

CREATE INDEX IF NOT EXISTS idx_usage_created ON usage_records(created_at);
CREATE INDEX IF NOT EXISTS idx_usage_ext_key ON usage_records(ext_key_id);
CREATE INDEX IF NOT EXISTS idx_usage_upstream ON usage_records(upstream_id);

CREATE TABLE IF NOT EXISTS response_sessions (
    id TEXT PRIMARY KEY,
    messages TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_resp_sessions_used ON response_sessions(last_used_at);
`

const migrationPG = `
CREATE TABLE IF NOT EXISTS upstreams (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    base_url TEXT NOT NULL,
    api_key TEXT NOT NULL,
    format TEXT NOT NULL,
    daily_token_limit INTEGER NOT NULL DEFAULT 0,
    monthly_token_limit INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP(0) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP(0) NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS upstream_models (
    id BIGSERIAL PRIMARY KEY,
    upstream_id BIGINT NOT NULL REFERENCES upstreams(id) ON DELETE CASCADE,
    model_name TEXT NOT NULL,
    manual INTEGER NOT NULL DEFAULT 0,
    context_length INTEGER NOT NULL DEFAULT 200000,
    max_output_length INTEGER NOT NULL DEFAULT 200000,
    UNIQUE(upstream_id, model_name)
);

CREATE TABLE IF NOT EXISTS ext_keys (
    id BIGSERIAL PRIMARY KEY,
    key TEXT NOT NULL UNIQUE,
    label TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1,
    daily_token_limit INTEGER NOT NULL DEFAULT 0,
    monthly_token_limit INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP(0) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at TIMESTAMP(0)
);

CREATE TABLE IF NOT EXISTS usage_records (
    id BIGSERIAL PRIMARY KEY,
    ext_key_id BIGINT REFERENCES ext_keys(id),
    upstream_id BIGINT,
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
    created_at TIMESTAMP(0) NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_usage_created ON usage_records(created_at);
CREATE INDEX IF NOT EXISTS idx_usage_ext_key ON usage_records(ext_key_id);
CREATE INDEX IF NOT EXISTS idx_usage_upstream ON usage_records(upstream_id);

CREATE TABLE IF NOT EXISTS response_sessions (
    id TEXT PRIMARY KEY,
    messages TEXT NOT NULL,
    created_at TIMESTAMP(0) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at TIMESTAMP(0) NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_resp_sessions_used ON response_sessions(last_used_at);
`

// extraCols lists the columns added after the initial schema, together with
// their ALTER TABLE defaults. Existing databases created before these columns
// existed need them backfilled; fresh databases get them from the CREATE TABLE
// statements above.
var extraCols = []struct {
	table, column, def string
}{
	{"upstreams", "daily_token_limit", "0"},
	{"upstreams", "monthly_token_limit", "0"},
	{"ext_keys", "daily_token_limit", "0"},
	{"ext_keys", "monthly_token_limit", "0"},
	{"upstream_models", "context_length", "200000"},
	{"upstream_models", "max_output_length", "200000"},
	{"usage_records", "cache_read_tokens", "0"},
	{"usage_records", "cache_creation_tokens", "0"},
	{"usage_records", "reasoning_tokens", "0"},
}

// migrateExtraCols ensures columns added after the initial schema exist on
// older databases. It is idempotent: columns present are skipped. Must be
// called after the main migration script has run so the tables exist.
func migrateExtraCols(d *sql.DB) error {
	dialect := DialectOf(d)
	for _, ec := range extraCols {
		exists, err := columnExists(d, dialect, ec.table, ec.column)
		if err != nil {
			return fmt.Errorf("check column %s.%s: %w", ec.table, ec.column, err)
		}
		if exists {
			continue
		}
		stmt := fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s INTEGER NOT NULL DEFAULT %s`, ec.table, ec.column, ec.def)
		if _, err := d.Exec(stmt); err != nil {
			return fmt.Errorf("add column %s.%s: %w", ec.table, ec.column, err)
		}
	}
	return nil
}

func columnExists(d *sql.DB, dialect Dialect, table, col string) (bool, error) {
	switch dialect {
	case DialectPostgres:
		var n int
		err := d.QueryRow(`SELECT COUNT(*) FROM information_schema.columns
			WHERE table_name = $1 AND column_name = $2`, table, col).Scan(&n)
		return n > 0, err
	default:
		rows, err := d.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, table))
		if err != nil {
			return false, err
		}
		defer rows.Close()
		for rows.Next() {
			var cid int
			var name, ctype string
			var notnull, pk int
			var dflt sql.NullString
			if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
				return false, err
			}
			if name == col {
				return true, nil
			}
		}
		return false, nil
	}
}

// dropUpstreamFormatCheck 移除旧库 upstreams 表上的 format CHECK 约束（新库
// 的 CREATE TABLE 已不带约束，无需处理）。校验改为应用层（webapi）。
// 必须在 PRAGMA foreign_keys=ON 之前调用。注意：SQLite 3.25+ 的
// ALTER TABLE RENAME 默认会改写其他表的 REFERENCES 子句（仅当
// PRAGMA legacy_alter_table=ON 时才不改写），与 foreign_keys 设置无关，
// 所以重建期间要临时打开 legacy_alter_table，否则 upstream_models 的
// REFERENCES 会被改成指向已删除的 upstreams_bak。
func dropUpstreamFormatCheck(d *sql.DB) error {
	switch DialectOf(d) {
	case DialectPostgres:
		_, err := d.Exec(`ALTER TABLE upstreams DROP CONSTRAINT IF EXISTS upstreams_format_check`)
		return err
	default:
		var sqlText string
		if err := d.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='upstreams'`).Scan(&sqlText); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				// 表不存在（新库尚未建）：交给主迁移处理
				return nil
			}
			return fmt.Errorf("read upstreams schema: %w", err)
		}
		if !strings.Contains(sqlText, "CHECK(format IN") {
			return nil
		}
		// 备份 -> 重建 -> 还原 -> 清理，整体一个事务；任何一步失败回滚，旧表仍在。
		// 注意：这里的 CREATE TABLE 与迁移脚本 migrationSQLite 里的 upstreams
		// DDL 是重复的，新增列时三处（migrationSQLite、本重建语句、INSERT 列清单）
		// 必须同步。
		tx, err := d.Begin()
		if err != nil {
			return fmt.Errorf("begin rebuild upstreams: %w", err)
		}
		defer tx.Rollback()
		steps := []string{
			`PRAGMA legacy_alter_table=ON`,
			`ALTER TABLE upstreams RENAME TO upstreams_bak`,
			`CREATE TABLE upstreams (
			    id INTEGER PRIMARY KEY AUTOINCREMENT,
			    name TEXT NOT NULL UNIQUE,
			    base_url TEXT NOT NULL,
			    api_key TEXT NOT NULL,
			    format TEXT NOT NULL,
			    daily_token_limit INTEGER NOT NULL DEFAULT 0,
			    monthly_token_limit INTEGER NOT NULL DEFAULT 0,
			    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
			)`,
			`INSERT INTO upstreams (id, name, base_url, api_key, format, daily_token_limit, monthly_token_limit, created_at, updated_at)
			 SELECT id, name, base_url, api_key, format, daily_token_limit, monthly_token_limit, created_at, updated_at FROM upstreams_bak`,
			`DROP TABLE upstreams_bak`,
			`PRAGMA legacy_alter_table=OFF`,
		}
		for i, s := range steps {
			if _, err := tx.Exec(s); err != nil {
				return fmt.Errorf("rebuild upstreams step %d failed: %w", i, err)
			}
		}
		return tx.Commit()
	}
}
