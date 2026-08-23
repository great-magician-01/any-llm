package db

import (
	"database/sql"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

type Dialect string

const (
	DialectSQLite   Dialect = "sqlite"
	DialectPostgres Dialect = "postgres"
)

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
	if _, err := d.Exec(migrationSQLite); err != nil {
		d.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}
	if err := migrateExtraCols(d); err != nil {
		d.Close()
		return nil, fmt.Errorf("migrate extra cols: %w", err)
	}
	// 去外键/软删除升级放在 foreign_keys=ON 之前：SQLite 重建期间 REFERENCES
	// 改写只受 legacy_alter_table 控制，但约束启用后 DROP/重建会受限。
	if err := migrateSoftDelete(d); err != nil {
		d.Close()
		return nil, fmt.Errorf("migrate soft delete: %w", err)
	}
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
	d := stdlib.OpenDB(*connConfig)
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
	if _, err := d.Exec(migrationPG); err != nil {
		d.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}
	if err := migrateExtraCols(d); err != nil {
		d.Close()
		return nil, fmt.Errorf("migrate extra cols: %w", err)
	}
	if err := migrateSoftDelete(d); err != nil {
		d.Close()
		return nil, fmt.Errorf("migrate soft delete: %w", err)
	}
	return d, nil
}

// DialectOf returns the SQL dialect of the given *sql.DB, inferred from its
// underlying driver. Returns DialectSQLite for nil or unknown drivers.
func DialectOf(d *sql.DB) Dialect {
	if d == nil {
		return DialectSQLite
	}
	if _, ok := d.Driver().(*stdlib.Driver); ok {
		return DialectPostgres
	}
	return DialectSQLite
}

// Rebind rewrites a query's "?" placeholders to the dialect-appropriate form.
// For PostgreSQL it converts to "$N" positional parameters; for SQLite it
// returns the query unchanged. String literals ('...') and SQL comments
// (-- line, /* block */) are skipped so that '?' inside them is preserved.
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
