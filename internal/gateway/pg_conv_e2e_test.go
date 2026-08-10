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
	d := stdlib.OpenDB(*cfg)
	if err := d.Ping(); err != nil {
		d.Close()
		t.Fatalf("ping: %v", err)
	}
	schema := fmt.Sprintf("any_llm_conv_test_%d", time.Now().UnixNano())
	if _, err := d.Exec(fmt.Sprintf("CREATE SCHEMA %s", schema)); err != nil {
		d.Close()
		t.Fatalf("create schema: %v", err)
	}
	if _, err := d.Exec(fmt.Sprintf("SET search_path TO %s", schema)); err != nil {
		d.Close()
		t.Fatalf("set search_path: %v", err)
	}
	if err := db.MigratePGForTest(d); err != nil {
		d.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
		d.Close()
		t.Fatalf("migrate: %v", err)
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
	k, _ := model.CreateExtKey(d, "test", 0, 0)
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
	err := d.QueryRow(`SELECT harness, user_agent, status, in_format, stream,
		prompt_tokens, completion_tokens, total_tokens, request_ir, response_ir,
		request_raw, response_raw, ext_key_id FROM conversation_records`).Scan(
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
	if !strings.Contains(reqIR, `"Model":"gpt-4o"`) {
		t.Errorf("request_ir missing model: %s", reqIR)
	}
	if !strings.Contains(respIR, `"content":"ok"`) && !strings.Contains(respIR, `"Text":"ok"`) {
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
	k, _ := model.CreateExtKey(d, "test", 0, 0)
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
	err := d.QueryRow(`SELECT harness, status, stream, response_ir, response_raw FROM conversation_records`).Scan(
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

func TestPGConvUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(`{"error":{"message":"boom","type":"server_error"}}`))
	}))
	defer srv.Close()

	g, d, w := setupPGGateway(t)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "oai", BaseURL: srv.URL, APIKey: "k", Format: "openai"})
	model.AddModel(d, uid, "gpt-4o", false, 0, 0)
	k, _ := model.CreateExtKey(d, "test", 0, 0)
	g.client = upstream.NewClient(http.DefaultClient)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"oai/gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	flushConv(w)

	var status string
	var tt int
	err := d.QueryRow(`SELECT status, total_tokens FROM conversation_records`).Scan(&status, &tt)
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
