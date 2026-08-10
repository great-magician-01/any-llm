package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/great-magician-01/any-llm/internal/model"
	"github.com/great-magician-01/any-llm/internal/translate"
	"github.com/great-magician-01/any-llm/internal/upstream"
)

// SQLite 上 newConvCtx 必须返回 nil——结构性证明对话归档是 PG-only。
func TestConversationDisabledOnSQLite(t *testing.T) {
	g, _ := setupGateway(t)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"oai/gpt-4o"}`))
	req.Header.Set("User-Agent", "claude-code/1.0")
	rec := g.newConvCtx(req, nil, nil, "gpt-4o", "openai", nil, []byte("{}"))
	if rec != nil {
		t.Fatalf("newConvCtx on SQLite = %+v, want nil (PG-only)", rec)
	}
}

// nil convCtx 的 finish 必须安全无操作（SQLite 路径零开销）。
func TestConvCtxFinishNilSafe(t *testing.T) {
	var rec *convCtx
	rec.finish("ok", translate.Usage{}, nil) // 不应 panic
}

// teeWriter：写入透传并被捕获，到 max 后截断，且仍满足 http.Flusher。
func TestTeeWriter(t *testing.T) {
	rr := httptest.NewRecorder()
	tw := newTeeWriter(rr, 5)
	if _, ok := any(tw).(http.Flusher); !ok {
		t.Fatal("*teeWriter must satisfy http.Flusher")
	}
	tw.Write([]byte("hello"))
	tw.Write([]byte("world")) // 超出 max=5，应被截断丢弃
	if rr.Body.String() != "helloworld" {
		t.Errorf("passthrough = %q, want helloworld", rr.Body.String())
	}
	if tw.buf.String() != "hello" {
		t.Errorf("captured = %q, want hello (capped at 5)", tw.buf.String())
	}
}

// 回归：SQLite 下一次非流式请求照常工作（recordUsage 仍写，对话归档静默跳过）。
func TestNonStreamFlowUnaffectedOnSQLite(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":5,"total_tokens":10}}`))
	}))
	defer srv.Close()

	g, d := setupGateway(t)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "oai", BaseURL: srv.URL, APIKey: "k", Format: "openai"})
	model.AddModel(d, uid, "gpt-4o", false, 0, 0)
	k, _ := model.CreateExtKey(d, "test", 0, 0)
	g.client = upstream.NewClient(http.DefaultClient)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"oai/gpt-4o","messages":[{"role":"user","content":"hi"}],"max_tokens":50}`))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	req.Header.Set("User-Agent", "claude-code/1.0")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"content":"ok"`) {
		t.Fatalf("missing content: %s", w.Body.String())
	}
	var n int
	if err := d.QueryRow("SELECT COUNT(*) FROM usage_records").Scan(&n); err != nil || n != 1 {
		t.Fatalf("usage_records count=%d err=%v, want 1", n, err)
	}
}

// 回归：SQLite 下一次流式请求照常工作（含 tee 安装的代码路径不破坏 SSE）。
func TestStreamFlowUnaffectedOnSQLite(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		w.Write([]byte("data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Hi\"}}]}\n\n"))
		f.Flush()
		w.Write([]byte("data: [DONE]\n\n"))
		f.Flush()
	}))
	defer srv.Close()

	g, d := setupGateway(t)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "oai", BaseURL: srv.URL, APIKey: "k", Format: "openai"})
	model.AddModel(d, uid, "gpt-4o", false, 0, 0)
	k, _ := model.CreateExtKey(d, "test", 0, 0)
	g.client = upstream.NewClient(http.DefaultClient)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"oai/gpt-4o","messages":[{"role":"user","content":"hi"}],"stream":true}`))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"content":"Hi"`) {
		t.Fatalf("missing stream content: %s", w.Body.String())
	}
	var n int
	if err := d.QueryRow("SELECT COUNT(*) FROM usage_records").Scan(&n); err != nil || n != 1 {
		t.Fatalf("usage_records count=%d err=%v, want 1", n, err)
	}
}
