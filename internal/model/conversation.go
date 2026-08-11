package model

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/great-magician-01/any-llm/internal/db"
)

// ConversationRecord 归档一次网关对话（仅 PostgreSQL 落库）。
// RequestIR/ResponseIR 是归一化 IR 的 JSON 文本（含工具调用与思维链）；
// RequestRaw/ResponseRaw 是入站请求体与发给客户端的原始字节。
type ConversationRecord struct {
	ID                  int64     `json:"id"`
	ExtKeyID            *int64    `json:"ext_key_id"`
	UpstreamID          *int64    `json:"upstream_id"`
	UpstreamName        string    `json:"upstream_name"`
	Model               string    `json:"model"`
	InFormat            string    `json:"in_format"`
	UpFormat            string    `json:"up_format"`
	Harness             string    `json:"harness"`
	UserAgent           string    `json:"user_agent"`
	Stream              bool      `json:"stream"`
	Status              string    `json:"status"`
	PromptTokens        int       `json:"prompt_tokens"`
	CompletionTokens    int       `json:"completion_tokens"`
	TotalTokens         int       `json:"total_tokens"`
	CacheReadTokens     int       `json:"cache_read_tokens"`
	CacheCreationTokens int       `json:"cache_creation_tokens"`
	ReasoningTokens     int       `json:"reasoning_tokens"`
	RequestIR           string    `json:"request_ir"`
	ResponseIR          string    `json:"response_ir"`
	RequestRaw          []byte    `json:"request_raw"`
	ResponseRaw         []byte    `json:"response_raw"`
	CreatedAt           time.Time `json:"created_at"`
}

// InsertConversation 写入一条对话归档。只在 PG 上被调用（网关层门控）；
// 表由 migrationPG 创建，SQLite 没有此表。两个 IR 列用 ?::jsonb 占位，
// Rebind 会把 ? 重写为 $N 并保留 ::jsonb 转换。
func InsertConversation(d *sql.DB, r *ConversationRecord) error {
	stream := 0
	if r.Stream {
		stream = 1
	}
	ts := r.CreatedAt
	if ts.IsZero() {
		ts = time.Now()
	}
	_, err := d.Exec(db.Rebind(d, `INSERT INTO conversation_records
		(ext_key_id, upstream_id, upstream_name, model, in_format, up_format,
		 harness, user_agent, stream, status,
		 prompt_tokens, completion_tokens, total_tokens,
		 cache_read_tokens, cache_creation_tokens, reasoning_tokens,
		 request_ir, response_ir, request_raw, response_raw, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?::jsonb,?::jsonb,?,?,?)`),
		r.ExtKeyID, r.UpstreamID, r.UpstreamName, r.Model, r.InFormat, r.UpFormat,
		r.Harness, r.UserAgent, stream, r.Status,
		r.PromptTokens, r.CompletionTokens, r.TotalTokens,
		r.CacheReadTokens, r.CacheCreationTokens, r.ReasoningTokens,
		r.RequestIR, r.ResponseIR, r.RequestRaw, r.ResponseRaw, ts)
	if err != nil {
		return fmt.Errorf("insert conversation: %w", err)
	}
	return nil
}

// convMetaCols 是列表/详情查询共用的元数据列，不含 request_ir/response_ir/
// request_raw/response_raw 四个 payload 列（列表页不需要，raw 字节也不出 API）。
const convMetaCols = `id, ext_key_id, upstream_id, upstream_name, model, in_format, up_format,
	harness, user_agent, stream, status,
	prompt_tokens, completion_tokens, total_tokens,
	cache_read_tokens, cache_creation_tokens, reasoning_tokens, created_at`

// scanConversation 按 convMetaCols（withIR 时追加 request_ir, response_ir）的
// 列顺序扫描一行。两种方言都适用：PG 的 JSONB 以 []byte 返回，可赋给 string。
func scanConversation(scan func(dest ...any) error, r *ConversationRecord, withIR bool) error {
	var extKeyID, upstreamID sql.NullInt64
	var stream int
	dest := []any{&r.ID, &extKeyID, &upstreamID, &r.UpstreamName, &r.Model,
		&r.InFormat, &r.UpFormat, &r.Harness, &r.UserAgent, &stream, &r.Status,
		&r.PromptTokens, &r.CompletionTokens, &r.TotalTokens,
		&r.CacheReadTokens, &r.CacheCreationTokens, &r.ReasoningTokens, &r.CreatedAt}
	if withIR {
		dest = append(dest, &r.RequestIR, &r.ResponseIR)
	}
	if err := scan(dest...); err != nil {
		return err
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
	return nil
}

// ConversationRecordsList 分页列出对话归档（新到旧），只含元数据列。
// page/size 规范化与 UsageRecordsList 一致。
func ConversationRecordsList(d *sql.DB, page, size int) ([]ConversationRecord, int, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 50
	}
	var total int
	if err := d.QueryRow("SELECT COUNT(*) FROM conversation_records").Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count conversations: %w", err)
	}
	offset := (page - 1) * size
	rows, err := d.Query(db.Rebind(d, `SELECT `+convMetaCols+`
		FROM conversation_records ORDER BY id DESC LIMIT ? OFFSET ?`), size, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list conversations: %w", err)
	}
	defer rows.Close()
	out := make([]ConversationRecord, 0)
	for rows.Next() {
		var r ConversationRecord
		if err := scanConversation(rows.Scan, &r, false); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, nil
}

// GetConversation 取单条归档，含 request_ir/response_ir；raw 字节不查询
// （JSON 输出为 null），避免 base64 大响应。不存在时返回 sql.ErrNoRows。
func GetConversation(d *sql.DB, id int64) (*ConversationRecord, error) {
	var r ConversationRecord
	row := d.QueryRow(db.Rebind(d, `SELECT `+convMetaCols+`, request_ir, response_ir
		FROM conversation_records WHERE id = ?`), id)
	if err := scanConversation(row.Scan, &r, true); err != nil {
		return nil, err
	}
	return &r, nil
}
