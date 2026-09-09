package model

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/great-magician-01/any-llm/internal/db"
)

// BalanceSnapshot is one archived vendor balance/quota poll result. Payload is
// the normalized JSON produced by upstream.FetchBalance (kind=balance|quota).
type BalanceSnapshot struct {
	ID           int64           `json:"id"`
	UpstreamID   int64           `json:"upstream_id"`
	UpstreamName string          `json:"upstream_name"`
	Vendor       string          `json:"vendor"`
	Payload      json.RawMessage `json:"payload"`
	CreatedAt    time.Time       `json:"created_at"`
}

// InsertBalanceSnapshot archives one snapshot, filling CreatedAt with the
// server-local now when unset (project timestamp convention: Go time.Now(),
// never SQL defaults). On success s.ID is set to the new row id.
func InsertBalanceSnapshot(d *sql.DB, s *BalanceSnapshot) error {
	ts := s.CreatedAt
	if ts.IsZero() {
		ts = time.Now()
	}
	err := d.QueryRow(db.Rebind(d, `INSERT INTO balance_snapshots
		(upstream_id, upstream_name, vendor, payload, created_at)
		VALUES (?,?,?,?,?) RETURNING id`),
		s.UpstreamID, s.UpstreamName, s.Vendor, string(s.Payload), ts).Scan(&s.ID)
	if err != nil {
		return fmt.Errorf("insert balance snapshot: %w", err)
	}
	s.CreatedAt = ts
	return nil
}

// LatestBalanceSnapshots returns the newest snapshot per upstream, for list
// pages and the dashboard.
func LatestBalanceSnapshots(d *sql.DB) ([]BalanceSnapshot, error) {
	rows, err := d.Query(`SELECT id, upstream_id, upstream_name, vendor, payload, created_at
		FROM balance_snapshots
		WHERE id IN (SELECT MAX(id) FROM balance_snapshots GROUP BY upstream_id)
		ORDER BY upstream_id`)
	if err != nil {
		return nil, fmt.Errorf("latest balance snapshots: %w", err)
	}
	defer rows.Close()
	out := make([]BalanceSnapshot, 0)
	for rows.Next() {
		var s BalanceSnapshot
		var payload string
		if err := rows.Scan(&s.ID, &s.UpstreamID, &s.UpstreamName, &s.Vendor, &payload, &s.CreatedAt); err != nil {
			return nil, err
		}
		s.Payload = json.RawMessage(payload)
		out = append(out, s)
	}
	return out, nil
}

// BalanceSnapshotsList pages one upstream's snapshot history, newest first,
// following the UsageRecordsList clamp/COUNT/LIMIT-OFFSET pattern.
func BalanceSnapshotsList(d *sql.DB, upstreamID int64, page, size int) ([]BalanceSnapshot, int, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 50
	}
	var total int
	if err := d.QueryRow(db.Rebind(d, `SELECT COUNT(*) FROM balance_snapshots WHERE upstream_id = ?`), upstreamID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count balance snapshots: %w", err)
	}
	offset := (page - 1) * size
	rows, err := d.Query(db.Rebind(d, `SELECT id, upstream_id, upstream_name, vendor, payload, created_at
		FROM balance_snapshots WHERE upstream_id = ? ORDER BY id DESC LIMIT ? OFFSET ?`), upstreamID, size, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list balance snapshots: %w", err)
	}
	defer rows.Close()
	out := make([]BalanceSnapshot, 0)
	for rows.Next() {
		var s BalanceSnapshot
		var payload string
		if err := rows.Scan(&s.ID, &s.UpstreamID, &s.UpstreamName, &s.Vendor, &payload, &s.CreatedAt); err != nil {
			return nil, 0, err
		}
		s.Payload = json.RawMessage(payload)
		out = append(out, s)
	}
	return out, total, nil
}
