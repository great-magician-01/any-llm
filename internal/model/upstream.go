package model

import (
	"database/sql"
	"fmt"
	"time"
)

func CreateUpstream(db *sql.DB, u *Upstream) (int64, error) {
	res, err := db.Exec(`INSERT INTO upstreams (name, base_url, api_key, format) VALUES (?,?,?,?)`,
		u.Name, u.BaseURL, u.APIKey, u.Format)
	if err != nil {
		return 0, fmt.Errorf("create upstream: %w", err)
	}
	id, _ := res.LastInsertId()
	return id, nil
}

func GetUpstreamByID(db *sql.DB, id int64) (*Upstream, error) {
	u := &Upstream{}
	var createdAt, updatedAt string
	err := db.QueryRow(`SELECT id, name, base_url, api_key, format, created_at, updated_at FROM upstreams WHERE id=?`, id).
		Scan(&u.ID, &u.Name, &u.BaseURL, &u.APIKey, &u.Format, &createdAt, &updatedAt)
	if err != nil {
		return nil, fmt.Errorf("get upstream %d: %w", id, err)
	}
	u.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	u.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updatedAt)
	return u, nil
}

func GetUpstreamByName(db *sql.DB, name string) (*Upstream, error) {
	u := &Upstream{}
	var createdAt, updatedAt string
	err := db.QueryRow(`SELECT id, name, base_url, api_key, format, created_at, updated_at FROM upstreams WHERE name=?`, name).
		Scan(&u.ID, &u.Name, &u.BaseURL, &u.APIKey, &u.Format, &createdAt, &updatedAt)
	if err != nil {
		return nil, fmt.Errorf("get upstream by name %q: %w", name, err)
	}
	u.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	u.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updatedAt)
	return u, nil
}

func ListUpstreams(db *sql.DB) ([]Upstream, error) {
	rows, err := db.Query(`SELECT id, name, base_url, api_key, format, created_at, updated_at FROM upstreams ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list upstreams: %w", err)
	}
	defer rows.Close()
	var out []Upstream
	for rows.Next() {
		var u Upstream
		var createdAt, updatedAt string
		if err := rows.Scan(&u.ID, &u.Name, &u.BaseURL, &u.APIKey, &u.Format, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		u.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
		u.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updatedAt)
		out = append(out, u)
	}
	return out, nil
}

func UpdateUpstream(db *sql.DB, u *Upstream) error {
	_, err := db.Exec(`UPDATE upstreams SET name=?, base_url=?, api_key=?, format=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		u.Name, u.BaseURL, u.APIKey, u.Format, u.ID)
	if err != nil {
		return fmt.Errorf("update upstream %d: %w", u.ID, err)
	}
	return nil
}

func DeleteUpstream(db *sql.DB, id int64) error {
	_, err := db.Exec(`DELETE FROM upstreams WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete upstream %d: %w", id, err)
	}
	return nil
}

func ListModels(db *sql.DB, upstreamID int64) ([]UpstreamModel, error) {
	rows, err := db.Query(`SELECT id, upstream_id, model_name, manual FROM upstream_models WHERE upstream_id=? ORDER BY model_name`, upstreamID)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	defer rows.Close()
	var out []UpstreamModel
	for rows.Next() {
		var m UpstreamModel
		var manual int
		if err := rows.Scan(&m.ID, &m.UpstreamID, &m.ModelName, &manual); err != nil {
			return nil, err
		}
		m.Manual = manual != 0
		out = append(out, m)
	}
	return out, nil
}

func AddModel(db *sql.DB, upstreamID int64, modelName string, manual bool) error {
	m := 0
	if manual {
		m = 1
	}
	_, err := db.Exec(`INSERT OR IGNORE INTO upstream_models (upstream_id, model_name, manual) VALUES (?,?,?)`, upstreamID, modelName, m)
	if err != nil {
		return fmt.Errorf("add model: %w", err)
	}
	return nil
}

func DeleteModel(db *sql.DB, id int64) error {
	_, err := db.Exec(`DELETE FROM upstream_models WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete model: %w", err)
	}
	return nil
}

func ReplaceModels(db *sql.DB, upstreamID int64, names []string) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()
	// delete non-manual models
	if _, err := tx.Exec(`DELETE FROM upstream_models WHERE upstream_id=? AND manual=0`, upstreamID); err != nil {
		return fmt.Errorf("delete non-manual: %w", err)
	}
	for _, n := range names {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO upstream_models (upstream_id, model_name, manual) VALUES (?,?,0)`, upstreamID, n); err != nil {
			return fmt.Errorf("insert model %s: %w", n, err)
		}
	}
	return tx.Commit()
}
