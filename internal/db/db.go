package db

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

func Open(path string) (*sql.DB, error) {
	d, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	if _, err := d.Exec(migrationSQL); err != nil {
		d.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}
	if _, err := d.Exec("PRAGMA foreign_keys = ON"); err != nil {
		d.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}
	return d, nil
}
