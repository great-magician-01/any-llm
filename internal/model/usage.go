package model

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type UsageSummary struct {
	GroupKey         string
	RequestCount     int
	TotalTokens      int
	PromptTokens     int
	CompletionTokens int
	OkCount          int
	ErrorCount       int
}

func InsertUsage(db *sql.DB, r *UsageRecord) error {
	stream := 0
	if r.Stream {
		stream = 1
	}
	_, err := db.Exec(`INSERT INTO usage_records
		(ext_key_id, upstream_id, upstream_name, model, in_format, up_format,
		 prompt_tokens, completion_tokens, total_tokens, stream, status)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		r.ExtKeyID, r.UpstreamID, r.UpstreamName, r.Model, r.InFormat, r.UpFormat,
		r.PromptTokens, r.CompletionTokens, r.TotalTokens, stream, r.Status)
	if err != nil {
		return fmt.Errorf("insert usage: %w", err)
	}
	return nil
}

func UsageSummaryByGroup(db *sql.DB, groupBy, from, to string) ([]UsageSummary, error) {
	var groupCol string
	switch groupBy {
	case "key":
		groupCol = "COALESCE(ext_key_id, 0)"
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
		conditions = append(conditions, "created_at >= ?")
		args = append(args, from)
	}
	if to != "" {
		conditions = append(conditions, "created_at <= ?")
		args = append(args, to)
	}
	if len(conditions) > 0 {
		q += " WHERE " + strings.Join(conditions, " AND ")
	}
	q += fmt.Sprintf(" GROUP BY %s ORDER BY gk", groupCol)
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("usage summary: %w", err)
	}
	defer rows.Close()
	var out []UsageSummary
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

func UsageRecordsList(db *sql.DB, page, size int) ([]UsageRecord, int, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 50
	}
	var total int
	if err := db.QueryRow("SELECT COUNT(*) FROM usage_records").Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count usage: %w", err)
	}
	offset := (page - 1) * size
	rows, err := db.Query(`SELECT id, ext_key_id, upstream_id, upstream_name, model, in_format, up_format,
		prompt_tokens, completion_tokens, total_tokens, stream, status, created_at
		FROM usage_records ORDER BY id DESC LIMIT ? OFFSET ?`, size, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list usage: %w", err)
	}
	defer rows.Close()
	var out []UsageRecord
	for rows.Next() {
		var r UsageRecord
		var stream int
		var createdAt string
		var extKeyID sql.NullInt64
		var upstreamID sql.NullInt64
		if err := rows.Scan(&r.ID, &extKeyID, &upstreamID, &r.UpstreamName, &r.Model,
			&r.InFormat, &r.UpFormat, &r.PromptTokens, &r.CompletionTokens, &r.TotalTokens,
			&stream, &r.Status, &createdAt); err != nil {
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
		r.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
		out = append(out, r)
	}
	return out, total, nil
}
