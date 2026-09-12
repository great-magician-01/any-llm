package gateway

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/great-magician-01/any-llm/internal/db"
	"github.com/great-magician-01/any-llm/internal/model"
	"github.com/great-magician-01/any-llm/internal/upstream"
)

// pgConvTestDB 连接 DB_TEST_PG_DSN，在独立 schema 里跑 PG 迁移（含
// conversation_records），测试结束 drop schema。未配置 DSN 时跳过。
// gateway 包无法复用 internal/db 的未导出 pgTestDB，故自带一份。
func pgConvTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("DB_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set DB_TEST_PG_DSN to run postgres e2e tests")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	schema := fmt.Sprintf("any_llm_conv_test_%d", time.Now().UnixNano())
	// search_path 走连接参数而非 SET：pgx 连接池里 SET 只影响单条连接，
	// 网关的异步 writer 等其他连接会漏配（曾导致查询打到默认 schema）。
	cfg.RuntimeParams["search_path"] = schema
	d := stdlib.OpenDB(*cfg)
	if err := d.Ping(); err != nil {
		d.Close()
		t.Fatalf("ping: %v", err)
	}
	if _, err := d.Exec(fmt.Sprintf("CREATE SCHEMA %s", schema)); err != nil {
		d.Close()
		t.Fatalf("create schema: %v", err)
	}
	if err := db.MigratePGForTest(d); err != nil {
		d.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
		d.Close()
		t.Fatalf("migrate: %v", err)
	}
	// 分表注册缓存是进程级全局变量：每个测试用自己的 schema，需按当前
	// schema 重载，避免串到其他测试的缓存状态。
	if err := model.LoadConvShards(d); err != nil {
		d.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
		d.Close()
		t.Fatalf("load conversation shards: %v", err)
	}
	t.Cleanup(func() {
		d.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
		d.Close()
	})
	return d
}

// setupPGGateway 基于 PG 建 Gateway（带真实 writer，走 DoAsync 路径）。
func setupPGGateway(t *testing.T) (*Gateway, *sql.DB, *db.Writer) {
	t.Helper()
	d := pgConvTestDB(t)
	w := db.NewWriter(d, 512)
	w.Start()
	t.Cleanup(w.Stop)
	g := New(d, w, nil)
	return g, d, w
}

// flushConv 用一次 DoSync 空操作做写屏障：writer 单 goroutine 顺序执行，
// DoSync 返回即表示此前 DoAsync 的对话记录已落库。
func flushConv(w *db.Writer) {
	_ = w.DoSync(func(d *sql.DB) error { return nil })
}

func TestPGConvNonStream(t *testing.T) {
	mockBody := `{"id":"c1","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":7,"total_tokens":12}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(mockBody))
	}))
	defer srv.Close()

	g, d, w := setupPGGateway(t)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "oai", BaseURL: srv.URL, APIKey: "k", Format: "openai"})
	model.AddModel(d, uid, "gpt-4o", false, 0, 0)
	k, _ := model.CreateExtKey(d, "test", 0, 0, nil)
	g.client = upstream.NewClient(http.DefaultClient)

	reqBody := `{"model":"oai/gpt-4o","messages":[{"role":"user","content":"hi"}],"max_tokens":50}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	req.Header.Set("User-Agent", "claude-code/1.0.71")
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	flushConv(w)

	var (
		harness, userAgent, status, inFmt string
		stream                            int
		pt, ct, tt                        int
		reqIR, respIR                     string
		reqRaw, respRaw                   []byte
		extKeyID                          int64
	)
	// 归档按月分表：新记录落在当月分表。
	err := d.QueryRow(`SELECT harness, user_agent, status, in_format, stream,
		prompt_tokens, completion_tokens, total_tokens, request_ir, response_ir,
		request_raw, response_raw, ext_key_id FROM `+model.ConvShardName(time.Now())).Scan(
		&harness, &userAgent, &status, &inFmt, &stream,
		&pt, &ct, &tt, &reqIR, &respIR, &reqRaw, &respRaw, &extKeyID)
	if err != nil {
		t.Fatalf("query conversation_records: %v", err)
	}
	if harness != "claude-code" {
		t.Errorf("harness=%q, want claude-code", harness)
	}
	if userAgent != "claude-code/1.0.71" {
		t.Errorf("user_agent=%q", userAgent)
	}
	if status != "ok" || stream != 0 || inFmt != "openai" {
		t.Errorf("status=%q stream=%d in_format=%q", status, stream, inFmt)
	}
	if pt != 5 || ct != 7 || tt != 12 {
		t.Errorf("tokens=%d/%d/%d, want 5/7/12", pt, ct, tt)
	}
	if string(reqRaw) != reqBody {
		t.Errorf("request_raw=%q, want sent body", reqRaw)
	}
	if string(respRaw) != mockBody {
		t.Errorf("response_raw mismatch:\n got %s\nwant %s", respRaw, mockBody)
	}
	// 注意：jsonb 经 PG 输出时格式为 `"Model": "gpt-4o"`（冒号后带空格），
	// 断言不依赖紧凑格式。
	if !strings.Contains(reqIR, `"Model"`) || !strings.Contains(reqIR, `"gpt-4o"`) {
		t.Errorf("request_ir missing model: %s", reqIR)
	}
	if !strings.Contains(respIR, `"ok"`) {
		t.Errorf("response_ir missing content: %s", respIR)
	}
	if extKeyID != k.ID {
		t.Errorf("ext_key_id=%d, want %d", extKeyID, k.ID)
	}
}

func TestPGConvStream(t *testing.T) {
	// Anthropic 上游流：text + thinking(含真签名) + tool_use。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		frames := []string{
			"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-3-5\",\"content\":[],\"usage\":{\"input_tokens\":10,\"output_tokens\":1}}}\n\n",
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"pondering\"}}\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"sig_xyz\"}}\n\n",
			"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"answer\"}}\n\n",
			"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n",
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":4}}\n\n",
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
		}
		for _, fr := range frames {
			w.Write([]byte(fr))
			f.Flush()
		}
	}))
	defer srv.Close()

	g, d, w := setupPGGateway(t)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "ant", BaseURL: srv.URL, APIKey: "sk-ant", Format: "anthropic"})
	model.AddModel(d, uid, "claude-3-5", false, 0, 0)
	k, _ := model.CreateExtKey(d, "test", 0, 0, nil)
	g.client = upstream.NewClient(http.DefaultClient)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"ant/claude-3-5","messages":[{"role":"user","content":"hi"}],"stream":true}`))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	req.Header.Set("User-Agent", "codex/0.10")
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	flushConv(w)

	var (
		harness, status, respIR string
		stream                  int
		respRaw                 []byte
	)
	err := d.QueryRow(`SELECT harness, status, stream, response_ir, response_raw FROM `+model.ConvShardName(time.Now())).Scan(
		&harness, &status, &stream, &respIR, &respRaw)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if harness != "codex" {
		t.Errorf("harness=%q, want codex", harness)
	}
	if status != "ok" || stream != 1 {
		t.Errorf("status=%q stream=%d", status, stream)
	}
	// response_ir 应含思维链文本 + 真签名 + 文本块。
	if !strings.Contains(respIR, "pondering") {
		t.Errorf("response_ir missing thinking: %s", respIR)
	}
	if !strings.Contains(respIR, "sig_xyz") {
		t.Errorf("response_ir missing real signature: %s", respIR)
	}
	if !strings.Contains(respIR, "answer") {
		t.Errorf("response_ir missing text: %s", respIR)
	}
	// response_raw 是发给客户端（openai 格式）的 SSE 帧。
	if !strings.Contains(string(respRaw), "data: ") {
		t.Errorf("response_raw missing SSE frames: %q", respRaw)
	}
}

// TestPGConvShardedReadWithLegacy 验证应用层分表的读取合并：存量库的
// conversation_records（历史分表，含旧数据）+ 当月分表（新写入）一起参与
// 列表分页与详情查询，顺序新→旧、翻页跨表拼接正确。
func TestPGConvShardedReadWithLegacy(t *testing.T) {
	mockBody := `{"id":"c1","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":7,"total_tokens":12}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(mockBody))
	}))
	defer srv.Close()

	g, d, w := setupPGGateway(t)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "oai", BaseURL: srv.URL, APIKey: "k", Format: "openai"})
	model.AddModel(d, uid, "gpt-4o", false, 0, 0)
	k, _ := model.CreateExtKey(d, "test", 0, 0, nil)
	g.client = upstream.NewClient(http.DefaultClient)

	// 模拟存量库：历史分表（沿用旧 schema 的 BIGSERIAL，自带共享序列）+ 一条旧数据。
	_, err := d.Exec(`CREATE TABLE conversation_records (
		id BIGSERIAL PRIMARY KEY,
		ext_key_id BIGINT,
		upstream_id BIGINT,
		upstream_name TEXT NOT NULL,
		model TEXT NOT NULL,
		in_format TEXT NOT NULL,
		up_format TEXT NOT NULL,
		harness TEXT NOT NULL,
		user_agent TEXT NOT NULL,
		stream INTEGER NOT NULL DEFAULT 0,
		status TEXT NOT NULL DEFAULT 'ok',
		prompt_tokens INTEGER NOT NULL DEFAULT 0,
		completion_tokens INTEGER NOT NULL DEFAULT 0,
		total_tokens INTEGER NOT NULL DEFAULT 0,
		cache_read_tokens INTEGER NOT NULL DEFAULT 0,
		cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
		reasoning_tokens INTEGER NOT NULL DEFAULT 0,
		request_ir JSONB NOT NULL DEFAULT '{}'::jsonb,
		response_ir JSONB NOT NULL DEFAULT '{}'::jsonb,
		request_raw BYTEA NOT NULL,
		response_raw BYTEA NOT NULL,
		created_at TIMESTAMP(0) NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO conversation_records
		(upstream_name, model, in_format, up_format, harness, user_agent,
		 request_raw, response_raw, created_at)
		VALUES ('u', 'legacy-model', 'openai', 'openai', 'claude-code', 'old-agent',
			'{}', '{}', '2025-01-15 10:00:00')`); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}
	// 历史分表是启动后（外部）出现的：重载注册缓存让其生效。
	if err := model.LoadConvShards(d); err != nil {
		t.Fatalf("reload shards: %v", err)
	}

	// 真实网关请求 → 新记录落当月分表。
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"oai/gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	flushConv(w)

	// 列表：total=2，新→旧（分表块序：月分表在前，历史分表在后）。
	records, total, err := model.ConversationRecordsList(d, 1, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 2 || len(records) != 2 {
		t.Fatalf("total=%d len=%d, want 2/2", total, len(records))
	}
	if records[0].Model != "gpt-4o" || records[1].Model != "legacy-model" {
		t.Fatalf("order=%q,%q, want gpt-4o,legacy-model", records[0].Model, records[1].Model)
	}
	newID := records[0].ID

	// 翻页跨表拼接：size=1 时第 1 页是月分表的新记录，第 2 页是历史分表的旧记录。
	p1, _, err := model.ConversationRecordsList(d, 1, 1)
	if err != nil || len(p1) != 1 || p1[0].Model != "gpt-4o" {
		t.Fatalf("page1=%+v err=%v", p1, err)
	}
	p2, _, err := model.ConversationRecordsList(d, 2, 1)
	if err != nil || len(p2) != 1 || p2[0].Model != "legacy-model" {
		t.Fatalf("page2=%+v err=%v", p2, err)
	}

	// 详情：两条记录跨分表都能按 id 取到（id 由共享序列保证全局唯一）。
	if _, err := model.GetConversation(d, p2[0].ID); err != nil {
		t.Fatalf("get legacy id=%d: %v", p2[0].ID, err)
	}
	got, err := model.GetConversation(d, newID)
	if err != nil {
		t.Fatalf("get new id=%d: %v", newID, err)
	}
	if got.Model != "gpt-4o" || !strings.Contains(got.RequestIR, `"Messages"`) {
		t.Fatalf("got=%+v", got)
	}
}

func TestPGConvUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(`{"error":{"message":"boom","type":"server_error"}}`))
	}))
	defer srv.Close()

	g, d, w := setupPGGateway(t)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "oai", BaseURL: srv.URL, APIKey: "k", Format: "openai"})
	model.AddModel(d, uid, "gpt-4o", false, 0, 0)
	k, _ := model.CreateExtKey(d, "test", 0, 0, nil)
	g.client = upstream.NewClient(http.DefaultClient)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"oai/gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	flushConv(w)

	var status string
	var tt int
	err := d.QueryRow(`SELECT status, total_tokens FROM `+model.ConvShardName(time.Now())).Scan(&status, &tt)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if status != "error" {
		t.Errorf("status=%q, want error", status)
	}
	if tt != 0 {
		t.Errorf("total_tokens=%d, want 0", tt)
	}
}
