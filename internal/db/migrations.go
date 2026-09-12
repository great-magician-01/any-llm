package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/great-magician-01/any-llm/internal/logger"
)

// 表设计原则：不使用外键约束（删除一律应用层软删除，is_active=0 标记，
// 历史记录靠存储的 name/id 关联），唯一性用「仅活跃行」的部分唯一索引实现
// ——软删除的行不占唯一名额，同名资源删除后可重建。usage_records /
// conversation_records 等归档表只存 id/name 快照，不依赖引用完整性。
//
// 注意：这里的 CREATE TABLE 与 sqliteSoftDeleteSpecs 里的重建 DDL 是重复的，
// 新增/修改列时两处（及各自的 insertCols/selectExprs）必须同步。
const migrationSQLite = `
CREATE TABLE IF NOT EXISTS upstreams (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    base_url TEXT NOT NULL,
    api_key TEXT NOT NULL,
    format TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
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
    remark TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1,
    daily_token_limit INTEGER NOT NULL DEFAULT 0,
    monthly_token_limit INTEGER NOT NULL DEFAULT 0,
    allowed_models TEXT NOT NULL DEFAULT '',
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
    duration_ms INTEGER NOT NULL DEFAULT 0,
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

-- model_aliases：固定对外模型名。客户端用别名请求，网关按绑定优先级
-- 依次尝试 model_alias_bindings 里的「上游+真实模型」，实现对外名称不变、
-- 内里自由切换与故障转移。
CREATE TABLE IF NOT EXISTS model_aliases (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    is_active INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS model_alias_bindings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    alias_id INTEGER NOT NULL,
    upstream_id INTEGER NOT NULL,
    model_name TEXT NOT NULL,
    priority INTEGER NOT NULL DEFAULT 0,
    is_active INTEGER NOT NULL DEFAULT 1
);

-- balance_snapshots：厂商余额/额度快照（后台轮询或手动刷新写入的归档表，
-- 只存 upstream id/name 快照，无外键、无软删除）。payload 是归一化 JSON。
CREATE TABLE IF NOT EXISTS balance_snapshots (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    upstream_id INTEGER NOT NULL,
    upstream_name TEXT NOT NULL,
    vendor TEXT NOT NULL,
    payload TEXT NOT NULL,
    created_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_balance_snapshots_upstream ON balance_snapshots(upstream_id, id DESC);
`

const migrationPG = `
CREATE TABLE IF NOT EXISTS upstreams (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    base_url TEXT NOT NULL,
    api_key TEXT NOT NULL,
    format TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
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
    remark TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1,
    daily_token_limit INTEGER NOT NULL DEFAULT 0,
    monthly_token_limit INTEGER NOT NULL DEFAULT 0,
    allowed_models TEXT NOT NULL DEFAULT '',
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
    duration_ms INTEGER NOT NULL DEFAULT 0,
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

CREATE TABLE IF NOT EXISTS model_aliases (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    created_at TIMESTAMP(0) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP(0) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    is_active INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS model_alias_bindings (
    id BIGSERIAL PRIMARY KEY,
    alias_id BIGINT NOT NULL,
    upstream_id BIGINT NOT NULL,
    model_name TEXT NOT NULL,
    priority INTEGER NOT NULL DEFAULT 0,
    is_active INTEGER NOT NULL DEFAULT 1
);

-- conversation_records 不再由迁移建表：改为应用层按月分表
-- （conversation_records_YYYY_MM），由 model.EnsureConversationShard 按需创建，
-- 见 docs/conversation-sharding.md。存量库的旧表原地保留为历史分表，
-- 读取由应用层跨分表合并。migrateSoftDeletePG 里对该表旧外键的
-- DROP CONSTRAINT IF EXISTS 保留，用于存量库升级。

-- balance_snapshots：厂商余额/额度快照（后台轮询或手动刷新写入的归档表，
-- 只存 upstream id/name 快照，无外键、无软删除）。payload 是归一化 JSON。
CREATE TABLE IF NOT EXISTS balance_snapshots (
    id BIGSERIAL PRIMARY KEY,
    upstream_id BIGINT NOT NULL,
    upstream_name TEXT NOT NULL,
    vendor TEXT NOT NULL,
    payload TEXT NOT NULL,
    created_at TIMESTAMP(0) NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_balance_snapshots_upstream ON balance_snapshots(upstream_id, id DESC);
`

// extraCols 列出建表语句之外还要保证存在的列，连同 ALTER TABLE 补列用的完整
// 列定义（类型 + NOT NULL + 默认值）。老库需要回填，新库由上面的 CREATE TABLE
// 提供；CREATE TABLE IF NOT EXISTS 对已存在的表是空操作，缺列会一直潜伏到查询
// 时才以 42703 暴露（list ext keys: column "label" does not exist），所以除了
// 建表语句，这里也必须列一份。
//
// 改过名的列**不要**写在这里：补列只看列名在不在，会把改名前的旧列名又补出来。
// 改名统一交给 migrateRenamedCols（ext_keys.label 就是这么从 name 改回来的，
// 它还会顺带清掉补列留下的空壳列）。因此这里只列：初始 schema 之后新增的列
// （remark / allowed_models / is_active / enabled / token 限额等）。
//
// 补列只在列不存在时执行，幂等；coldef 需能直接用于两种方言的 ADD COLUMN。
var extraCols = []struct {
	table, column, coldef string
}{
	{"upstreams", "daily_token_limit", "INTEGER NOT NULL DEFAULT 0"},
	{"upstreams", "monthly_token_limit", "INTEGER NOT NULL DEFAULT 0"},
	{"upstreams", "is_active", "INTEGER NOT NULL DEFAULT 1"},
	{"upstreams", "enabled", "INTEGER NOT NULL DEFAULT 1"},
	{"ext_keys", "enabled", "INTEGER NOT NULL DEFAULT 1"},
	{"ext_keys", "daily_token_limit", "INTEGER NOT NULL DEFAULT 0"},
	{"ext_keys", "monthly_token_limit", "INTEGER NOT NULL DEFAULT 0"},
	{"ext_keys", "is_active", "INTEGER NOT NULL DEFAULT 1"},
	// 按 key 的模型白名单：'' = 不限；否则 JSON 数组文本（对外模型名）
	{"ext_keys", "allowed_models", "TEXT NOT NULL DEFAULT ''"},
	// 备注（原始 schema 里没有，老库回填）。名称列是初始 schema 的 label，
	// 见上面「改过名的列不要写在这里」
	{"ext_keys", "remark", "TEXT NOT NULL DEFAULT ''"},
	{"upstream_models", "is_active", "INTEGER NOT NULL DEFAULT 1"},
	{"upstream_models", "context_length", "INTEGER NOT NULL DEFAULT 200000"},
	{"upstream_models", "max_output_length", "INTEGER NOT NULL DEFAULT 200000"},
	{"usage_records", "cache_read_tokens", "INTEGER NOT NULL DEFAULT 0"},
	{"usage_records", "cache_creation_tokens", "INTEGER NOT NULL DEFAULT 0"},
	{"usage_records", "reasoning_tokens", "INTEGER NOT NULL DEFAULT 0"},
	{"usage_records", "duration_ms", "INTEGER NOT NULL DEFAULT 0"},
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
		stmt := fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, ec.table, ec.column, ec.coldef)
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

// renamedCols 列出改过名的列，{表, 旧列名, 新列名}。改名只在「旧列存在且新列
// 不存在」时执行，幂等；新旧列并存时走 healCoexistingColumns（见下）。
var renamedCols = []struct {
	table, from, to string
}{
	// ext_keys.name → label：撤销 2026-09-13 那次 label → name 改名，名称仍存在
	// 最初的 label 列里（对外显示为「名称」，备注另用 remark 列）。
	{"ext_keys", "name", "label"},
}

// migrateRenamedCols 把老库的列名升级到当前 schema。必须跑在 migrateSoftDelete
// 之后：SQLite 重建 spec 仍按未改名的列名取数。
func migrateRenamedCols(d *sql.DB) error {
	dialect := DialectOf(d)
	for _, rc := range renamedCols {
		from, err := columnExists(d, dialect, rc.table, rc.from)
		if err != nil {
			return fmt.Errorf("check column %s.%s: %w", rc.table, rc.from, err)
		}
		if !from {
			continue // 旧列不存在：新库，或早就改过名了
		}
		to, err := columnExists(d, dialect, rc.table, rc.to)
		if err != nil {
			return fmt.Errorf("check column %s.%s: %w", rc.table, rc.to, err)
		}
		if to {
			if err := healCoexistingColumns(d, rc.table, rc.from, rc.to); err != nil {
				return err
			}
			continue
		}
		if err := renameColumn(d, rc.table, rc.from, rc.to); err != nil {
			return err
		}
	}
	return nil
}

// healCoexistingColumns 处理「改名前后两列并存」。这种局面是补列兜底造成的：
// 改名已经跑过（数据都在 to 列），之后 extraCols 又把 from 这个旧列名当缺失列
// 补了回来——补出来的列恒为空，查询却按 from 取值，数据就「看不见」了。
// 只删得掉确认为空的列（不丢数据）：
//   - to 列整列为空（补出来的空壳）：删掉它，把 from 改成 to；
//   - from 列整列为空（数据已在 to）：删掉 from 就行；
//   - 两列都有数据：删哪列都丢数据，只打警告并给出人工 DDL，留给人决定。
func healCoexistingColumns(d *sql.DB, table, from, to string) error {
	fromEmpty, err := columnAllEmpty(d, table, from)
	if err != nil {
		return err
	}
	toEmpty, err := columnAllEmpty(d, table, to)
	if err != nil {
		return err
	}
	switch {
	case !fromEmpty && !toEmpty:
		logger.Warn("migrate: both columns hold data, left untouched",
			"table", table, "old", from, "new", to,
			"hint", fmt.Sprintf("确认保留哪一列后手动处理，例如：ALTER TABLE %s DROP COLUMN %s; ALTER TABLE %s RENAME COLUMN %s TO %s",
				table, to, table, from, to))
		return nil
	case fromEmpty:
		return dropColumn(d, table, from)
	default: // toEmpty：空壳列删掉，旧列改成新名
		tx, err := d.Begin()
		if err != nil {
			return fmt.Errorf("begin column heal on %s: %w", table, err)
		}
		defer tx.Rollback()
		if _, err := tx.Exec(fmt.Sprintf(`ALTER TABLE %s DROP COLUMN %s`, table, to)); err != nil {
			return fmt.Errorf("drop empty column %s.%s: %w", table, to, err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`ALTER TABLE %s RENAME COLUMN %s TO %s`, table, from, to)); err != nil {
			return fmt.Errorf("rename column %s.%s to %s: %w", table, from, to, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit column heal on %s: %w", table, err)
		}
		logger.Info("migrate: dropped empty duplicate column", "table", table, "dropped", to, "renamed", from)
		return nil
	}
}

// columnAllEmpty 报告列在整张表里是否都没有值（只适用于文本列）。
// IS NOT NULL 兼顾没写 DEFAULT 的老表。
func columnAllEmpty(d *sql.DB, table, col string) (bool, error) {
	var n int
	q := fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE %s IS NOT NULL AND %s <> ''`, table, col, col)
	if err := d.QueryRow(q).Scan(&n); err != nil {
		return false, fmt.Errorf("count non-empty %s.%s: %w", table, col, err)
	}
	return n == 0, nil
}

// dropColumn 删除一列（调用方必须先确认该列为空）。
func dropColumn(d *sql.DB, table, col string) error {
	if _, err := d.Exec(fmt.Sprintf(`ALTER TABLE %s DROP COLUMN %s`, table, col)); err != nil {
		return fmt.Errorf("drop empty column %s.%s: %w", table, col, err)
	}
	logger.Info("migrate: dropped empty duplicate column", "table", table, "dropped", col)
	return nil
}

// renameColumn 执行一次 ALTER TABLE ... RENAME COLUMN。
func renameColumn(d *sql.DB, table, from, to string) error {
	stmt := fmt.Sprintf(`ALTER TABLE %s RENAME COLUMN %s TO %s`, table, from, to)
	if _, err := d.Exec(stmt); err != nil {
		return fmt.Errorf("rename column %s.%s to %s: %w", table, from, to, err)
	}
	logger.Info("migrate: renamed column", "table", table, "from", from, "to", to)
	return nil
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
		// 注意：按 PG 默认约束名 DROP；若旧库约束是手工建的、命名非默认，
		// 这里 DROP 不到，旧 UNIQUE/FK 会与部分唯一索引并存（软删除后同名
		// 重建会被旧约束拒绝），需要人工处理。
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
		    enabled INTEGER NOT NULL DEFAULT 1,
		    daily_token_limit INTEGER NOT NULL DEFAULT 0,
		    monthly_token_limit INTEGER NOT NULL DEFAULT 0,
		    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		    is_active INTEGER NOT NULL DEFAULT 1
		)`,
		insertCols:  []string{"id", "name", "base_url", "api_key", "format", "enabled", "daily_token_limit", "monthly_token_limit", "created_at", "updated_at", "is_active"},
		selectExprs: []string{"id", "name", "base_url", "api_key", "format", "enabled", "daily_token_limit", "monthly_token_limit", "created_at", "updated_at", "is_active"},
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
		    remark TEXT NOT NULL DEFAULT '',
		    enabled INTEGER NOT NULL DEFAULT 1,
		    daily_token_limit INTEGER NOT NULL DEFAULT 0,
		    monthly_token_limit INTEGER NOT NULL DEFAULT 0,
		    allowed_models TEXT NOT NULL DEFAULT '',
		    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		    last_used_at DATETIME,
		    is_active INTEGER NOT NULL DEFAULT 1
		)`,
		// 走重建的都是「旧库」（schema 仍带内联 UNIQUE）：列名就是 label，改名
		// （name → label）在重建之后跑，所以这里直接按 label 搬运。remark 由
		// extraCols 在重建前补好，故可直接搬运。
		insertCols:  []string{"id", "key", "label", "remark", "enabled", "daily_token_limit", "monthly_token_limit", "allowed_models", "created_at", "last_used_at", "is_active"},
		selectExprs: []string{"id", "key", "label", "remark", "enabled", "daily_token_limit", "monthly_token_limit", "allowed_models", "created_at", "last_used_at", "is_active"},
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
		    duration_ms INTEGER NOT NULL DEFAULT 0,
		    stream INTEGER NOT NULL DEFAULT 0,
		    status TEXT NOT NULL DEFAULT 'ok',
		    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		insertCols:  []string{"id", "ext_key_id", "upstream_id", "upstream_name", "model", "in_format", "up_format", "prompt_tokens", "completion_tokens", "total_tokens", "cache_read_tokens", "cache_creation_tokens", "reasoning_tokens", "duration_ms", "stream", "status", "created_at"},
		selectExprs: []string{"id", "ext_key_id", "upstream_id", "upstream_name", "model", "in_format", "up_format", "prompt_tokens", "completion_tokens", "total_tokens", "cache_read_tokens", "cache_creation_tokens", "reasoning_tokens", "duration_ms", "stream", "status", "created_at"},
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
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_model_aliases_name ON model_aliases(name) WHERE is_active = 1`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_alias_bindings_order ON model_alias_bindings(alias_id, priority) WHERE is_active = 1`,
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
	if err := migrateSoftDelete(d); err != nil {
		return err
	}
	return migrateRenamedCols(d)
}
