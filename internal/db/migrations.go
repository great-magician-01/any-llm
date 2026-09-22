package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/great-magician-01/any-llm/internal/logger"
)

// migrateExtraCols ensures columns added after the initial schema exist on
// older databases. It is idempotent: columns present are skipped. Must be
// called after the main migration script has run so the tables exist.
//
// 哪些列算「初始 schema 之后才加入」由 Column.LateAdd 标记（schema.go），
// 补列 DDL 也从同一份定义渲染——加列只改一处定义，没有第二份清单可以漂移。
func migrateExtraCols(d *sql.DB) error {
	dialect := DialectOf(d)
	for _, tbl := range tablesFor(dialect) {
		for _, col := range tbl.Cols {
			if !col.LateAdd {
				continue
			}
			exists, err := columnExists(d, dialect, tbl.Name, col.Name)
			if err != nil {
				return fmt.Errorf("check column %s.%s: %w", tbl.Name, col.Name, err)
			}
			if exists {
				continue
			}
			stmt, err := tbl.AddColumnDDL(dialect, col, DDLConfig{})
			if err != nil {
				return fmt.Errorf("add column %s.%s: %w", tbl.Name, col.Name, err)
			}
			if _, err := d.Exec(stmt); err != nil {
				return fmt.Errorf("add column %s.%s: %w", tbl.Name, col.Name, err)
			}
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
	case DialectMySQL:
		var n int
		// MySQL 的 database 就是 schema；DATABASE() 返回连接当前默认库。
		err := d.QueryRow(`SELECT COUNT(*) FROM information_schema.columns
			WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?`, table, col).Scan(&n)
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
//     同时顺带移除旧库 upstreams 上的 format CHECK；
//   - MySQL：no-op。MySQL 支持是后来加的，不存在需要升级的存量 MySQL 库——
//     按本项目的原则（不为不可能存在的库做防御性兜底），这里直接跳过，
//     而不是留一段永远不会执行的迁移代码。
//
// 必须在 PRAGMA foreign_keys=ON 之前调用（OpenSQLite 已保证）。SQLite
// 3.25+ 的 ALTER TABLE RENAME 默认会改写其他表的 REFERENCES 子句（仅当
// PRAGMA legacy_alter_table=ON 时才不改写），重建期间需临时打开
// legacy_alter_table，否则未重建表的 REFERENCES 会被改成指向 _bak 表。
func migrateSoftDelete(d *sql.DB) error {
	switch DialectOf(d) {
	case DialectPostgres:
		return migrateSoftDeletePG(d)
	case DialectMySQL:
		return nil
	default:
		return migrateSoftDeleteSQLite(d)
	}
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
	return nil
}

// sqliteTableSpec 描述一次 SQLite 表重建：重建判定与新旧 DDL。
type sqliteTableSpec struct {
	table     string // 表名
	rebuildIf func(sqlText string) bool
}

// sqliteSoftDeleteSpecs 列出需要重建的表。重建 DDL 与列清单都由 schema.go 的
// 规范定义渲染（见 migrateSoftDeleteSQLite），历史上那份手抄的 CREATE TABLE 与
// 「两处必须同步」的警告已经删除。
var sqliteSoftDeleteSpecs = []sqliteTableSpec{
	{
		table: "upstreams",
		rebuildIf: func(s string) bool {
			return strings.Contains(s, "CHECK(") || strings.Contains(s, "UNIQUE")
		},
	},
	{
		table: "upstream_models",
		rebuildIf: func(s string) bool {
			return strings.Contains(s, "REFERENCES") || strings.Contains(s, "UNIQUE")
		},
	},
	{
		table: "ext_keys",
		rebuildIf: func(s string) bool {
			return strings.Contains(s, "UNIQUE")
		},
	},
	{
		table: "usage_records",
		rebuildIf: func(s string) bool {
			return strings.Contains(s, "REFERENCES")
		},
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
		tbl, ok := schemaTableByName(spec.table)
		if !ok {
			return fmt.Errorf("rebuild %s: not in schema", spec.table)
		}
		// 重建前先核对老表是否具备规范定义里的全部列。缺列时按名搬运会静默丢掉
		// 它（要等下次重启才由 extraCols 补回来），所以这里响亮失败并列出缺失列。
		var missing []string
		for _, c := range tbl.Cols {
			var n int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, spec.table, c.Name).Scan(&n); err != nil {
				return fmt.Errorf("check %s columns: %w", spec.table, err)
			}
			if n == 0 {
				missing = append(missing, c.Name)
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf("rebuild %s: legacy table is missing columns %s; "+
				"this database was not created by this project — fix it by hand", spec.table, strings.Join(missing, ", "))
		}
		create, err := tbl.CreateTableDDL(DialectSQLite, DDLConfig{})
		if err != nil {
			return fmt.Errorf("rebuild %s: %w", spec.table, err)
		}
		bak := spec.table + "_bak"
		cols := strings.Join(tbl.ColumnNames(), ", ")
		steps := []string{
			fmt.Sprintf(`ALTER TABLE %s RENAME TO %s`, spec.table, bak),
			create,
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
	// 部分唯一索引必须在 is_active 列就位后创建，因此不放在主迁移里。
	if err := migratePartialUniqueIndexes(d); err != nil {
		return err
	}
	return migratePlainIndexes(d)
}

// migratePartialUniqueIndexes 建「仅活跃行」的部分唯一索引（PG/SQLite 原生支持）。
// 必须在 is_active 列就位后调用；MySQL 的等价约束已内联在 CREATE TABLE，是 no-op。
func migratePartialUniqueIndexes(d *sql.DB) error {
	if DialectOf(d) == DialectMySQL {
		return nil
	}
	dialect := DialectOf(d)
	cfg := DDLConfig{IfNotExists: true}
	for _, t := range tablesFor(dialect) {
		for _, ix := range t.Idx {
			if !ix.Unique || ix.Tolerant {
				continue
			}
			stmt, err := t.indexDef(dialect, ix, cfg)
			if err != nil {
				return fmt.Errorf("create index %s: %w", ix.Name, err)
			}
			if _, err := d.Exec(stmt); err != nil {
				return fmt.Errorf("create index %s: %w", ix.Name, err)
			}
		}
	}
	return nil
}

// migratePlainIndexes 建所有表声明的普通（非唯一）索引：usage_records 的三个、
// response_sessions 与 balance_snapshots 各一个。与部分唯一索引分开：它们不依赖
// is_active，且 SQLite 表重建后同样要重建。MySQL 的索引内联在 CREATE TABLE，
// 对它是 no-op。
func migratePlainIndexes(d *sql.DB) error {
	if DialectOf(d) == DialectMySQL {
		return nil
	}
	dialect := DialectOf(d)
	cfg := DDLConfig{IfNotExists: true}
	for _, tbl := range tablesFor(dialect) {
		for _, ix := range tbl.Idx {
			if ix.Unique {
				continue
			}
			stmt, err := tbl.indexDef(dialect, ix, cfg)
			if err != nil {
				return fmt.Errorf("create index %s: %w", ix.Name, err)
			}
			if _, err := d.Exec(stmt); err != nil {
				return fmt.Errorf("create index %s: %w", ix.Name, err)
			}
		}
	}
	return nil
}

// extKeyLabelIndexDDL 从 schema.go 的规范定义渲染 label 唯一索引（PG/SQLite）。
// 它是 ext_keys.label 唯一性的 DB 兜底，口径与应用层 ExtKeyLabelTaken 一致（空名
// 不参与、软删行不占名额）。它在 schema 里标了 Tolerant：历史库可能存在唯一性
// 约束加入前留下的重名活跃 key，建索引会失败，由 ensureExtKeyLabelIndex 单独
// 容错执行（打出重名明细、不阻断启动），而不是随 migratePartialUniqueIndexes 建。
// DDL 必须渲染而非手写：MySQL 上的等价约束（生成列 + 唯一键）就从同一份声明
// 渲染，手写一份 PG/SQLite 版会让两侧悄悄分叉。
func extKeyLabelIndexDDL(d Dialect) (string, error) {
	tbl, ok := schemaTableByName("ext_keys")
	if !ok {
		return "", fmt.Errorf("ext_keys not in schema")
	}
	for _, ix := range tbl.Idx {
		if ix.Name == "idx_ext_keys_label" {
			return tbl.indexDef(d, ix, DDLConfig{IfNotExists: true})
		}
	}
	return "", fmt.Errorf("idx_ext_keys_label not declared in schema")
}

// ensureExtKeyLabelIndex 在每次启动时尝试建立 label 唯一索引。历史库有重名活跃
// key 时建索引必然失败：不自动改名去重（不动别人的数据），也不阻断启动（单进程
// 下应用层检查仍生效）——打出冲突明细，人工去重后下次启动这里会自动补上。
func ensureExtKeyLabelIndex(d *sql.DB) {
	if DialectOf(d) == DialectMySQL {
		// MySQL 的等价约束是生成列唯一键，已内联在 CREATE TABLE 里；这里只做存在性
		// 检查，缺失时告警（被人为 DROP 掉时 PG/SQLite 会自愈，MySQL 不会）。
		var n int
		if err := d.QueryRow(`SELECT COUNT(*) FROM information_schema.statistics
			WHERE table_schema = DATABASE() AND table_name = 'ext_keys' AND index_name = 'idx_ext_keys_label'`).Scan(&n); err == nil && n == 0 {
			logger.Warn("db: ext_keys label 唯一键缺失（MySQL）；应用层检查仍生效，需人工补建")
		}
		return
	}
	stmt, err := extKeyLabelIndexDDL(DialectOf(d))
	if err != nil {
		logger.Warn("db: 渲染 ext_keys label 唯一索引 DDL 失败；应用层检查仍生效", "err", err)
		return
	}
	_, err = d.Exec(stmt)
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

// MigrateForTest 对已连接的库执行与 Open* 相同的完整迁移管线。导出供其他包
// （如 gateway）的 e2e 测试在独立 schema 里建表；生产代码用 OpenSQLite /
// OpenPG / OpenMySQL，不经过此函数。
func MigrateForTest(d *sql.DB) error {
	return migrateAll(d)
}
