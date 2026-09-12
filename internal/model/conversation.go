package model

import (
	"database/sql"
	"fmt"
	"strings"
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
// PG 下按 created_at 月份路由到月分表（缺表时自动建表并注册进缓存，
// 见 conversation_shard.go），SQLite 走单表（仅单测使用）。两个 IR 列用
// ?::jsonb 占位，Rebind 会把 ? 重写为 $N 并保留 ::jsonb 转换。
func InsertConversation(d *sql.DB, r *ConversationRecord) error {
	ts := r.CreatedAt
	if ts.IsZero() {
		ts = time.Now()
	}
	table := convBaseTable
	if db.DialectOf(d) == db.DialectPostgres {
		key := convMonthKey(ts)
		name, ok := convShardForMonth(key)
		if !ok {
			if err := EnsureConversationShard(d, ts); err != nil {
				return fmt.Errorf("ensure conversation shard: %w", err)
			}
			name = ConvShardName(ts)
		}
		table = name
	}
	err := insertConversationInto(d, table, r, ts)
	if err != nil && db.DialectOf(d) == db.DialectPostgres && isUndefinedTable(err) {
		// 缓存与 catalog 不一致（如表被外部 DROP）：重建并重试一次。
		if cerr := EnsureConversationShard(d, ts); cerr != nil {
			return fmt.Errorf("re-ensure conversation shard: %w", cerr)
		}
		err = insertConversationInto(d, ConvShardName(ts), r, ts)
	}
	if err != nil {
		return fmt.Errorf("insert conversation: %w", err)
	}
	return nil
}

// insertConversationInto 执行向指定分表的单行插入。table 必须已过
// convShardNameRe 白名单或为 convBaseTable（本包内部保证），不接受外部输入。
func insertConversationInto(d *sql.DB, table string, r *ConversationRecord, ts time.Time) error {
	stream := 0
	if r.Stream {
		stream = 1
	}
	_, err := d.Exec(db.Rebind(d, `INSERT INTO `+table+`
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
	return err
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
// page/size 规范化与 UsageRecordsList 一致。PG 下跨分表按块翻页
// （见 conversation_shard.go）；SQLite 走单表（仅单测使用）。
func ConversationRecordsList(d *sql.DB, page, size int) ([]ConversationRecord, int, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 50
	}
	if db.DialectOf(d) != db.DialectPostgres {
		total, err := countConversations(d, convBaseTable)
		if err != nil {
			return nil, 0, err
		}
		records, err := listConversationsFrom(d, convBaseTable, size, (page-1)*size)
		return records, total, err
	}

	shards, err := convShardSnapshot(d)
	if err != nil {
		return nil, 0, fmt.Errorf("conversation shards: %w", err)
	}
	if len(shards) == 0 {
		return []ConversationRecord{}, 0, nil
	}
	// 各分表行数：一条 UNION ALL 取回，按表名归位（UNION ALL 不保证输出顺序）。
	countBy, err := countConversationsByShard(d, shards)
	if err != nil {
		return nil, 0, err
	}
	counts := make([]int, len(shards))
	total := 0
	for i, t := range shards {
		counts[i] = countBy[t]
		total += counts[i]
	}
	// 全局页 → 各分表窗口，只查命中的分表，按新→旧拼接。
	out := make([]ConversationRecord, 0, size)
	for _, w := range convPageWindows(counts, (page-1)*size, size) {
		records, err := listConversationsFrom(d, shards[w.shard], w.limit, w.offset)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, records...)
	}
	return out, total, nil
}

// countConversationsByShard 用一条 UNION ALL 查询取各分表行数（key 为表名）。
func countConversationsByShard(d *sql.DB, shards []string) (map[string]int, error) {
	var sb strings.Builder
	for i, t := range shards {
		if i > 0 {
			sb.WriteString(" UNION ALL ")
		}
		fmt.Fprintf(&sb, `SELECT '%s', COUNT(*) FROM %s`, t, t)
	}
	rows, err := d.Query(sb.String())
	if err != nil {
		return nil, fmt.Errorf("count conversations: %w", err)
	}
	defer rows.Close()
	out := make(map[string]int, len(shards))
	for rows.Next() {
		var name string
		var n int
		if err := rows.Scan(&name, &n); err != nil {
			return nil, err
		}
		out[name] = n
	}
	return out, rows.Err()
}

func countConversations(d *sql.DB, table string) (int, error) {
	var total int
	if err := d.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&total); err != nil {
		return 0, fmt.Errorf("count conversations: %w", err)
	}
	return total, nil
}

// listConversationsFrom 查单张分表的一页（新到旧；created_at 秒级精度，
// 同秒用 id 决胜保证翻页稳定）。表名同 insertConversationInto 的安全约定。
func listConversationsFrom(d *sql.DB, table string, limit, offset int) ([]ConversationRecord, error) {
	rows, err := d.Query(db.Rebind(d, `SELECT `+convMetaCols+`
		FROM `+table+` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`), limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list conversations from %s: %w", table, err)
	}
	defer rows.Close()
	out := make([]ConversationRecord, 0)
	for rows.Next() {
		var r ConversationRecord
		if err := scanConversation(rows.Scan, &r, false); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetConversation 取单条归档，含 request_ir/response_ir；raw 字节不查询
// （JSON 输出为 null），避免 base64 大响应。不存在时返回 sql.ErrNoRows。
// PG 下跨全部分表按 id 查（各分支独立 WHERE id=?，均走主键索引）。
func GetConversation(d *sql.DB, id int64) (*ConversationRecord, error) {
	if db.DialectOf(d) == db.DialectPostgres {
		shards, err := convShardSnapshot(d)
		if err != nil {
			return nil, fmt.Errorf("conversation shards: %w", err)
		}
		if len(shards) == 0 {
			return nil, sql.ErrNoRows
		}
		var sb strings.Builder
		args := make([]any, 0, len(shards))
		for i, t := range shards {
			if i > 0 {
				sb.WriteString(" UNION ALL ")
			}
			fmt.Fprintf(&sb, `SELECT %s, request_ir, response_ir FROM %s WHERE id = ?`, convMetaCols, t)
			args = append(args, id)
		}
		return scanOneConversation(d.QueryRow(db.Rebind(d, sb.String()), args...))
	}
	return scanOneConversation(d.QueryRow(db.Rebind(d, `SELECT `+convMetaCols+`, request_ir, response_ir
		FROM conversation_records WHERE id = ?`), id))
}

func scanOneConversation(row *sql.Row) (*ConversationRecord, error) {
	var r ConversationRecord
	if err := scanConversation(row.Scan, &r, true); err != nil {
		return nil, err
	}
	return &r, nil
}
