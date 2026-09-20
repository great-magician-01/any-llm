package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
)

// 方言相关的小助手。原则：调用方照常用 ? 占位写 SQL，方言差异集中在这里收口，
// 不在业务代码里散 switch。

// InsertReturningID 执行一条 INSERT 并取回自增 id。
//
// PG/SQLite 走 RETURNING；MySQL 没有 RETURNING，改用 Exec + LastInsertId。
// query 一律按「带 RETURNING id 结尾」的形态书写，本函数负责去掉后缀。
func InsertReturningID(d *sql.DB, query string, args ...any) (int64, error) {
	if DialectOf(d) != DialectMySQL {
		var id int64
		err := d.QueryRow(Rebind(d, query), args...).Scan(&id)
		return id, err
	}
	q := strings.TrimSpace(query)
	if !strings.HasSuffix(q, "RETURNING id") {
		return 0, fmt.Errorf("InsertReturningID: query must end in \"RETURNING id\", got %q", q)
	}
	q = strings.TrimSpace(strings.TrimSuffix(q, "RETURNING id"))
	res, err := d.Exec(q, args...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// InsertReturningIDTx 是 InsertReturningID 的事务版本。MySQL 分支同样走
// LastInsertId（驱动从 OK 包里取，任意语句都可用）。
func InsertReturningIDTx(tx *sql.Tx, d *sql.DB, query string, args ...any) (int64, error) {
	if DialectOf(d) != DialectMySQL {
		var id int64
		err := tx.QueryRow(Rebind(d, query), args...).Scan(&id)
		return id, err
	}
	q := strings.TrimSpace(query)
	if !strings.HasSuffix(q, "RETURNING id") {
		return 0, fmt.Errorf("InsertReturningIDTx: query must end in \"RETURNING id\", got %q", q)
	}
	q = strings.TrimSpace(strings.TrimSuffix(q, "RETURNING id"))
	res, err := tx.Exec(q, args...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ConflictIgnoreSuffix 返回「插入时若与活跃行冲突则忽略」的方言后缀。
//
// cols 是冲突判定涉及的列。PG/SQLite 用部分唯一索引做冲突目标；MySQL 没有部分
// 索引（等价约束是生成列唯一键），用 ON DUPLICATE KEY UPDATE 做空操作。
func ConflictIgnoreSuffix(d *sql.DB, cols string) string {
	if DialectOf(d) == DialectMySQL {
		// id = id 是无操作赋值：命中唯一键时什么都不改，等效 DO NOTHING。
		return " ON DUPLICATE KEY UPDATE id = id"
	}
	return " ON CONFLICT (" + cols + ") WHERE is_active = 1 DO NOTHING"
}

// UpsertSuffix 返回「冲突时用新值覆盖指定列」的方言后缀。
//
// conflictCols 是冲突判定列，copyCols 是要用新值覆盖的列。PG/SQLite 引用
// excluded.<col>；MySQL 用 VALUES(<col>)（自 8.0.20 起废弃但仍可用，行别名写法
// 要求 8.0.19+，为兼容 8.0 全系选前者）。
func UpsertSuffix(d *sql.DB, conflictCols string, copyCols ...string) string {
	if DialectOf(d) == DialectMySQL {
		sets := make([]string, len(copyCols))
		for i, c := range copyCols {
			sets[i] = c + " = VALUES(" + c + ")"
		}
		return " ON DUPLICATE KEY UPDATE " + strings.Join(sets, ", ")
	}
	sets := make([]string, len(copyCols))
	for i, c := range copyCols {
		sets[i] = c + " = excluded." + c
	}
	return " ON CONFLICT (" + conflictCols + ") DO UPDATE SET " + strings.Join(sets, ", ")
}

// JSONPlaceholder 返回把该列的值按 JSON 绑定的占位符。PG 的 jsonb 列需要显式
// 转换，否则驱动发来的 text 参数会被拒；SQLite/MySQL 直接绑字符串即可。
//
// 顺带修掉一个潜在 bug：以前这里对所有方言都写死 ?::jsonb，而 SQLite 根本不支持
// :: 转换语法 —— 那条语句以前只在 PG 上合法过。
func JSONPlaceholder(d *sql.DB) string {
	if DialectOf(d) == DialectPostgres {
		return "?::jsonb"
	}
	return "?"
}

// DayBucketExpr 返回把时间列截到本地日的表达式，产出 "YYYY-MM-DD" 文本。
func DayBucketExpr(d *sql.DB, col string) string {
	switch DialectOf(d) {
	case DialectPostgres:
		return "to_char(date_trunc('day', " + col + "), 'YYYY-MM-DD')"
	case DialectMySQL:
		return "DATE_FORMAT(" + col + ", '%Y-%m-%d')"
	default:
		// modernc.org/sqlite 把 time.Time 存成 "YYYY-MM-DD HH:MM:SS.nnn +ZZZZ CST"，
		// SQLite 的日期函数解析不了，取前 10 个字符就是本地日。
		return "substr(" + col + ", 1, 10)"
	}
}

// ConcatExpr 返回把多个字符串表达式按方言拼接连通的写法。MySQL 里 || 是逻辑或，
// 必须用 CONCAT。
func ConcatExpr(d *sql.DB, exprs ...string) string {
	if DialectOf(d) == DialectMySQL {
		return "CONCAT(" + strings.Join(exprs, ", ") + ")"
	}
	return strings.Join(exprs, " || ")
}

// CastTextExpr 把表达式转成文本。MySQL 的 CAST 没有 TEXT 目标类型，用 CHAR。
func CastTextExpr(d *sql.DB, expr string) string {
	if DialectOf(d) == DialectMySQL {
		return "CAST(" + expr + " AS CHAR)"
	}
	return "CAST(" + expr + " AS TEXT)"
}

// QuoteIdent 按方言引用标识符。只有 ext_keys.key 是 MySQL 保留字，实际需要它；
// 一并提供以备后用。
func QuoteIdent(d *sql.DB, name string) string {
	if DialectOf(d) == DialectMySQL {
		return "`" + name + "`"
	}
	return name
}

// IsUndefinedTable 判断「表不存在」错误：PG 是 SQLSTATE 42P01，MySQL 是 1146。
func IsUndefinedTable(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "42P01"
	}
	var myErr *mysql.MySQLError
	if errors.As(err, &myErr) {
		return myErr.Number == 1146 // ER_NO_SUCH_TABLE
	}
	return false
}

// ListTablesLike 列出当前库/schema 里名字以 prefix 开头的表。用于对话归档的分表
// 发现；捞回来后由调用方用白名单正则复校验。
func ListTablesLike(d *sql.DB, prefix string) ([]string, error) {
	var q string
	switch DialectOf(d) {
	case DialectPostgres:
		q = `SELECT tablename FROM pg_tables WHERE schemaname = current_schema() AND tablename LIKE $1`
	case DialectMySQL:
		q = `SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME LIKE ?`
	default:
		q = `SELECT name FROM sqlite_master WHERE type = 'table' AND name LIKE ?`
	}
	rows, err := d.Query(q, prefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// NextShardID 返回下一个对话归档分表共享 id。
//
// PG 用 CREATE SEQUENCE 建的共享序列（由 convShardDDL 的 DEFAULT nextval 自动
// 取用，调用方不显式传 id）；MySQL 没有序列，用 id_sequences 计数器表模拟：
// 先 INSERT IGNORE 保证行存在，再 UPDATE ... LAST_INSERT_ID(next_id + 1)，
// 从结果里读回新 id。两句话不是原子的，中途崩溃只会烧掉一个 id —— id 只要求唯一，
// 不要求连续，可接受。
func NextShardID(d *sql.DB, seqName string) (int64, error) {
	if DialectOf(d) != DialectMySQL {
		return 0, fmt.Errorf("NextShardID: %s uses a database sequence, not a counter table", DialectOf(d))
	}
	if _, err := d.Exec(`INSERT IGNORE INTO id_sequences (name, next_id) VALUES (?, 0)`, seqName); err != nil {
		return 0, fmt.Errorf("ensure sequence row: %w", err)
	}
	res, err := d.Exec(`UPDATE id_sequences SET next_id = LAST_INSERT_ID(next_id + 1) WHERE name = ?`, seqName)
	if err != nil {
		return 0, fmt.Errorf("next shard id: %w", err)
	}
	// 驱动从 UPDATE 的 OK 包里取 LAST_INSERT_ID(expr) 的值，与连接无关、无需再查。
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read shard id: %w", err)
	}
	return id, nil
}
