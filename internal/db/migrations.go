package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// 表设计原则：不使用外键约束（删除一律应用层软删除，is_active=0 标记，
// 历史记录靠存储的 name/id 关联），唯一性用「仅活跃行」的部分唯一索引实现
// ——软删除的行不占唯一名额，同名资源删除后可重建。usage_records /
// conversation_records 等归档表只存 id/name 快照，不依赖引用完整性。
const migrationSQLite = `
CREATE TABLE IF NOT EXISTS upstreams (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    base_url TEXT NOT NULL,
    api_key TEXT NOT NULL,
    format TEXT NOT NULL,
    daily_token_limit INTEGER NOT NULL DEFAULT 0,
    monthly_token_limit INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    is_active INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS upstream_models (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    upstream_id INTEGER NOT NULL,
    model_name TEXT NOT NULL,
    manual INTEGER NOT NULL DEFAULT 0,
    context_length INTEGER NOT NULL DEFAULT 200000,
    max_output_length INTEGER NOT NULL DEFAULT 200000,
    is_active INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS ext_keys (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    key TEXT NOT NULL,
    label TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1,
    daily_token_limit INTEGER NOT NULL DEFAULT 0,
    monthly_token_limit INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at DATETIME,
    is_active INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS usage_records (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ext_key_id INTEGER,
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
    name TEXT NOT NULL,
    base_url TEXT NOT NULL,
    api_key TEXT NOT NULL,
    format TEXT NOT NULL,
    daily_token_limit INTEGER NOT NULL DEFAULT 0,
    monthly_token_limit INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP(0) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP(0) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    is_active INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS upstream_models (
    id BIGSERIAL PRIMARY KEY,
    upstream_id BIGINT NOT NULL,
    model_name TEXT NOT NULL,
    manual INTEGER NOT NULL DEFAULT 0,
    context_length INTEGER NOT NULL DEFAULT 200000,
    max_output_length INTEGER NOT NULL DEFAULT 200000,
    is_active INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS ext_keys (
    id BIGSERIAL PRIMARY KEY,
    key TEXT NOT NULL,
    label TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1,
    daily_token_limit INTEGER NOT NULL DEFAULT 0,
    monthly_token_limit INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP(0) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at TIMESTAMP(0),
    is_active INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS usage_records (
    id BIGSERIAL PRIMARY KEY,
    ext_key_id BIGINT,
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

-- conversation_records 归档每次网关对话（仅 PG；SQLite 不建此表）。
-- request_ir/response_ir 是归一化 IR 的 JSON（含工具调用与思维链，可查询）；
-- request_raw/response_raw 是入站请求体与发给客户端的原始字节（保真回放）。
CREATE TABLE IF NOT EXISTS conversation_records (
    id BIGSERIAL PRIMARY KEY,
    ext_key_id BIGINT,
    upstream_id BIGINT,
    upstream_name TEXT NOT NULL,
    model TEXT NOT NULL,
    in_format TEXT NOT NULL,
    up_format TEXT NOT NULL,
    harness TEXT NOT NULL,
    user_agent TEXT NOT NULL,
    stream INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'ok',
    prompt_tokens INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens INTEGER NOT NULL DEFAULT 0,
    cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
    reasoning_tokens INTEGER NOT NULL DEFAULT 0,
    request_ir JSONB NOT NULL DEFAULT '{}'::jsonb,
    response_ir JSONB NOT NULL DEFAULT '{}'::jsonb,
    request_raw BYTEA NOT NULL,
    response_raw BYTEA NOT NULL,
    created_at TIMESTAMP(0) NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_conv_created ON conversation_records(created_at);
CREATE INDEX IF NOT EXISTS idx_conv_ext_key ON conversation_records(ext_key_id);
CREATE INDEX IF NOT EXISTS idx_conv_harness ON conversation_records(harness);
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
	{"upstreams", "is_active", "1"},
	{"ext_keys", "daily_token_limit", "0"},
	{"ext_keys", "monthly_token_limit", "0"},
	{"ext_keys", "is_active", "1"},
	{"upstream_models", "is_active", "1"},
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

// migrateSoftDelete 把旧库升级到「无外键 + 软删除」模式：
//   - PG：补 is_active 列、DROP 旧的 REFERENCES/内联 UNIQUE 约束、建部分唯一索引；
//   - SQLite：对 schema 不达标的表整体重建（SQLite 无法就地删约束），
//     同时顺带移除旧库 upstreams 上的 format CHECK。
//
// 必须在 PRAGMA foreign_keys=ON 之前调用（OpenSQLite 已保证）。SQLite
// 3.25+ 的 ALTER TABLE RENAME 默认会改写其他表的 REFERENCES 子句（仅当
// PRAGMA legacy_alter_table=ON 时才不改写），重建期间需临时打开
// legacy_alter_table，否则未重建表的 REFERENCES 会被改成指向 _bak 表。
func migrateSoftDelete(d *sql.DB) error {
	if DialectOf(d) == DialectPostgres {
		return migrateSoftDeletePG(d)
	}
	return migrateSoftDeleteSQLite(d)
}

func migrateSoftDeletePG(d *sql.DB) error {
	steps := []string{
		// 旧库补列（新库 CREATE TABLE 已含；SQLite 走 extraCols，此处仅 PG）
		`ALTER TABLE upstreams ADD COLUMN IF NOT EXISTS is_active INTEGER NOT NULL DEFAULT 1`,
		`ALTER TABLE upstream_models ADD COLUMN IF NOT EXISTS is_active INTEGER NOT NULL DEFAULT 1`,
		`ALTER TABLE ext_keys ADD COLUMN IF NOT EXISTS is_active INTEGER NOT NULL DEFAULT 1`,
		// 去掉所有外键
		`ALTER TABLE upstreams DROP CONSTRAINT IF EXISTS upstreams_format_check`,
		`ALTER TABLE upstream_models DROP CONSTRAINT IF EXISTS upstream_models_upstream_id_fkey`,
		`ALTER TABLE usage_records DROP CONSTRAINT IF EXISTS usage_records_ext_key_id_fkey`,
		`ALTER TABLE conversation_records DROP CONSTRAINT IF EXISTS conversation_records_ext_key_id_fkey`,
		// 去掉内联 UNIQUE 约束（连同其索引），改由下面的部分唯一索引接管
		`ALTER TABLE upstreams DROP CONSTRAINT IF EXISTS upstreams_name_key`,
		`ALTER TABLE upstream_models DROP CONSTRAINT IF EXISTS upstream_models_upstream_id_model_name_key`,
		`ALTER TABLE ext_keys DROP CONSTRAINT IF EXISTS ext_keys_key_key`,
	}
	for _, s := range steps {
		if _, err := d.Exec(s); err != nil {
			return fmt.Errorf("soft-delete migrate: %q: %w", s, err)
		}
	}
	for _, s := range uniqueIndexDDL() {
		if _, err := d.Exec(s); err != nil {
			return fmt.Errorf("soft-delete migrate: %q: %w", s, err)
		}
	}
	return nil
}

// sqliteTableSpec 描述一次 SQLite 表重建：重建判定与新旧 DDL。
type sqliteTableSpec struct {
	table     string // 表名
	rebuildIf func(sqlText string) bool
	// create 为新表 DDL（无外键、无内联 UNIQUE、含 is_active——usage_records
	// 无软删除列，仅去 REFERENCES）。insertCols 为新表列清单，selectExprs 为
	// 从旧表取数的表达式清单（逐列对应）。
	create      string
	insertCols  []string
	selectExprs []string
}

// sqliteSoftDeleteSpecs 列出需要重建的表。注意：重建 DDL 与 migrationSQLite
// 中的 CREATE TABLE 是重复的，后续新增列时两处必须同步。
var sqliteSoftDeleteSpecs = []sqliteTableSpec{
	{
		table: "upstreams",
		rebuildIf: func(s string) bool {
			return strings.Contains(s, "CHECK(") || strings.Contains(s, "UNIQUE")
		},
		create: `CREATE TABLE upstreams (
		    id INTEGER PRIMARY KEY AUTOINCREMENT,
		    name TEXT NOT NULL,
		    base_url TEXT NOT NULL,
		    api_key TEXT NOT NULL,
		    format TEXT NOT NULL,
		    daily_token_limit INTEGER NOT NULL DEFAULT 0,
		    monthly_token_limit INTEGER NOT NULL DEFAULT 0,
		    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		    is_active INTEGER NOT NULL DEFAULT 1
		)`,
		insertCols:  []string{"id", "name", "base_url", "api_key", "format", "daily_token_limit", "monthly_token_limit", "created_at", "updated_at", "is_active"},
		selectExprs: []string{"id", "name", "base_url", "api_key", "format", "daily_token_limit", "monthly_token_limit", "created_at", "updated_at", "is_active"},
	},
	{
		table: "upstream_models",
		rebuildIf: func(s string) bool {
			return strings.Contains(s, "REFERENCES") || strings.Contains(s, "UNIQUE")
		},
		create: `CREATE TABLE upstream_models (
		    id INTEGER PRIMARY KEY AUTOINCREMENT,
		    upstream_id INTEGER NOT NULL,
		    model_name TEXT NOT NULL,
		    manual INTEGER NOT NULL DEFAULT 0,
		    context_length INTEGER NOT NULL DEFAULT 200000,
		    max_output_length INTEGER NOT NULL DEFAULT 200000,
		    is_active INTEGER NOT NULL DEFAULT 1
		)`,
		insertCols:  []string{"id", "upstream_id", "model_name", "manual", "context_length", "max_output_length", "is_active"},
		selectExprs: []string{"id", "upstream_id", "model_name", "manual", "context_length", "max_output_length", "is_active"},
	},
	{
		table: "ext_keys",
		rebuildIf: func(s string) bool {
			return strings.Contains(s, "UNIQUE")
		},
		create: `CREATE TABLE ext_keys (
		    id INTEGER PRIMARY KEY AUTOINCREMENT,
		    key TEXT NOT NULL,
		    label TEXT NOT NULL DEFAULT '',
		    enabled INTEGER NOT NULL DEFAULT 1,
		    daily_token_limit INTEGER NOT NULL DEFAULT 0,
		    monthly_token_limit INTEGER NOT NULL DEFAULT 0,
		    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		    last_used_at DATETIME,
		    is_active INTEGER NOT NULL DEFAULT 1
		)`,
		insertCols:  []string{"id", "key", "label", "enabled", "daily_token_limit", "monthly_token_limit", "created_at", "last_used_at", "is_active"},
		selectExprs: []string{"id", "key", "label", "enabled", "daily_token_limit", "monthly_token_limit", "created_at", "last_used_at", "is_active"},
	},
	{
		table: "usage_records",
		rebuildIf: func(s string) bool {
			return strings.Contains(s, "REFERENCES")
		},
		create: `CREATE TABLE usage_records (
		    id INTEGER PRIMARY KEY AUTOINCREMENT,
		    ext_key_id INTEGER,
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
		)`,
		insertCols:  []string{"id", "ext_key_id", "upstream_id", "upstream_name", "model", "in_format", "up_format", "prompt_tokens", "completion_tokens", "total_tokens", "cache_read_tokens", "cache_creation_tokens", "reasoning_tokens", "stream", "status", "created_at"},
		selectExprs: []string{"id", "ext_key_id", "upstream_id", "upstream_name", "model", "in_format", "up_format", "prompt_tokens", "completion_tokens", "total_tokens", "cache_read_tokens", "cache_creation_tokens", "reasoning_tokens", "stream", "status", "created_at"},
	},
}

func migrateSoftDeleteSQLite(d *sql.DB) error {
	tx, err := d.Begin()
	if err != nil {
		return fmt.Errorf("begin soft-delete migrate: %w", err)
	}
	defer tx.Rollback()
	// 重建期间抑制 REFERENCES 子句改写（见函数注释）；结束前关闭。
	if _, err := tx.Exec(`PRAGMA legacy_alter_table=ON`); err != nil {
		return fmt.Errorf("enable legacy_alter_table: %w", err)
	}
	for _, spec := range sqliteSoftDeleteSpecs {
		var sqlText string
		if err := tx.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name=?`, spec.table).Scan(&sqlText); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue // 表不存在（新库尚未建）：交给主迁移处理
			}
			return fmt.Errorf("read %s schema: %w", spec.table, err)
		}
		if !spec.rebuildIf(sqlText) {
			continue
		}
		bak := spec.table + "_bak"
		steps := []string{
			fmt.Sprintf(`ALTER TABLE %s RENAME TO %s`, spec.table, bak),
			spec.create,
			fmt.Sprintf(`INSERT INTO %s (%s) SELECT %s FROM %s`,
				spec.table, strings.Join(spec.insertCols, ", "), strings.Join(spec.selectExprs, ", "), bak),
			fmt.Sprintf(`DROP TABLE %s`, bak),
		}
		for i, s := range steps {
			if _, err := tx.Exec(s); err != nil {
				return fmt.Errorf("rebuild %s step %d failed: %w", spec.table, i, err)
			}
		}
	}
	if _, err := tx.Exec(`PRAGMA legacy_alter_table=OFF`); err != nil {
		return fmt.Errorf("disable legacy_alter_table: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit soft-delete migrate: %w", err)
	}
	// 重建会连带删除旧表上的索引（含 sqlite_autoindex），这里统一重建。
	// 部分唯一索引必须在 is_active 列就位后创建，因此不放在 migrationSQLite 里。
	for _, s := range append(uniqueIndexDDL(), usageIndexDDL()...) {
		if _, err := d.Exec(s); err != nil {
			return fmt.Errorf("soft-delete migrate: %q: %w", s, err)
		}
	}
	return nil
}

// uniqueIndexDDL 返回「仅活跃行」的部分唯一索引 DDL（两种方言通用）。
// 软删除的行不占唯一名额，同名资源删除后可重建。
func uniqueIndexDDL() []string {
	return []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_upstreams_name ON upstreams(name) WHERE is_active = 1`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_upstream_models_uid_name ON upstream_models(upstream_id, model_name) WHERE is_active = 1`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_ext_keys_key ON ext_keys(key) WHERE is_active = 1`,
	}
}

func usageIndexDDL() []string {
	return []string{
		`CREATE INDEX IF NOT EXISTS idx_usage_created ON usage_records(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_usage_ext_key ON usage_records(ext_key_id)`,
		`CREATE INDEX IF NOT EXISTS idx_usage_upstream ON usage_records(upstream_id)`,
	}
}

// MigratePGForTest 对已连接的 PG 执行与 OpenPG 相同的完整迁移管线。导出供
// 其他包（如 gateway）的 PG e2e 测试在独立 schema 里建表；生产代码用
// OpenPG，不经过此函数。
func MigratePGForTest(d *sql.DB) error {
	if _, err := d.Exec(migrationPG); err != nil {
		return err
	}
	if err := migrateExtraCols(d); err != nil {
		return err
	}
	return migrateSoftDelete(d)
}
