package model

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/great-magician-01/any-llm/internal/db"
)

func CreateUpstream(d *sql.DB, u *Upstream) (int64, error) {
	var id int64
	now := time.Now()
	err := d.QueryRow(db.Rebind(d, `INSERT INTO upstreams (name, base_url, api_key, format, daily_token_limit, monthly_token_limit, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?) RETURNING id`),
		u.Name, u.BaseURL, u.APIKey, u.Format, u.DailyTokenLimit, u.MonthlyTokenLimit, now, now).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("create upstream: %w", err)
	}
	return id, nil
}

func GetUpstreamByID(d *sql.DB, id int64) (*Upstream, error) {
	u := &Upstream{}
	err := d.QueryRow(db.Rebind(d, `SELECT id, name, base_url, api_key, format, daily_token_limit, monthly_token_limit, created_at, updated_at FROM upstreams WHERE id=?`), id).
		Scan(&u.ID, &u.Name, &u.BaseURL, &u.APIKey, &u.Format, &u.DailyTokenLimit, &u.MonthlyTokenLimit, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get upstream %d: %w", id, err)
	}
	return u, nil
}

func GetUpstreamByName(d *sql.DB, name string) (*Upstream, error) {
	u := &Upstream{}
	err := d.QueryRow(db.Rebind(d, `SELECT id, name, base_url, api_key, format, daily_token_limit, monthly_token_limit, created_at, updated_at FROM upstreams WHERE name=?`), name).
		Scan(&u.ID, &u.Name, &u.BaseURL, &u.APIKey, &u.Format, &u.DailyTokenLimit, &u.MonthlyTokenLimit, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get upstream by name %q: %w", name, err)
	}
	return u, nil
}

func ListUpstreams(d *sql.DB) ([]Upstream, error) {
	rows, err := d.Query(`SELECT u.id, u.name, u.base_url, u.api_key, u.format, u.daily_token_limit, u.monthly_token_limit, u.created_at, u.updated_at,
		(SELECT COUNT(*) FROM upstream_models WHERE upstream_id = u.id) AS model_count
		FROM upstreams u ORDER BY u.id`)
	if err != nil {
		return nil, fmt.Errorf("list upstreams: %w", err)
	}
	defer rows.Close()
	out := make([]Upstream, 0)
	for rows.Next() {
		var u Upstream
		if err := rows.Scan(&u.ID, &u.Name, &u.BaseURL, &u.APIKey, &u.Format, &u.DailyTokenLimit, &u.MonthlyTokenLimit, &u.CreatedAt, &u.UpdatedAt, &u.ModelCount); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, nil
}

func UpdateUpstream(d *sql.DB, u *Upstream) error {
	_, err := d.Exec(db.Rebind(d, `UPDATE upstreams SET name=?, base_url=?, api_key=?, format=?, daily_token_limit=?, monthly_token_limit=?, updated_at=? WHERE id=?`),
		u.Name, u.BaseURL, u.APIKey, u.Format, u.DailyTokenLimit, u.MonthlyTokenLimit, time.Now(), u.ID)
	if err != nil {
		return fmt.Errorf("update upstream %d: %w", u.ID, err)
	}
	return nil
}

func DeleteUpstream(d *sql.DB, id int64) error {
	_, err := d.Exec(db.Rebind(d, `DELETE FROM upstreams WHERE id=?`), id)
	if err != nil {
		return fmt.Errorf("delete upstream %d: %w", id, err)
	}
	return nil
}

func ListModels(d *sql.DB, upstreamID int64) ([]UpstreamModel, error) {
	rows, err := d.Query(db.Rebind(d, `SELECT id, upstream_id, model_name, manual FROM upstream_models WHERE upstream_id=? ORDER BY model_name`), upstreamID)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	defer rows.Close()
	out := make([]UpstreamModel, 0)
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

func AddModel(d *sql.DB, upstreamID int64, modelName string, manual bool) error {
	m := 0
	if manual {
		m = 1
	}
	_, err := d.Exec(db.Rebind(d, `INSERT INTO upstream_models (upstream_id, model_name, manual) VALUES (?,?,?) ON CONFLICT (upstream_id, model_name) DO NOTHING`),
		upstreamID, modelName, m)
	if err != nil {
		return fmt.Errorf("add model: %w", err)
	}
	return nil
}

func DeleteModel(d *sql.DB, id int64) error {
	_, err := d.Exec(db.Rebind(d, `DELETE FROM upstream_models WHERE id=?`), id)
	if err != nil {
		return fmt.Errorf("delete model: %w", err)
	}
	return nil
}

func ReplaceModels(d *sql.DB, upstreamID int64, names []string) error {
	tx, err := d.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(db.Rebind(d, `DELETE FROM upstream_models WHERE upstream_id=? AND manual=0`), upstreamID); err != nil {
		return fmt.Errorf("delete non-manual: %w", err)
	}
	for _, n := range names {
		if _, err := tx.Exec(db.Rebind(d, `INSERT INTO upstream_models (upstream_id, model_name, manual) VALUES (?,?,0) ON CONFLICT (upstream_id, model_name) DO NOTHING`), upstreamID, n); err != nil {
			return fmt.Errorf("insert model %s: %w", n, err)
		}
	}
	return tx.Commit()
}
