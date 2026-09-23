package db

import (
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/great-magician-01/any-llm/internal/logger"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

type Dialect string

const (
	DialectSQLite   Dialect = "sqlite"
	DialectPostgres Dialect = "postgres"
	DialectMySQL    Dialect = "mysql"
)

// SupportsConversationArchive 报告该方言是否启用对话归档（按月分表）。
// SQLite 不建 conversation_records 表，归档整体关闭。
func (dl Dialect) SupportsConversationArchive() bool {
	return dl == DialectPostgres || dl == DialectMySQL
}

type PGConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	DBName   string
	Schema   string
}

func OpenSQLite(path string) (*sql.DB, error) {
	d, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	if err := migrateAll(d); err != nil {
		d.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	// 去外键/软删除升级放在 foreign_keys=ON 之前：SQLite 重建期间 REFERENCES
	// 改写只受 legacy_alter_table 控制，但约束启用后 DROP/重建会受限。
	if _, err := d.Exec("PRAGMA foreign_keys = ON"); err != nil {
		d.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}
	if _, err := d.Exec("PRAGMA journal_mode = WAL"); err != nil {
		d.Close()
		return nil, fmt.Errorf("enable WAL mode: %w", err)
	}
	if _, err := d.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		d.Close()
		return nil, fmt.Errorf("set busy_timeout: %w", err)
	}
	return d, nil
}

type MySQLConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	DBName   string
}

// OpenMySQL 连接 MySQL 并执行迁移。
//
// DSN 关键参数：
//   - parseTime=true&loc=Local —— DATETIME 无时区，驱动按 loc 读写墙钟，语义与
//     PG 的 pgtime.go 重标 time.Local 完全一致（本项目一律写 time.Now()）。
//   - charset=utf8mb4&collation=utf8mb4_bin —— MySQL 默认的 utf8mb4_0900_ai_ci
//     是大小写/重音不敏感的，会让 name/key 的唯一性与等值比较语义偏离 PG/SQLite
//     的字节精确比较。utf8mb4_bin 是 PAD SPACE（尾部空格会被忽略），这是残留差异。
//   - maxAllowedPacket=0 —— 0 表示连接时读服务器的 @@max_allowed_packet。驱动的
//     客户端默认值恰好是 64 MiB，与归档的 response_raw 上限（convRawCap）相等，
//     也就是合法范围内最大的那条归档必然写失败。
func OpenMySQL(cfg MySQLConfig) (*sql.DB, error) {
	mc := mysql.NewConfig()
	mc.User = cfg.User
	mc.Passwd = cfg.Password
	mc.Net = "tcp"
	mc.Addr = net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	mc.DBName = cfg.DBName
	mc.ParseTime = true
	mc.Loc = time.Local
	mc.Timeout = 10 * time.Second
	mc.MaxAllowedPacket = 0
	// charset/collation 走 DSN 参数与握手字段：驱动只有在 charset 非空时才会发
	// SET NAMES，否则握手里声明的是 utf8mb4_general_ci。
	mc.Params = map[string]string{"charset": "utf8mb4"}
	mc.Collation = "utf8mb4_bin"
	d, err := sql.Open("mysql", mc.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("open mysql %s@%s: %w", cfg.User, mc.Addr, err)
	}
	if err := d.Ping(); err != nil {
		d.Close()
		return nil, fmt.Errorf("ping mysql: %w", err)
	}
	warnIfSmallMaxAllowedPacket(d)
	if err := migrateAll(d); err != nil {
		d.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return d, nil
}

// warnIfSmallMaxAllowedPacket 在服务器 max_allowed_packet 装不下归档上限时告警。
// 写入走异步 writer，失败只记日志，所以这里必须在启动时把话说清楚。
func warnIfSmallMaxAllowedPacket(d *sql.DB) {
	var v int64
	if err := d.QueryRow(`SELECT @@max_allowed_packet`).Scan(&v); err != nil {
		return
	}
	if v < 128<<20 {
		logger.Warn("db: MySQL max_allowed_packet 过小，64 MiB 的对话归档可能写失败；建议 >= 256M",
			"max_allowed_packet", v)
	}
}

// migrateAll 是三种方言共用的迁移管线。OpenSQLite / OpenPG / OpenMySQL 与
// MigrateForTest 都走这里，避免测试管线与生产管线漂移。
//
// 顺序是负载的：主迁移建表 → 补列 → 软删除升级 → 部分唯一索引 → 普通索引 →
// label 唯一索引。部分唯一索引必须晚于 is_active 列就位；SQLite 的表重建必须在
// PRAGMA foreign_keys=ON 之前（OpenSQLite 已保证）。
func migrateAll(d *sql.DB) error {
	if err := migrateMain(d); err != nil {
		return err
	}
	if err := migrateExtraCols(d); err != nil {
		return err
	}
	if err := migrateSoftDelete(d); err != nil {
		return err
	}
	if err := migratePartialUniqueIndexes(d); err != nil {
		return err
	}
	if err := migratePlainIndexes(d); err != nil {
		return err
	}
	ensureExtKeyLabelIndex(d)
	return nil
}

var schemaIdentRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func OpenPG(cfg PGConfig) (*sql.DB, error) {
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
		url.QueryEscape(cfg.User), url.QueryEscape(cfg.Password),
		cfg.Host, cfg.Port, cfg.DBName)
	connConfig, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse postgres dsn: %w", err)
	}
	if cfg.Schema != "" {
		if !schemaIdentRe.MatchString(cfg.Schema) {
			return nil, fmt.Errorf("invalid schema name %q", cfg.Schema)
		}
		connConfig.RuntimeParams["search_path"] = cfg.Schema
	}
	d := stdlib.OpenDB(*connConfig, stdlib.OptionAfterConnect(registerLocalTimestamp))
	if err := d.Ping(); err != nil {
		d.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	if cfg.Schema != "" {
		if _, err := d.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", cfg.Schema)); err != nil {
			d.Close()
			return nil, fmt.Errorf("create schema %s: %w", cfg.Schema, err)
		}
	}
	if err := migrateAll(d); err != nil {
		d.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return d, nil
}

// DialectOf returns the SQL dialect of the given *sql.DB, inferred from its
// underlying driver. Returns DialectSQLite for nil or unknown drivers.
func DialectOf(d *sql.DB) Dialect {
	if d == nil {
		return DialectSQLite
	}
	switch d.Driver().(type) {
	case *stdlib.Driver:
		return DialectPostgres
	case *mysql.MySQLDriver:
		return DialectMySQL
	}
	return DialectSQLite
}

// Rebind rewrites a query's "?" placeholders to the dialect-appropriate form.
// For PostgreSQL it converts to "$N" positional parameters; SQLite and MySQL
// both use "?" natively and get the query back unchanged. String literals
// ('...') and SQL comments (-- line, /* block */) are skipped so that '?'
// inside them is preserved.
func Rebind(d *sql.DB, query string) string {
	if DialectOf(d) != DialectPostgres {
		return query
	}
	return rebindPostgres(query)
}

func rebindPostgres(query string) string {
	var b strings.Builder
	n := 0
	i := 0
	for i < len(query) {
		c := query[i]
		switch {
		case c == '\'':
			b.WriteByte(c)
			i++
			for i < len(query) {
				b.WriteByte(query[i])
				if query[i] == '\'' {
					i++
					if i < len(query) && query[i] == '\'' {
						b.WriteByte(query[i])
						i++
						continue
					}
					break
				}
				i++
			}
		case c == '-' && i+1 < len(query) && query[i+1] == '-':
			b.WriteString("--")
			i += 2
			for i < len(query) && query[i] != '\n' {
				b.WriteByte(query[i])
				i++
			}
		case c == '/' && i+1 < len(query) && query[i+1] == '*':
			b.WriteString("/*")
			i += 2
			for i+1 < len(query) && !(query[i] == '*' && query[i+1] == '/') {
				b.WriteByte(query[i])
				i++
			}
			if i+1 < len(query) {
				b.WriteString("*/")
				i += 2
			} else if i < len(query) {
				b.WriteByte(query[i])
				i++
			}
		case c == '?':
			n++
			fmt.Fprintf(&b, "$%d", n)
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}
