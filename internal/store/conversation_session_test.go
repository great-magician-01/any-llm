package store

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// createSessionTable 建 conversation_sessions 的 SQLite 等价表（单测用；
// SQLite 不启用归档，生产建表由 EnsureSessionShard 按月分表完成）。
// 建表后重载会话分表注册缓存，让它作为「历史分表」被发现。
func createSessionTable(t *testing.T, d *sql.DB) {
	t.Helper()
	resetShardCache(t, sessShards)
	_, err := d.Exec(`CREATE TABLE conversation_sessions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT NOT NULL,
		last_response_id TEXT,
		ext_key_id INTEGER,
		harness TEXT NOT NULL,
		in_format TEXT NOT NULL,
		model TEXT NOT NULL,
		turn_count INTEGER NOT NULL DEFAULT 0,
		prompt_tokens INTEGER NOT NULL DEFAULT 0,
		completion_tokens INTEGER NOT NULL DEFAULT 0,
		total_tokens INTEGER NOT NULL DEFAULT 0,
		cache_read_tokens INTEGER NOT NULL DEFAULT 0,
		cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
		reasoning_tokens INTEGER NOT NULL DEFAULT 0,
		status TEXT NOT NULL DEFAULT 'ok',
		msg_count INTEGER NOT NULL DEFAULT 0,
		messages TEXT NOT NULL DEFAULT '[]',
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		last_active_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`CREATE UNIQUE INDEX idx_sess_test_sid ON conversation_sessions(session_id)`); err != nil {
		t.Fatal(err)
	}
	if err := LoadSessionShards(d); err != nil {
		t.Fatal(err)
	}
}

func msgJSON(msgs ...string) json.RawMessage {
	arr := make([]json.RawMessage, 0, len(msgs))
	for _, m := range msgs {
		arr = append(arr, json.RawMessage(m))
	}
	b, _ := json.Marshal(arr)
	return b
}

const (
	tUser1 = `{"role":"user","content":[{"type":"text","text":"hi"}]}`
	tAsst1 = `{"role":"assistant","content":[{"type":"text","text":"hello"}]}`
	tUser2 = `{"role":"user","content":[{"type":"text","text":"how"}]}`
	tAsst2 = `{"role":"assistant","content":[{"type":"text","text":"fine"}]}`
)

func turnOK(sid string, full, delta, assistant json.RawMessage) *SessionTurn {
	return &SessionTurn{
		SessionID: sid, OK: true,
		Harness: "claude-code", InFormat: "anthropic", Model: "m", Status: "ok",
		PromptTokens: 10, CompletionTokens: 5,
		FullMessages: full, DeltaMessages: delta, Assistant: assistant,
		Now: time.Now(),
	}
}

func messagesOf(t *testing.T, d *sql.DB, sid string) (string, int) {
	t.Helper()
	var msgs string
	var cnt int
	if err := d.QueryRow(`SELECT messages, msg_count FROM conversation_sessions WHERE session_id = ?`, sid).
		Scan(&msgs, &cnt); err != nil {
		t.Fatal(err)
	}
	return msgs, cnt
}

func TestUpsertSessionTurn_FirstTurnInsert(t *testing.T) {
	d := testDB(t)
	createSessionTable(t, d)

	if err := UpsertSessionTurn(d, turnOK("cc:abc", msgJSON(tUser1), nil, json.RawMessage(tAsst1))); err != nil {
		t.Fatal(err)
	}
	msgs, cnt := messagesOf(t, d, "cc:abc")
	if cnt != 2 { // user + assistant
		t.Fatalf("msg_count=%d, want 2", cnt)
	}
	var arr []json.RawMessage
	if err := json.Unmarshal([]byte(msgs), &arr); err != nil || len(arr) != 2 {
		t.Fatalf("messages=%s len=%d err=%v", msgs, len(arr), err)
	}
	var turnCount, prompt, completion int
	if err := d.QueryRow(`SELECT turn_count, prompt_tokens, completion_tokens FROM conversation_sessions WHERE session_id='cc:abc'`).
		Scan(&turnCount, &prompt, &completion); err != nil {
		t.Fatal(err)
	}
	if turnCount != 1 || prompt != 10 || completion != 5 {
		t.Fatalf("turn=%d prompt=%d completion=%d", turnCount, prompt, completion)
	}
}

func TestUpsertSessionTurn_IncrementalAppend(t *testing.T) {
	d := testDB(t)
	createSessionTable(t, d)

	// 第一轮：1 条 user
	if err := UpsertSessionTurn(d, turnOK("cc:abc", msgJSON(tUser1), nil, json.RawMessage(tAsst1))); err != nil {
		t.Fatal(err)
	}
	// 第二轮：客户端重发全量历史（user1 + asst1）+ 新 user2 → 只追加增量
	full2 := msgJSON(tUser1, tAsst1, tUser2)
	if err := UpsertSessionTurn(d, turnOK("cc:abc", full2, nil, json.RawMessage(tAsst2))); err != nil {
		t.Fatal(err)
	}
	msgs, cnt := messagesOf(t, d, "cc:abc")
	var arr []json.RawMessage
	if err := json.Unmarshal([]byte(msgs), &arr); err != nil {
		t.Fatal(err)
	}
	if len(arr) != 4 || cnt != 4 { // user1 asst1 user2 asst2，无重复
		t.Fatalf("messages len=%d msg_count=%d, want 4/4: %s", len(arr), cnt, msgs)
	}
	var turnCount, prompt int
	if err := d.QueryRow(`SELECT turn_count, prompt_tokens FROM conversation_sessions WHERE session_id='cc:abc'`).
		Scan(&turnCount, &prompt); err != nil {
		t.Fatal(err)
	}
	if turnCount != 2 || prompt != 20 {
		t.Fatalf("turn=%d prompt=%d, want 2/20", turnCount, prompt)
	}
	// 数组文本必须合法（文本拼接不能破坏 JSON）
	if !json.Valid([]byte(msgs)) {
		t.Fatalf("messages is not valid JSON: %s", msgs)
	}
}

func TestUpsertSessionTurn_Compaction(t *testing.T) {
	d := testDB(t)
	createSessionTable(t, d)

	if err := UpsertSessionTurn(d, turnOK("cc:abc", msgJSON(tUser1), nil, json.RawMessage(tAsst1))); err != nil {
		t.Fatal(err)
	}
	// 压缩后历史变短（1 条 < 已记录的 2 条）：标记 + 全量并入，链不断
	compactFull := msgJSON(`{"role":"user","content":[{"type":"text","text":"summary"}]}`)
	turn := turnOK("cc:abc", compactFull, nil, json.RawMessage(tAsst2))
	if err := UpsertSessionTurn(d, turn); err != nil {
		t.Fatal(err)
	}
	msgs, cnt := messagesOf(t, d, "cc:abc")
	if !strings.Contains(msgs, "_marker") {
		t.Fatalf("compaction marker missing: %s", msgs)
	}
	// marker + summary + assistant；msg_count 重置为压缩后历史 1 + assistant 1
	if cnt != 2 {
		t.Fatalf("msg_count=%d, want 2", cnt)
	}

	// 显式压缩头同样触发
	turn = turnOK("cc:abc", compactFull, nil, json.RawMessage(tAsst2))
	turn.Compact = true
	if err := UpsertSessionTurn(d, turn); err != nil {
		t.Fatal(err)
	}
	msgs, _ = messagesOf(t, d, "cc:abc")
	if strings.Count(msgs, "_marker") != 2 {
		t.Fatalf("explicit compact should add a second marker: %s", msgs)
	}
}

func TestUpsertSessionTurn_ErrorTurn(t *testing.T) {
	d := testDB(t)
	createSessionTable(t, d)

	// 首轮即失败：不建行
	errTurn := &SessionTurn{SessionID: "cc:bad", OK: false, Status: "error", Harness: "h", Now: time.Now()}
	if err := UpsertSessionTurn(d, errTurn); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM conversation_sessions`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("error-first session should not be created: n=%d err=%v", n, err)
	}

	// 已有会话的失败轮：只刷状态，内容/轮数/token 不变
	if err := UpsertSessionTurn(d, turnOK("cc:abc", msgJSON(tUser1), nil, json.RawMessage(tAsst1))); err != nil {
		t.Fatal(err)
	}
	if err := UpsertSessionTurn(d, &SessionTurn{SessionID: "cc:abc", OK: false, Status: "error", Harness: "h", Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	var turnCount, prompt int
	var status string
	if err := d.QueryRow(`SELECT turn_count, prompt_tokens, status FROM conversation_sessions WHERE session_id='cc:abc'`).
		Scan(&turnCount, &prompt, &status); err != nil {
		t.Fatal(err)
	}
	if turnCount != 1 || prompt != 10 || status != "error" {
		t.Fatalf("turn=%d prompt=%d status=%s", turnCount, prompt, status)
	}
	msgs, cnt := messagesOf(t, d, "cc:abc")
	if cnt != 2 || strings.Contains(msgs, "fine") {
		t.Fatalf("error turn must not append content: cnt=%d msgs=%s", cnt, msgs)
	}
}

func TestUpsertSessionTurn_ResponsesChain(t *testing.T) {
	d := testDB(t)
	createSessionTable(t, d)

	// 首轮：无 pid，以本轮 respID 为根
	turn1 := turnOK("", nil, msgJSON(tUser1), json.RawMessage(tAsst1))
	turn1.OwnRespID = "resp_1"
	turn1.InFormat = "responses"
	if err := UpsertSessionTurn(d, turn1); err != nil {
		t.Fatal(err)
	}
	// 续接轮：按 last_response_id 定位到首轮的会话
	turn2 := turnOK("", nil, msgJSON(tUser2), json.RawMessage(tAsst2))
	turn2.PrevResponseID = "resp_1"
	turn2.OwnRespID = "resp_2"
	turn2.InFormat = "responses"
	if err := UpsertSessionTurn(d, turn2); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM conversation_sessions`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("chain should stay one session: n=%d err=%v", n, err)
	}
	var sid, lastResp string
	if err := d.QueryRow(`SELECT session_id, last_response_id FROM conversation_sessions`).Scan(&sid, &lastResp); err != nil {
		t.Fatal(err)
	}
	if sid != "resp:resp_1" || lastResp != "resp_2" {
		t.Fatalf("sid=%s last_resp=%s", sid, lastResp)
	}
	msgs, cnt := messagesOf(t, d, "resp:resp_1")
	var arr []json.RawMessage
	if err := json.Unmarshal([]byte(msgs), &arr); err != nil || len(arr) != 4 || cnt != 4 {
		t.Fatalf("chain messages len=%d cnt=%d: %s", len(arr), cnt, msgs)
	}

	// pid 未知（上一轮失败或历史数据）：以 pid 为根开新会话，后续轮还能续上
	turn3 := turnOK("", nil, msgJSON(tUser1), json.RawMessage(tAsst1))
	turn3.PrevResponseID = "resp_lost"
	turn3.OwnRespID = "resp_3"
	turn3.InFormat = "responses"
	if err := UpsertSessionTurn(d, turn3); err != nil {
		t.Fatal(err)
	}
	if _, cnt := messagesOf(t, d, "resp:resp_lost"); cnt != 2 {
		t.Fatalf("orphan chain root: cnt=%d", cnt)
	}
}

func TestUpsertSessionTurn_NoID(t *testing.T) {
	d := testDB(t)
	createSessionTable(t, d)
	if err := UpsertSessionTurn(d, &SessionTurn{OK: true, Status: "ok", Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM conversation_sessions`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("no-id turn must not create session: n=%d err=%v", n, err)
	}
}

func TestConversationSessionsListAndGet(t *testing.T) {
	d := testDB(t)
	createSessionTable(t, d)

	for _, sid := range []string{"cc:a", "cc:b", "cc:c"} {
		if err := UpsertSessionTurn(d, turnOK(sid, msgJSON(tUser1), nil, json.RawMessage(tAsst1))); err != nil {
			t.Fatal(err)
		}
	}
	list, total, err := ConversationSessionsList(d, 1, 50)
	if err != nil || total != 3 || len(list) != 3 {
		t.Fatalf("list total=%d len=%d err=%v", total, len(list), err)
	}
	for _, s := range list {
		if s.Messages != "" {
			t.Fatalf("list must not include messages: %+v", s)
		}
	}
	// 翻页
	page1, _, _ := ConversationSessionsList(d, 1, 2)
	page2, _, _ := ConversationSessionsList(d, 2, 2)
	if len(page1) != 2 || len(page2) != 1 {
		t.Fatalf("paging: %d/%d", len(page1), len(page2))
	}
	seen := map[int64]bool{}
	for _, s := range append(page1, page2...) {
		if seen[s.ID] {
			t.Fatalf("duplicate row across pages: %d", s.ID)
		}
		seen[s.ID] = true
	}
	// 详情含 messages；不存在 → ErrNoRows
	detail, err := GetConversationSession(d, page1[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid([]byte(detail.Messages)) || detail.Messages == "[]" {
		t.Fatalf("detail messages: %q", detail.Messages)
	}
	if _, err := GetConversationSession(d, 9999); err != sql.ErrNoRows {
		t.Fatalf("missing session: err=%v", err)
	}
}

// TestUpsertSessionTurn_FailedTurnKeepsChain 钉住 responses 链的失败重试语义：
// 失败轮不得推进 last_response_id，否则客户端按网关契约带上一成功轮的
// previous_response_id 重试时会查不到本行，把一条对话劈成两行。
func TestUpsertSessionTurn_FailedTurnKeepsChain(t *testing.T) {
	d := testDB(t)
	createSessionTable(t, d)

	mkTurn := func(prev, own string) *SessionTurn {
		turn := turnOK("", nil, msgJSON(tUser1), json.RawMessage(tAsst1))
		turn.InFormat = "responses"
		turn.PrevResponseID = prev
		turn.OwnRespID = own
		return turn
	}
	// turn1 ok（根）→ turn2 ok（续接）
	if err := UpsertSessionTurn(d, mkTurn("", "resp_1")); err != nil {
		t.Fatal(err)
	}
	if err := UpsertSessionTurn(d, mkTurn("resp_1", "resp_2")); err != nil {
		t.Fatal(err)
	}

	// turn3 失败：内容不追加，last_response_id 必须仍停在 resp_2
	fail := &SessionTurn{
		PrevResponseID: "resp_2", OwnRespID: "resp_3", OK: false, Status: "error",
		InFormat: "responses", Harness: "h", Now: time.Now(),
	}
	if err := UpsertSessionTurn(d, fail); err != nil {
		t.Fatal(err)
	}
	var sid, lastResp, status string
	if err := d.QueryRow(`SELECT session_id, last_response_id, status FROM conversation_sessions`).Scan(&sid, &lastResp, &status); err != nil {
		t.Fatal(err)
	}
	if lastResp != "resp_2" || status != "error" {
		t.Fatalf("failed turn must keep last_response_id=resp_2: got %q status=%q", lastResp, status)
	}

	// 客户端按契约带 pid=resp_2 重试：必须落回同一会话，不新增行
	if err := UpsertSessionTurn(d, mkTurn("resp_2", "resp_4")); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := d.QueryRow(`SELECT COUNT(*) FROM conversation_sessions`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("retry after failure must stay in one session: rows=%d err=%v", rows, err)
	}
	var lastResp2 string
	if err := d.QueryRow(`SELECT last_response_id FROM conversation_sessions`).Scan(&lastResp2); err != nil {
		t.Fatal(err)
	}
	if sid != "resp:resp_1" || lastResp2 != "resp_4" {
		t.Fatalf("sid=%q last_resp=%q, want resp:resp_1 / resp_4", sid, lastResp2)
	}
}
