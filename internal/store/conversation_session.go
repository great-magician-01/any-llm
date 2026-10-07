package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/great-magician-01/any-llm/internal/db"
	"github.com/great-magician-01/any-llm/internal/logger"
)

// 会话聚合表（conversation_sessions 月分表）：每行一个会话，随每轮请求
// upsert（无则新增、有则修改）。它是按轮归档（conversation_records）之上的
// 浏览层副本——按轮记录仍是审计/回放的事实来源，本表的任何失败只记日志，
// 不影响主请求与按轮归档。
//
// 设计见 docs/superpowers/specs/2026-10-07-conversation-sessions.md。

// sessionMessagesCap 是 messages 累积列的字节上限。超过后停止追加内容，
// 只继续累计数，避免个别超长会话把单行（及 writer 队列内存）撑爆。
const sessionMessagesCap = 16 << 20 // 16 MiB

// compactionMarker 是历史压缩边界标记：触发压缩检测（显式头或历史变短）时
// 先把它并入 messages，再并入当轮全量，浏览时能看出上下文在哪里被换过。
// 字段名与 translate.Message 的 JSON 序列化一致（大写，无 tag）。
var compactionMarker = json.RawMessage(
	`{"Role":"_marker","Content":[{"Type":"text","Text":"--- 历史已压缩，以上为压缩后的新上下文 ---"}]}`)

// ConversationSession 是会话聚合表的一行。
type ConversationSession struct {
	ID                  int64     `json:"id"`
	SessionID           string    `json:"session_id"`
	LastResponseID      *string   `json:"last_response_id"`
	ExtKeyID            *int64    `json:"ext_key_id"`
	Harness             string    `json:"harness"`
	InFormat            string    `json:"in_format"`
	Model               string    `json:"model"`
	TurnCount           int       `json:"turn_count"`
	PromptTokens        int64     `json:"prompt_tokens"`
	CompletionTokens    int64     `json:"completion_tokens"`
	TotalTokens         int64     `json:"total_tokens"`
	CacheReadTokens     int64     `json:"cache_read_tokens"`
	CacheCreationTokens int64     `json:"cache_creation_tokens"`
	ReasoningTokens     int64     `json:"reasoning_tokens"`
	Status              string    `json:"status"`
	MsgCount            int       `json:"msg_count"`
	Messages            string    `json:"messages,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
	LastActiveAt        time.Time `json:"last_active_at"`
}

// SessionTurn 是一轮请求对会话聚合表的更新输入，由网关在 convCtx.finish 里
// 组装。所有解析（含 responses 链的会话定位）都在本包内完成——调用方在
// db.Writer 的单 goroutine 里执行它，按轮归档先入队，因此 responses 续接
// 的上一轮会话行对本轮可见，无读写竞态。
type SessionTurn struct {
	// SessionID 是已带来源前缀的会话 id（dsh:/hdr:/cc:）。responses 链
	// 场景下为空，走 PrevResponseID 解析。
	SessionID string
	// PrevResponseID 是 responses 续接的 previous_response_id（可空）。
	PrevResponseID string
	// OwnRespID 是本轮 responses 的 respID（可空）， upsert 成功时写进
	// last_response_id 供下一轮定位。
	OwnRespID string
	// Compact 是显式压缩信号（x-deepseek-harness-compact 头）。
	Compact bool
	// OK 表示本轮调用成功：追加内容 + turn_count+1 + token 累加。
	// 失败轮只刷新已存在会话的 status/last_active_at；首轮即失败不建行
	// （没有可浏览的内容）。
	OK bool

	ExtKeyID *int64
	Harness  string
	InFormat string
	Model    string
	Status   string

	PromptTokens        int
	CompletionTokens    int
	CacheReadTokens     int
	CacheCreationTokens int
	ReasoningTokens     int

	// FullMessages 与 DeltaMessages 二选一（都是 JSON 数组）：
	//   - 无状态格式（openai/anthropic）给 FullMessages——本轮请求的完整
	//     messages，按行内 msg_count 切出增量；
	//   - responses 格式给 DeltaMessages——本轮新增 input（网关已知边界）。
	FullMessages  json.RawMessage
	DeltaMessages json.RawMessage
	// Assistant 是本轮 assistant 输出（单条消息 JSON），OK 时追加。
	Assistant json.RawMessage

	Now time.Time
}

// UpsertSessionTurn 把一轮请求并入会话聚合表：会话不存在 → 当月分表新增行；
// 已存在（可能在上个月的分表）→ 更新原行。只在启用归档的方言上被调用
// （网关层门控；SQLite 单测走 base 表路径）。
func UpsertSessionTurn(d *sql.DB, t *SessionTurn) error {
	if t.SessionID == "" && t.PrevResponseID == "" && t.OwnRespID == "" {
		return nil
	}
	now := t.Now
	if now.IsZero() {
		now = time.Now()
	}
	sid := t.SessionID
	if sid == "" {
		// responses 链：按上一轮的 last_response_id 定位所属会话。
		if t.PrevResponseID != "" {
			prev, _, err := findSessionByLastResp(d, t.PrevResponseID)
			if err != nil {
				return err
			}
			if prev != nil {
				sid = prev.SessionID
			}
		}
		if sid == "" {
			// 首轮（无 pid）→ 以本轮 respID 为根；上一轮没留下行（失败或
			// 功能上线前的历史）→ 以 pid 为根，保持后续轮次还能续上这条链。
			base := t.OwnRespID
			if t.PrevResponseID != "" {
				base = t.PrevResponseID
			}
			if base == "" {
				return nil
			}
			sid = "resp:" + base
		}
	}
	row, shard, err := findSessionBySID(d, sid)
	if err != nil {
		return err
	}
	if row == nil {
		if !t.OK {
			return nil
		}
		return insertSession(d, sid, t, now)
	}
	return updateSession(d, shard, row, t, now)
}

// findSessionBySID 按 session_id 跨分表查找（新→旧，各表走唯一索引）。
// 命中返回行与其所在分表名。
func findSessionBySID(d *sql.DB, sid string) (*ConversationSession, string, error) {
	return findSessionBy(d, "session_id", sid)
}

// findSessionByLastResp 按 last_response_id 跨分表查找（responses 链定位）。
// 只需要会话 id，不读 messages——那是可能上 MiB 的大列，定位查询不该搬它。
func findSessionByLastResp(d *sql.DB, respID string) (*ConversationSession, string, error) {
	shards, err := sessShardSnapshot(d)
	if err != nil {
		return nil, "", fmt.Errorf("session shards: %w", err)
	}
	for _, shard := range shards {
		var s ConversationSession
		err := d.QueryRow(db.Rebind(d,
			`SELECT id, session_id FROM `+shard+` WHERE last_response_id = ?`), respID).
			Scan(&s.ID, &s.SessionID)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, "", fmt.Errorf("find session by last_response_id in %s: %w", shard, err)
		}
		return &s, shard, nil
	}
	return nil, "", nil
}

func findSessionBy(d *sql.DB, col, val string) (*ConversationSession, string, error) {
	shards, err := sessShardSnapshot(d)
	if err != nil {
		return nil, "", fmt.Errorf("session shards: %w", err)
	}
	// 扇出成本随分表数（= 上线以来的月份数）线性增长，但每张表都走索引、
	// 且清单新→旧排列：活跃会话通常在第一张表就命中，老会话最多多几次空查。
	// 这是「会话归属首轮月份、行不搬移」这一选择的已知代价（见设计文档）。
	for _, shard := range shards {
		row, err := scanOneSession(d.QueryRow(db.Rebind(d,
			`SELECT `+sessMetaCols+`, messages FROM `+shard+` WHERE `+col+` = ?`), val))
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, "", fmt.Errorf("find session by %s in %s: %w", col, shard, err)
		}
		return row, shard, nil
	}
	return nil, "", nil
}

// insertSession 在当月分表新建会话行（首轮）。缺表先建再插，与按轮归档
// 同约定；插入撞上 undefined_table（表被外部 DROP）重建一次重试。
func insertSession(d *sql.DB, sid string, t *SessionTurn, now time.Time) error {
	messages, msgCount := mergeSessionMessages("", 0, t)
	err := insertSessionInto(d, t, sid, messages, msgCount, now)
	if err != nil && db.DialectOf(d).SupportsConversationArchive() && db.IsUndefinedTable(err) {
		if cerr := EnsureSessionShard(d, now); cerr != nil {
			return fmt.Errorf("re-ensure session shard: %w", cerr)
		}
		err = insertSessionInto(d, t, sid, messages, msgCount, now)
	}
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

func insertSessionInto(d *sql.DB, t *SessionTurn, sid, messages string, msgCount int, now time.Time) error {
	if db.DialectOf(d).SupportsConversationArchive() {
		key := sessShards.monthKey(now)
		if _, ok := sessShards.forMonth(key); !ok {
			if err := EnsureSessionShard(d, now); err != nil {
				return fmt.Errorf("ensure session shard: %w", err)
			}
		}
	}
	table := sessBaseTable
	if db.DialectOf(d).SupportsConversationArchive() {
		table = SessionShardName(now)
	}
	cols := `session_id, last_response_id, ext_key_id, harness, in_format, model,
		turn_count, prompt_tokens, completion_tokens, total_tokens,
		cache_read_tokens, cache_creation_tokens, reasoning_tokens,
		status, msg_count, messages, created_at, last_active_at`
	ph := `?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?`
	args := []any{
		sid, strPtrOrNil(t.OwnRespID), t.ExtKeyID, t.Harness, t.InFormat, t.Model,
		1, t.PromptTokens, t.CompletionTokens, t.PromptTokens + t.CompletionTokens,
		t.CacheReadTokens, t.CacheCreationTokens, t.ReasoningTokens,
		t.Status, msgCount, messages, now, now,
	}
	if db.DialectOf(d) == db.DialectMySQL {
		id, err := db.NextShardID(d, sessSeqName)
		if err != nil {
			return fmt.Errorf("alloc session id: %w", err)
		}
		cols = "id, " + cols
		ph = "?, " + ph
		args = append([]any{id}, args...)
	}
	_, err := d.Exec(db.Rebind(d, `INSERT INTO `+table+` (`+cols+`) VALUES (`+ph+`)`), args...)
	return err
}

// updateSession 把一轮并入已存在的会话行。OK 轮追加内容并累计数；
// 失败轮只刷新 status / last_active_at（内容没产生，没什么好追加的）。
//
// 失败轮**不动 last_response_id**：网关的会话契约是「失败不保存会话，客户端带
// 同一个 previous_response_id 重试」。若把失败轮的 respID 写进去，客户端按契约
// 用上一成功轮的 pid 重试时就查不到本行，会以 pid 为根新开一行，把一条对话
// 劈成两行。留成最后成功轮的 id，重试才能落回原会话。
func updateSession(d *sql.DB, shard string, row *ConversationSession, t *SessionTurn, now time.Time) error {
	if !t.OK {
		_, err := d.Exec(db.Rebind(d, `UPDATE `+shard+`
			SET status = ?, last_active_at = ?
			WHERE id = ?`), t.Status, now, row.ID)
		if err != nil {
			return fmt.Errorf("update session (error turn): %w", err)
		}
		return nil
	}
	messages, msgCount := mergeSessionMessages(row.Messages, row.MsgCount, t)
	total := t.PromptTokens + t.CompletionTokens
	_, err := d.Exec(db.Rebind(d, `UPDATE `+shard+` SET
		messages = ?, msg_count = ?, turn_count = turn_count + 1,
		prompt_tokens = prompt_tokens + ?, completion_tokens = completion_tokens + ?,
		total_tokens = total_tokens + ?,
		cache_read_tokens = cache_read_tokens + ?,
		cache_creation_tokens = cache_creation_tokens + ?,
		reasoning_tokens = reasoning_tokens + ?,
		status = ?, model = ?, ext_key_id = ?, harness = ?, in_format = ?,
		last_active_at = ?, last_response_id = COALESCE(?, last_response_id)
		WHERE id = ?`),
		messages, msgCount,
		t.PromptTokens, t.CompletionTokens, total,
		t.CacheReadTokens, t.CacheCreationTokens, t.ReasoningTokens,
		t.Status, t.Model, t.ExtKeyID, t.Harness, t.InFormat,
		now, strPtrOrNil(t.OwnRespID), row.ID)
	if err != nil {
		return fmt.Errorf("update session: %w", err)
	}
	return nil
}

// mergeSessionMessages 计算本轮之后 messages 列的新文本与 msg_count 新值。
// existing 必须是 JSON 数组文本（本包写入保证）；空串按 "[]" 处理。
//
// 追加用文本拼接（existing 去掉结尾 ']' + 逗号连接新块），不把可能上 MiB 的
// 存量内容整包 unmarshal/remarshal。超过 sessionMessagesCap 停止追加内容，
// 计数照常。
func mergeSessionMessages(existing string, existingMsgCount int, t *SessionTurn) (string, int) {
	var additions []json.RawMessage
	newMsgCount := existingMsgCount
	if len(t.FullMessages) > 0 {
		var full []json.RawMessage
		if err := json.Unmarshal(t.FullMessages, &full); err != nil {
			logger.Warn("session: full messages decode failed, content skipped", "err", err)
			full = nil
		}
		switch {
		case t.Compact || len(full) < existingMsgCount:
			// 历史压缩：先插边界标记，再整份并入，msg_count 重置为当前
			// 请求消息数（标记不占 msg_count——它只对客户端消息数组计数）。
			//
			// 已知限制：客户端「重新生成」时若重发的是**不含上一条 assistant**
			// 的历史（claude-code 的 regenerate 会这样），长度同样会短于
			// msg_count，被这里当成压缩处理——结果是多一个边界标记、重发部分
			// 被再并入一次。不是数据损坏（内容仍在），只是浏览视图有噪音；
			// 精确区分需要前缀哈希，见设计文档「已知限制」。
			additions = append(additions, compactionMarker)
			additions = append(additions, full...)
			newMsgCount = len(full)
		case len(full) > existingMsgCount:
			additions = append(additions, full[existingMsgCount:]...)
			newMsgCount = len(full)
		default:
			// len(full) == existingMsgCount：客户端原样重发（重试），无新增。
		}
	} else if len(t.DeltaMessages) > 0 {
		var delta []json.RawMessage
		if err := json.Unmarshal(t.DeltaMessages, &delta); err != nil {
			logger.Warn("session: delta messages decode failed, content skipped", "err", err)
			delta = nil
		}
		additions = append(additions, delta...)
		newMsgCount = existingMsgCount + len(delta)
	}
	if len(t.Assistant) > 0 {
		additions = append(additions, t.Assistant)
		newMsgCount++
	}
	if len(additions) == 0 || len(existing) > sessionMessagesCap {
		if existing == "" {
			return "[]", newMsgCount
		}
		return existing, newMsgCount
	}
	var sb strings.Builder
	if existing == "" || existing == "[]" {
		sb.WriteByte('[')
	} else {
		sb.WriteString(strings.TrimSuffix(strings.TrimSpace(existing), "]"))
		sb.WriteByte(',')
	}
	for i, a := range additions {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.Write(a)
	}
	sb.WriteByte(']')
	out := sb.String()
	if len(out) > sessionMessagesCap {
		// 本轮追加后超限：保留追加前的内容，下轮起 len(existing) > cap
		// 走上面的早退分支，永久停止追加。
		logger.Warn("session: messages cap reached, content truncated",
			"cap", sessionMessagesCap, "size", len(out))
		if existing == "" {
			return "[]", newMsgCount
		}
		return existing, newMsgCount
	}
	return out, newMsgCount
}

func strPtrOrNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// sessMetaCols 是列表/详情共用的元数据列（详情追加 messages）。
const sessMetaCols = `id, session_id, last_response_id, ext_key_id, harness, in_format, model,
	turn_count, prompt_tokens, completion_tokens, total_tokens,
	cache_read_tokens, cache_creation_tokens, reasoning_tokens,
	status, msg_count, created_at, last_active_at`

// scanSession 按 sessMetaCols（withMessages 时追加 messages）的列顺序扫描一行。
func scanSession(scan func(dest ...any) error, s *ConversationSession, withMessages bool) error {
	var lastResp sql.NullString
	var extKeyID sql.NullInt64
	dest := []any{&s.ID, &s.SessionID, &lastResp, &extKeyID, &s.Harness, &s.InFormat, &s.Model,
		&s.TurnCount, &s.PromptTokens, &s.CompletionTokens, &s.TotalTokens,
		&s.CacheReadTokens, &s.CacheCreationTokens, &s.ReasoningTokens,
		&s.Status, &s.MsgCount, &s.CreatedAt, &s.LastActiveAt}
	if withMessages {
		dest = append(dest, &s.Messages)
	}
	if err := scan(dest...); err != nil {
		return err
	}
	if lastResp.Valid {
		v := lastResp.String
		s.LastResponseID = &v
	}
	if extKeyID.Valid {
		id := extKeyID.Int64
		s.ExtKeyID = &id
	}
	return nil
}

func scanOneSession(row *sql.Row) (*ConversationSession, error) {
	var s ConversationSession
	if err := scanSession(row.Scan, &s, true); err != nil {
		return nil, err
	}
	return &s, nil
}

// ConversationSessionsList 分页列出会话聚合（分表内按 last_active_at 新→旧，
// 分表按月份新→旧拼接；跨月续聊的会话留在首轮月份的分表里）。只含元数据列。
// page/size 规范化与 ConversationRecordsList 一致。
func ConversationSessionsList(d *sql.DB, page, size int) ([]ConversationSession, int, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 200 {
		size = 50
	}
	shards, err := sessShardSnapshot(d)
	if err != nil {
		return nil, 0, fmt.Errorf("session shards: %w", err)
	}
	if len(shards) == 0 {
		return []ConversationSession{}, 0, nil
	}
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
	out := make([]ConversationSession, 0, size)
	for _, w := range convPageWindows(counts, (page-1)*size, size) {
		rows, err := listSessionsFrom(d, shards[w.shard], w.limit, w.offset)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, rows...)
	}
	return out, total, nil
}

// listSessionsFrom 查单张分表的一页（last_active_at 新→旧；秒级精度，
// 同秒用 id 决胜保证翻页稳定）。表名同分表白名单约定。
func listSessionsFrom(d *sql.DB, table string, limit, offset int) ([]ConversationSession, error) {
	rows, err := d.Query(db.Rebind(d, `SELECT `+sessMetaCols+`
		FROM `+table+` ORDER BY last_active_at DESC, id DESC LIMIT ? OFFSET ?`), limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list sessions from %s: %w", table, err)
	}
	defer rows.Close()
	out := make([]ConversationSession, 0)
	for rows.Next() {
		var s ConversationSession
		if err := scanSession(rows.Scan, &s, false); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// GetConversationSession 取单个会话（含 messages）。不存在返回 sql.ErrNoRows。
func GetConversationSession(d *sql.DB, id int64) (*ConversationSession, error) {
	shards, err := sessShardSnapshot(d)
	if err != nil {
		return nil, fmt.Errorf("session shards: %w", err)
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
		fmt.Fprintf(&sb, `SELECT %s, messages FROM %s WHERE id = ?`, sessMetaCols, t)
		args = append(args, id)
	}
	return scanOneSession(d.QueryRow(db.Rebind(d, sb.String()), args...))
}
