package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/great-magician-01/any-llm/internal/store"
	"github.com/great-magician-01/any-llm/internal/upstream"
)

// hasDataFrame 报告 body 里是否存在真正的 SSE data: 帧（用来区分「合法流」与
// 「裸 JSON 被塞进 SSE 响应」——后者严格 SDK 会整行丢弃）。
func hasDataFrame(body string) bool {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "data: {") {
			return true
		}
	}
	return false
}

// 业务规则：上游无视 stream:true、直接用非流式 JSON 应答时，客户端必须拿到**完整
// 内容**，而不是一个空的 200 SSE 流。
//
// 回归位：CLAUDE.md 写着「上游用非流式 JSON 应答流式请求时按完整响应转发
// （result.Response != nil 分支）」，但 upstream.Client.Call 只在非流式请求时填
// result.Response，而 handleStream 只在流式请求时执行——那个分支实际上是死代码。
// 实测：openai 客户端拿到空 body（无内容、无 [DONE]），usage 记 ok/0 token，
// 会话里存的也是空内容。既有 TestResponsesStreamFallbackNonStreamJSON 只看
// responses 形状与 id 能否续接，所以内容丢失完全不可见。
func TestStreamNonStreamJSONUpstreamForwardsContent(t *testing.T) {
	const upstreamJSON = `{"id":"c1","model":"m",` +
		`"choices":[{"index":0,"message":{"role":"assistant","content":"Hi there"},"finish_reason":"stop"}],` +
		`"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`

	newUpstream := func(t *testing.T) *httptest.Server {
		t.Helper()
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 明确的 JSON Content-Type：模拟「无视 stream 参数」的兼容层。
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(upstreamJSON))
		}))
	}

	t.Run("openai 客户端", func(t *testing.T) {
		srv := newUpstream(t)
		defer srv.Close()

		g, d := setupGateway(t)
		uid, _ := store.CreateUpstream(d, &store.Upstream{Name: "mock", BaseURL: srv.URL, APIKey: "sk", Format: "openai"})
		store.AddModel(d, uid, store.UpstreamModel{ModelName: "m"})
		k, _ := store.CreateExtKey(d, "fallback", "", 0, 0, nil)
		g.client = upstream.NewClient(http.DefaultClient)

		req := httptest.NewRequest("POST", "/v1/chat/completions",
			strings.NewReader(`{"model":"mock/m","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
		req.Header.Set("Authorization", "Bearer "+k.Key)
		w := httptest.NewRecorder()
		g.ServeHTTP(w, req)

		if w.Code != 200 {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), `"content":"Hi there"`) {
			t.Fatalf("客户端没拿到上游内容（空流回归）：%q", w.Body.String())
		}
		// 必须是合法 SSE：内容走 data: 帧，且以 [DONE] 收尾。
		if !hasDataFrame(w.Body.String()) {
			t.Errorf("body 不是 SSE data: 帧：%q", w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "[DONE]") {
			t.Errorf("缺少 [DONE] 结束帧：%q", w.Body.String())
		}

		records, _, err := store.UsageRecordsList(d, 1, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(records) != 1 {
			t.Fatalf("usage records=%d want 1", len(records))
		}
		if records[0].PromptTokens != 10 || records[0].CompletionTokens != 5 {
			t.Errorf("usage=%d/%d want 10/5（不能记成 0 token）", records[0].PromptTokens, records[0].CompletionTokens)
		}
		if records[0].Status != "ok" {
			t.Errorf("status=%q want ok", records[0].Status)
		}
	})

	t.Run("responses 客户端仍可续接且带内容", func(t *testing.T) {
		srv := newUpstream(t)
		defer srv.Close()

		g, d := setupGateway(t)
		uid, _ := store.CreateUpstream(d, &store.Upstream{Name: "mock", BaseURL: srv.URL, APIKey: "sk", Format: "openai"})
		store.AddModel(d, uid, store.UpstreamModel{ModelName: "m"})
		k, _ := store.CreateExtKey(d, "fallback-rsp", "", 0, 0, nil)
		g.client = upstream.NewClient(http.DefaultClient)

		req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(
			`{"model":"mock/m","stream":true,"store":true,"input":[{"role":"user","content":[{"type":"input_text","text":"hi"}]}]}`))
		req.Header.Set("Authorization", "Bearer "+k.Key)
		w := httptest.NewRecorder()
		g.ServeHTTP(w, req)

		if w.Code != 200 {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		body := w.Body.String()
		if !strings.Contains(body, "Hi there") {
			t.Fatalf("responses 客户端没拿到内容：%q", body)
		}
		if !strings.Contains(body, "event: response.completed") {
			t.Errorf("缺少 response.completed 结束帧：%q", body)
		}
		// 响应 id 必须被改写成会话 key（否则 previous_response_id 续接会 400）。
		var id string
		for _, line := range strings.Split(body, "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var ev struct {
				Response *struct {
					ID string `json:"id"`
				} `json:"response"`
			}
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err == nil &&
				ev.Response != nil && ev.Response.ID != "" {
				id = ev.Response.ID
				break
			}
		}
		if !strings.HasPrefix(id, "resp_") {
			t.Fatalf("响应 id=%q，期望被改写成会话 key（resp_ 前缀），body=%q", id, body)
		}

		// 用这个 id 续接必须 200（会话里存的是真实内容而不是空）。
		req2 := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(fmt.Sprintf(
			`{"model":"mock/m","previous_response_id":"%s","input":[{"role":"user","content":[{"type":"input_text","text":"more"}]}]}`, id)))
		req2.Header.Set("Authorization", "Bearer "+k.Key)
		w2 := httptest.NewRecorder()
		g.ServeHTTP(w2, req2)
		if w2.Code != 200 {
			t.Fatalf("续接 status=%d body=%s", w2.Code, w2.Body.String())
		}
	})
}

// 反向保护：Content-Type 缺失或非标准的上游仍必须走流式解析（不能因为头不规范
// 就把真正的 SSE 流当 JSON 缓冲掉——那会让正常流式退化成「等到结束才响应」）。
func TestStreamDetectsNonStreamJSONByContentTypeOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 故意不设 Content-Type：net/http 会嗅探成 text/plain; charset=utf-8。
		f := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"id\":\"c1\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Hi\"}}]}\n\n"))
		f.Flush()
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		f.Flush()
	}))
	defer srv.Close()

	g, d := setupGateway(t)
	uid, _ := store.CreateUpstream(d, &store.Upstream{Name: "plain", BaseURL: srv.URL, APIKey: "sk", Format: "openai"})
	store.AddModel(d, uid, store.UpstreamModel{ModelName: "m"})
	k, _ := store.CreateExtKey(d, "plain-ct", "", 0, 0, nil)
	g.client = upstream.NewClient(http.DefaultClient)

	req := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"plain/m","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status=%d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"content":"Hi"`) {
		t.Fatalf("SSE 流被当成 JSON 处理了：%q", body)
	}
	if !strings.Contains(body, "[DONE]") {
		t.Errorf("缺少 [DONE]：%q", body)
	}
}
