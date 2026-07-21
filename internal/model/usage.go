package model

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/great-magician-01/any-llm/internal/db"
)

type UsageSummary struct {
	GroupKey         string `json:"group_key"`
	RequestCount     int    `json:"request_count"`
	TotalTokens      int    `json:"total_tokens"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	OkCount          int    `json:"ok_count"`
	ErrorCount       int    `json:"error_count"`
}

// SumTokens returns the total tokens consumed in the half-open time window
// [from, to) for the given ext key or upstream. Pass a non-nil extKeyID to
// aggregate by ext key, or a non-nil upstreamID to aggregate by upstream.
// If both are nil the function returns 0.
func SumTokens(d *sql.DB, extKeyID, upstreamID *int64, from, to time.Time) (int, error) {
	if extKeyID == nil && upstreamID == nil {
		return 0, nil
	}
	q := `SELECT COALESCE(SUM(total_tokens), 0) FROM usage_records WHERE created_at >= ? AND created_at < ?`
	args := []any{from, to}
	if extKeyID != nil {
		q += ` AND ext_key_id = ?`
		args = append(args, *extKeyID)
	} else {
		q += ` AND upstream_id = ?`
		args = append(args, *upstreamID)
	}
	var sum int
	if err := d.QueryRow(db.Rebind(d, q), args...).Scan(&sum); err != nil {
		return 0, fmt.Errorf("sum tokens: %w", err)
	}
	return sum, nil
}

func InsertUsage(d *sql.DB, r *UsageRecord) error {
	stream := 0
	if r.Stream {
		stream = 1
	}
	ts := r.CreatedAt
	if ts.IsZero() {
		ts = time.Now()
	}
	_, err := d.Exec(db.Rebind(d, `INSERT INTO usage_records
		(ext_key_id, upstream_id, upstream_name, model, in_format, up_format,
		 prompt_tokens, completion_tokens, total_tokens, stream, status, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`),
		r.ExtKeyID, r.UpstreamID, r.UpstreamName, r.Model, r.InFormat, r.UpFormat,
		r.PromptTokens, r.CompletionTokens, r.TotalTokens, stream, r.Status, ts)
	if err != nil {
		return fmt.Errorf("insert usage: %w", err)
	}
	return nil
}

func UsageSummaryByGroup(d *sql.DB, groupBy, from, to string) ([]UsageSummary, error) {
	var groupCol string
	switch groupBy {
	case "key":
		groupCol = "COALESCE(CAST(ext_key_id AS TEXT), '0')"
	case "model":
		groupCol = "model"
	case "upstream":
		groupCol = "upstream_name"
	default:
		groupCol = "model"
	}
	q := fmt.Sprintf(`SELECT %s AS gk, COUNT(*), SUM(total_tokens), SUM(prompt_tokens), SUM(completion_tokens),
		SUM(CASE WHEN status='ok' THEN 1 ELSE 0 END), SUM(CASE WHEN status='error' THEN 1 ELSE 0 END)
		FROM usage_records`, groupCol)
	var conditions []string
	var args []any
	if from != "" {
		t, err := parseTimeParam(from)
		if err != nil {
			return nil, fmt.Errorf("usage summary: invalid from: %w", err)
		}
		conditions = append(conditions, "created_at >= ?")
		args = append(args, t)
	}
	if to != "" {
		t, err := parseTimeParam(to)
		if err != nil {
			return nil, fmt.Errorf("usage summary: invalid to: %w", err)
		}
		conditions = append(conditions, "created_at <= ?")
		args = append(args, t)
	}
	if len(conditions) > 0 {
		q += " WHERE " + strings.Join(conditions, " AND ")
	}
	q += fmt.Sprintf(" GROUP BY %s ORDER BY gk", groupCol)
	rows, err := d.Query(db.Rebind(d, q), args...)
	if err != nil {
		return nil, fmt.Errorf("usage summary: %w", err)
	}
	defer rows.Close()
	out := make([]UsageSummary, 0)
	for rows.Next() {
		var s UsageSummary
		if err := rows.Scan(&s.GroupKey, &s.RequestCount, &s.TotalTokens, &s.PromptTokens,
			&s.CompletionTokens, &s.OkCount, &s.ErrorCount); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// parseTimeParam parses a from/to query parameter into a time.Time. It
// accepts RFC3339 (with timezone offset) as well as naive local formats
// (interpreted in the server's local timezone, matching how created_at is
// written by InsertUsage). The result must be passed to queries as a
// time.Time — plain strings compare incorrectly against created_at under
// modernc.org/sqlite (DATETIME column affinity).
func parseTimeParam(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported time format %q", s)
}

func UsageRecordsList(d *sql.DB, page, size int) ([]UsageRecord, int, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 50
	}
	var total int
	if err := d.QueryRow("SELECT COUNT(*) FROM usage_records").Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count usage: %w", err)
	}
	offset := (page - 1) * size
	rows, err := d.Query(db.Rebind(d, `SELECT id, ext_key_id, upstream_id, upstream_name, model, in_format, up_format,
		prompt_tokens, completion_tokens, total_tokens, stream, status, created_at
		FROM usage_records ORDER BY id DESC LIMIT ? OFFSET ?`), size, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list usage: %w", err)
	}
	defer rows.Close()
	out := make([]UsageRecord, 0)
	for rows.Next() {
		var r UsageRecord
		var stream int
		var extKeyID sql.NullInt64
		var upstreamID sql.NullInt64
		if err := rows.Scan(&r.ID, &extKeyID, &upstreamID, &r.UpstreamName, &r.Model,
			&r.InFormat, &r.UpFormat, &r.PromptTokens, &r.CompletionTokens, &r.TotalTokens,
			&stream, &r.Status, &r.CreatedAt); err != nil {
			return nil, 0, err
		}
		if extKeyID.Valid {
			id := extKeyID.Int64
			r.ExtKeyID = &id
		}
		if upstreamID.Valid {
			id := upstreamID.Int64
			r.UpstreamID = &id
		}
		r.Stream = stream != 0
		out = append(out, r)
	}
	return out, total, nil
}
