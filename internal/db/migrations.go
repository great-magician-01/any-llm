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

// extraCols 列出「初始 schema 之后才加入」的列，连同 ALTER TABLE 补列用的完整
// 列定义（类型 + NOT NULL + 默认值）：本项目的老库升级后合理缺失这些列，需要回填；
// 新库由上面的 CREATE TABLE 提供。补列只在列不存在时执行，幂等；coldef 需能直接
// 用于两种方言的 ADD COLUMN。
//
// 初始 schema 就有的列（如 ext_keys.label）明确不在这里兜底：CREATE TABLE IF NOT
// EXISTS 对已存在的同名表是空操作，若那张表不是本项目按当前形态建的，缺列会在查询
// 时以 42703 暴露——此时应人工修库，而不是静默补一个语义不明的空壳列把库搞乱。
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
	// 备注（初始 schema 之后才加入，老库回填）
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
		// 限定当前 schema：同库其他 schema（另一实例/残留测试 schema）里的
		// 同名表不算数，否则它们的列会让本 schema 的补列被误判跳过。
		err := d.QueryRow(`SELECT COUNT(*) FROM information_schema.columns
			WHERE table_name = $1 AND column_name = $2 AND table_schema = current_schema()`, table, col).Scan(&n)
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
		// 注意：按 PG 默认约束名 DROP；若旧库约束是手工建的、命名非默认，
		// 这里 DROP 不到，旧 UNIQUE/FK 会与部分唯一索引并存（软删除后同名
		// 重建会被旧约束拒绝），需要人工处理。
		`ALTER TABLE upstreams DROP CONSTRAINT IF EXISTS upstreams_format_check`,
		`ALTER TABLE upstream_models DROP CONSTRAINT IF EXISTS upstream_models_upstream_id_fkey`,
		`ALTER TABLE usage_records DROP CONSTRAINT IF EXISTS usage_records_ext_key_id_fkey`,
		// conversation_records 迁移不再建表（改应用层按月分表），新库没有这张表：
		// 必须 ALTER TABLE IF EXISTS，否则存量库升级保留的这一步在新库上 42P01。
		`ALTER TABLE IF EXISTS conversation_records DROP CONSTRAINT IF EXISTS conversation_records_ext_key_id_fkey`,
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
	// 无软删除列，仅去 REFERENCES）。cols 为列清单，INSERT 与 SELECT 两侧同名
	// 同序搬运，共用一份以避免两侧清单漂移。
	create string
	cols   []string
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
		cols: []string{"id", "name", "base_url", "api_key", "format", "enabled", "daily_token_limit", "monthly_token_limit", "created_at", "updated_at", "is_active"},
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
		cols: []string{"id", "upstream_id", "model_name", "manual", "context_length", "max_output_length", "is_active"},
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
		// 走重建的都是「旧库」（schema 仍带内联 UNIQUE）。remark 等后期加入的
		// 列由 extraCols 在重建前补齐（OpenSQLite 的调用顺序保证），按名搬运。
		cols: []string{"id", "key", "label", "remark", "enabled", "daily_token_limit", "monthly_token_limit", "allowed_models", "created_at", "last_used_at", "is_active"},
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
		cols: []string{"id", "ext_key_id", "upstream_id", "upstream_name", "model", "in_format", "up_format", "prompt_tokens", "completion_tokens", "total_tokens", "cache_read_tokens", "cache_creation_tokens", "reasoning_tokens", "duration_ms", "stream", "status", "created_at"},
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
		cols := strings.Join(spec.cols, ", ")
		steps := []string{
			fmt.Sprintf(`ALTER TABLE %s RENAME TO %s`, spec.table, bak),
			spec.create,
			fmt.Sprintf(`INSERT INTO %s (%s) SELECT %s FROM %s`, spec.table, cols, cols, bak),
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

// extKeyLabelIndexDDL 是 ext_keys.label 唯一性的 DB 兜底：活跃且非空的名称不可
// 重复，口径与应用层 ExtKeyLabelTaken 一致（空名不参与、软删行不占名额）。
// 不放进 uniqueIndexDDL：历史库可能存在唯一性约束加入前留下的重名活跃 key，
// 建索引会失败，需要容错处理（见 ensureExtKeyLabelIndex）。
const extKeyLabelIndexDDL = `CREATE UNIQUE INDEX IF NOT EXISTS idx_ext_keys_label ON ext_keys(label) WHERE is_active = 1 AND label <> ''`

// ensureExtKeyLabelIndex 在每次启动时尝试建立 label 唯一索引。历史库有重名活跃
// key 时建索引必然失败：不自动改名去重（不动别人的数据），也不阻断启动（单进程
// 下应用层检查仍生效）——打出冲突明细，人工去重后下次启动这里会自动补上。
func ensureExtKeyLabelIndex(d *sql.DB) {
	_, err := d.Exec(extKeyLabelIndexDDL)
	if err == nil {
		return
	}
	var dupes []string
	rows, qerr := d.Query(`SELECT label, COUNT(*) FROM ext_keys WHERE is_active = 1 AND label <> '' GROUP BY label HAVING COUNT(*) > 1`)
	if qerr == nil {
		for rows.Next() {
			var label string
			var n int
			if serr := rows.Scan(&label, &n); serr != nil {
				break
			}
			dupes = append(dupes, fmt.Sprintf("%q×%d", label, n))
		}
		if rows.Err() != nil {
			dupes = nil
		}
		rows.Close()
	}
	logger.Warn("db: 无法创建 ext_keys label 唯一索引（存在重名活跃 key）；应用层检查仍生效，人工去重后重启自动补上",
		"err", err, "duplicates", dupes)
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
