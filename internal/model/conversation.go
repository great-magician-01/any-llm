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
