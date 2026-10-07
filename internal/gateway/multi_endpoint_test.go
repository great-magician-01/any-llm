package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/great-magician-01/any-llm/internal/store"
	"github.com/great-magician-01/any-llm/internal/upstream"
)

// multiEndpointServer 同时挂 OpenAI 与 Anthropic 两个端点的 mock 上游，
// 记录每个端点收到的请求（路径 / 认证头 / 请求体），供断言路由与透传。
type multiEndpointServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []capturedRequest
}

type capturedRequest struct {
	path    string
	auth    string // Authorization 头
	xAPIKey string // x-api-key 头
	body    string
}

func newMultiEndpointServer(t *testing.T) *multiEndpointServer {
	t.Helper()
	s := &multiEndpointServer{}
	mux := http.NewServeMux()
	capture := func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.requests = append(s.requests, capturedRequest{
			path:    r.URL.Path,
			auth:    r.Header.Get("Authorization"),
			xAPIKey: r.Header.Get("x-api-key"),
			body:    string(body),
		})
		s.mu.Unlock()
	}
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		capture(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"c1","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"from openai ep"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`))
	})
	mux.HandleFunc("/anthropic/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		capture(w, r)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"m1","type":"message","role":"assistant","content":[{"type":"text","text":"from anthropic ep"}],"model":"m","stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":2}}`))
	})
	// anthropic 端点的流式变体
	mux.HandleFunc("/anthropic-stream/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		capture(w, r)
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"m\",\"usage\":{\"input_tokens\":3,\"output_tokens\":1}}}\n\n"))
		f.Flush()
		w.Write([]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"))
		f.Flush()
		w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hi\"}}\n\n"))
		f.Flush()
		w.Write([]byte("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"))
		f.Flush()
		w.Write([]byte("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\n"))
		f.Flush()
		w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
		f.Flush()
	})
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func (s *multiEndpointServer) captured() []capturedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]capturedRequest(nil), s.requests...)
}

func (s *multiEndpointServer) hits(path string) []capturedRequest {
	var out []capturedRequest
	for _, r := range s.captured() {
		if r.path == path {
			out = append(out, r)
		}
	}
	return out
}

func setupMultiEndpointGateway(t *testing.T, s *multiEndpointServer) (*Gateway, *store.ExtKey) {
	t.Helper()
	g, d := setupGateway(t)
	_, err := store.CreateUpstream(d, &store.Upstream{
		Name:    "ds",
		BaseURL: s.URL + "/v1",
		APIKey:  "sk-test",
		Format:  "openai",
		ExtraEndpoints: []store.UpstreamEndpoint{
			{Format: "anthropic", BaseURL: s.URL + "/anthropic"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	k, err := store.CreateExtKey(d, "test", "", 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	g.client = upstream.NewClient(http.DefaultClient)
	return g, k
}

// TestMultiEndpoint_AnthropicInboundHitsAnthropicEndpoint：入站 anthropic 端点
// 的请求命中上游配置的 anthropic 附加端点——原生直通（x-api-key 认证、
// anthropic 请求形状、anthropic 应答），usage 的 up_format 记 anthropic。
func TestMultiEndpoint_AnthropicInboundHitsAnthropicEndpoint(t *testing.T) {
	s := newMultiEndpointServer(t)
	g, k := setupMultiEndpointGateway(t, s)

	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"ds/m","max_tokens":50,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("x-api-key", k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "from anthropic ep") {
		t.Fatalf("body=%s", w.Body.String())
	}
	hits := s.hits("/anthropic/v1/messages")
	if len(hits) != 1 {
		t.Fatalf("anthropic endpoint hits=%d, all=%+v", len(hits), s.captured())
	}
	if hits[0].xAPIKey != "sk-test" || hits[0].auth != "" {
		t.Fatalf("anthropic endpoint should use x-api-key auth: %+v", hits[0])
	}
	// 直通而非转译：请求体保留 anthropic 形状（max_tokens + messages）
	if !strings.Contains(hits[0].body, `"max_tokens"`) || !strings.Contains(hits[0].body, `"messages"`) {
		t.Fatalf("upstream request not anthropic-shaped: %s", hits[0].body)
	}
	if len(s.hits("/v1/chat/completions")) != 0 {
		t.Fatal("openai endpoint should not be hit")
	}
}

// TestMultiEndpoint_OpenAIInboundUsesPrimary：入站 openai 端点走主格式端点
// （Bearer 认证），与单格式上游行为一致。
func TestMultiEndpoint_OpenAIInboundUsesPrimary(t *testing.T) {
	s := newMultiEndpointServer(t)
	g, k := setupMultiEndpointGateway(t, s)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"ds/m","messages":[{"role":"user","content":"hi"}],"max_tokens":50}`))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)

	if w.Code != 200 || !strings.Contains(w.Body.String(), "from openai ep") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	hits := s.hits("/v1/chat/completions")
	if len(hits) != 1 || hits[0].auth != "Bearer sk-test" {
		t.Fatalf("openai endpoint hits=%+v", hits)
	}
}

// TestMultiEndpoint_UnmatchedFormatFallsBackToTranslation：入站 responses 端点
// 没有对应附加端点 → 回落主格式（openai）做 IR 转译，usage 记 up_format=openai。
func TestMultiEndpoint_UnmatchedFormatFallsBackToTranslation(t *testing.T) {
	s := newMultiEndpointServer(t)
	g, k := setupMultiEndpointGateway(t, s)

	req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"ds/m","input":[{"role":"user","content":[{"type":"input_text","text":"hi"}]}]}`))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	// 出站被重编码为 responses 格式
	if !strings.Contains(w.Body.String(), `"object":"response"`) {
		t.Fatalf("not responses format: %s", w.Body.String())
	}
	// 打到的是主格式 openai 端点
	if len(s.hits("/v1/chat/completions")) != 1 {
		t.Fatalf("openai endpoint hits=%d, all=%+v", len(s.hits("/v1/chat/completions")), s.captured())
	}
	records, total, err := store.UsageRecordsList(g.db, 1, 10)
	if err != nil || total != 1 {
		t.Fatalf("usage records=%d err=%v", total, err)
	}
	if records[0].InFormat != "responses" || records[0].UpFormat != "openai" {
		t.Fatalf("formats=%+v, want in=responses up=openai (translated)", records[0])
	}
}

// TestMultiEndpoint_UsageRecordsEffectiveFormat：原生直通时 usage 与日志口径记
// 实际生效的上游格式（anthropic），而不是上游行的主格式。
func TestMultiEndpoint_UsageRecordsEffectiveFormat(t *testing.T) {
	s := newMultiEndpointServer(t)
	g, k := setupMultiEndpointGateway(t, s)

	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"ds/m","max_tokens":50,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("x-api-key", k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	records, total, err := store.UsageRecordsList(g.db, 1, 10)
	if err != nil || total != 1 {
		t.Fatalf("usage records=%d err=%v", total, err)
	}
	if records[0].InFormat != "anthropic" || records[0].UpFormat != "anthropic" {
		t.Fatalf("formats=%+v, want in=anthropic up=anthropic (native passthrough)", records[0])
	}
	if records[0].TotalTokens != 5 {
		t.Fatalf("tokens=%d, want 5 (3 in + 2 out from anthropic usage)", records[0].TotalTokens)
	}
}

// TestMultiEndpoint_StreamPassthrough：流式 anthropic 入站命中 anthropic 附加
// 端点，上游 SSE 透传重编码为 anthropic 出站流。
func TestMultiEndpoint_StreamPassthrough(t *testing.T) {
	s := newMultiEndpointServer(t)
	g, d := setupGateway(t)
	_, err := store.CreateUpstream(d, &store.Upstream{
		Name:    "ds",
		BaseURL: s.URL + "/v1",
		APIKey:  "sk-test",
		Format:  "openai",
		ExtraEndpoints: []store.UpstreamEndpoint{
			{Format: "anthropic", BaseURL: s.URL + "/anthropic-stream"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	k, _ := store.CreateExtKey(d, "test", "", 0, 0, nil)
	g.client = upstream.NewClient(http.DefaultClient)

	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"ds/m","max_tokens":50,"stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("x-api-key", k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "message_start") || !strings.Contains(body, "message_stop") {
		t.Fatalf("not an anthropic stream: %s", body)
	}
	if !strings.Contains(body, "Hi") {
		t.Fatalf("stream content missing: %s", body)
	}
	hits := s.hits("/anthropic-stream/v1/messages")
	if len(hits) != 1 || hits[0].xAPIKey != "sk-test" {
		t.Fatalf("stream should hit anthropic endpoint with x-api-key: %+v", hits)
	}
	if !strings.Contains(hits[0].body, `"stream":true`) {
		t.Fatalf("stream flag not forwarded: %s", hits[0].body)
	}
	records, total, _ := store.UsageRecordsList(d, 1, 10)
	if total != 1 || records[0].UpFormat != "anthropic" {
		t.Fatalf("usage=%+v (total=%d)", records, total)
	}
}

func TestEffectiveUpstream(t *testing.T) {
	u := &store.Upstream{
		Name: "ds", BaseURL: "https://x/v1", Format: "openai",
		ExtraEndpoints: []store.UpstreamEndpoint{{Format: "anthropic", BaseURL: "https://x/anthropic"}},
	}
	// 命中附加端点：换 BaseURL/Format，其余字段保留，原行不被改动
	eu := effectiveUpstream(u, "anthropic")
	if eu.Format != "anthropic" || eu.BaseURL != "https://x/anthropic" || eu.Name != "ds" {
		t.Fatalf("effective=%+v", eu)
	}
	if u.Format != "openai" || u.BaseURL != "https://x/v1" {
		t.Fatalf("original mutated: %+v", u)
	}
	// 主格式恰好匹配 / 未命中：原样返回
	if got := effectiveUpstream(u, "openai"); got != u {
		t.Fatalf("primary match should return the same row")
	}
	if got := effectiveUpstream(u, "responses"); got != u {
		t.Fatalf("unmatched should fall back to the same row")
	}
	// 无附加端点：任何入站格式都原样
	plain := &store.Upstream{BaseURL: "https://x", Format: "openai"}
	if got := effectiveUpstream(plain, "anthropic"); got != plain {
		t.Fatalf("no extras should return the same row")
	}
	// JSON 序列化形状固定（admin API 与配置导出共用）
	b, err := json.Marshal(store.UpstreamEndpoint{Format: "anthropic", BaseURL: "https://x"})
	if err != nil || string(b) != `{"format":"anthropic","base_url":"https://x"}` {
		t.Fatalf("endpoint json=%s err=%v", b, err)
	}
}
