package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/great-magician-01/any-llm/internal/model"
	"github.com/great-magician-01/any-llm/internal/upstream"
)

// TestCompletion_StreamCrossFormat_OAIin_ANTup: OpenAI client streams from an
// Anthropic-format upstream. The gateway must translate Anthropic SSE events
// into OpenAI chat.completion.chunk frames and emit [DONE].
func TestCompletion_StreamCrossFormat_OAIin_ANTup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-3-5\",\"content\":[],\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":10,\"output_tokens\":1}}}\n\n"))
		f.Flush()
		w.Write([]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"))
		f.Flush()
		w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hi\"}}\n\n"))
		f.Flush()
		w.Write([]byte("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"))
		f.Flush()
		w.Write([]byte("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":2}}\n\n"))
		f.Flush()
		w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
		f.Flush()
	}))
	defer srv.Close()

	g, d := setupGateway(t)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "ant", BaseURL: srv.URL, APIKey: "sk-ant", Format: "anthropic"})
	model.AddModel(d, uid, "claude-3-5", false, 0, 0)
	k, _ := model.CreateExtKey(d, "test", 0, 0)
	g.client = upstream.NewClient(http.DefaultClient)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"ant/claude-3-5","messages":[{"role":"user","content":"hi"}],"max_tokens":50,"stream":true}`))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)

	body := w.Body.String()
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, body)
	}
	if !strings.Contains(body, `"content":"Hi"`) {
		t.Fatalf("missing content delta: %s", body)
	}
	if !strings.Contains(body, "[DONE]") {
		t.Fatalf("missing [DONE]: %s", body)
	}
	if !strings.Contains(body, `"finish_reason":"stop"`) {
		t.Fatalf("missing finish_reason: %s", body)
	}
	// usage should be emitted in the message_delta chunk
	if !strings.Contains(body, `"prompt_tokens":10`) {
		t.Fatalf("missing prompt_tokens=10 in usage: %s", body)
	}
	records, _, _ := model.UsageRecordsList(d, 1, 10)
	if len(records) != 1 {
		t.Fatalf("records=%d", len(records))
	}
	if records[0].PromptTokens != 10 || records[0].CompletionTokens != 2 {
		t.Fatalf("recorded tokens=%+v", records[0])
	}
}

// TestCompletion_StreamSSE_NoSpaceAfterColon verifies the SSE parser accepts
// "data:{...}" (no space after colon), which some upstreams emit. Per the SSE
// spec, the field value is everything after the first colon with one optional
// leading space stripped.
func TestCompletion_StreamSSE_NoSpaceAfterColon(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		w.Write([]byte("data:{\"id\":\"c1\",\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Hi\"}}]}\n\n"))
		f.Flush()
		w.Write([]byte("data:{\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\n"))
		f.Flush()
		w.Write([]byte("data:[DONE]\n\n"))
		f.Flush()
	}))
	defer srv.Close()

	g, d := setupGateway(t)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "oai", BaseURL: srv.URL, APIKey: "sk-test", Format: "openai"})
	model.AddModel(d, uid, "gpt-4o", false, 0, 0)
	k, _ := model.CreateExtKey(d, "test", 0, 0)
	g.client = upstream.NewClient(http.DefaultClient)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"oai/gpt-4o","messages":[{"role":"user","content":"hi"}],"max_tokens":50,"stream":true}`))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)

	body := w.Body.String()
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, body)
	}
	if !strings.Contains(body, `"content":"Hi"`) {
		t.Fatalf("missing content (data: without space not parsed): %s", body)
	}
	if !strings.Contains(body, "[DONE]") {
		t.Fatalf("missing [DONE]: %s", body)
	}
	records, _, _ := model.UsageRecordsList(d, 1, 10)
	if len(records) != 1 || records[0].TotalTokens != 5 {
		t.Fatalf("records=%+v", records)
	}
}

// TestCompletion_StreamCrossFormat_ANTin_OAIup: Anthropic client streams from
// an OpenAI-format upstream. The gateway must translate OpenAI chunks into
// Anthropic SSE events.
func TestCompletion_StreamCrossFormat_ANTin_OAIup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		w.Write([]byte("data: {\"id\":\"c1\",\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Hi\"}}]}\n\n"))
		f.Flush()
		w.Write([]byte("data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		f.Flush()
		w.Write([]byte("data: {\"id\":\"c1\",\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\n"))
		f.Flush()
		w.Write([]byte("data: [DONE]\n\n"))
		f.Flush()
	}))
	defer srv.Close()

	g, d := setupGateway(t)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "oai", BaseURL: srv.URL, APIKey: "sk-test", Format: "openai"})
	model.AddModel(d, uid, "gpt-4o", false, 0, 0)
	k, _ := model.CreateExtKey(d, "test", 0, 0)
	g.client = upstream.NewClient(http.DefaultClient)

	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"oai/gpt-4o","max_tokens":50,"messages":[{"role":"user","content":"hi"}],"stream":true}`))
	req.Header.Set("x-api-key", k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)

	body := w.Body.String()
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, body)
	}
	if !strings.Contains(body, "event: message_start") {
		t.Fatalf("missing message_start: %s", body)
	}
	if !strings.Contains(body, "event: content_block_delta") {
		t.Fatalf("missing content_block_delta: %s", body)
	}
	if !strings.Contains(body, `"text":"Hi"`) {
		t.Fatalf("missing text delta: %s", body)
	}
	if !strings.Contains(body, "event: message_stop") {
		t.Fatalf("missing message_stop: %s", body)
	}
	if !strings.Contains(body, `"stop_reason":"end_turn"`) {
		t.Fatalf("missing stop_reason end_turn: %s", body)
	}
	records, _, _ := model.UsageRecordsList(d, 1, 10)
	if len(records) != 1 {
		t.Fatalf("records=%d", len(records))
	}
	if records[0].PromptTokens != 3 || records[0].CompletionTokens != 2 {
		t.Fatalf("recorded tokens=%+v", records[0])
	}
}

// TestCompletion_StreamCrossFormat_ANTin_OAIup_ToolOnly verifies that when an
// OpenAI-format upstream streams a tool-only turn (no text deltas), the
// gateway's Anthropic output starts content_block indices at 0. The OpenAI
// StreamDecoder reserves IR index 0 for a never-opened text block, so the
// first tool_use block arrives at IR index 1; without index rewriting the
// Anthropic client would see a stream whose first content_block_start has
// index 1 (no index 0), which deviates from the Anthropic streaming spec.
func TestCompletion_StreamCrossFormat_ANTin_OAIup_ToolOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		w.Write([]byte(`data: {"id":"c1","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}` + "\n\n"))
		f.Flush()
		w.Write([]byte(`data: {"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":""}}]}}]}` + "\n\n"))
		f.Flush()
		w.Write([]byte(`data: {"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":\"SF\"}"}}]}}]}` + "\n\n"))
		f.Flush()
		w.Write([]byte(`data: {"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n"))
		f.Flush()
		w.Write([]byte("data: [DONE]\n\n"))
		f.Flush()
	}))
	defer srv.Close()

	g, d := setupGateway(t)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "oai", BaseURL: srv.URL, APIKey: "sk-test", Format: "openai"})
	model.AddModel(d, uid, "gpt-4o", false, 0, 0)
	k, _ := model.CreateExtKey(d, "test", 0, 0)
	g.client = upstream.NewClient(http.DefaultClient)

	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"oai/gpt-4o","max_tokens":50,"messages":[{"role":"user","content":"weather?"}],"stream":true}`))
	req.Header.Set("x-api-key", k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)

	body := w.Body.String()
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, body)
	}
	if !strings.Contains(body, "event: content_block_start") {
		t.Fatalf("missing content_block_start: %s", body)
	}
	// the (only) tool_use block must be at index 0, not 1
	if !strings.Contains(body, `"index":0`) {
		t.Fatalf("missing index 0 for first content_block (should be rewritten from 1): %s", body)
	}
	if strings.Contains(body, `"index":1`) {
		t.Fatalf("index 1 should not appear (tool-only turn must start at 0): %s", body)
	}
	// payload sanity: the tool_use id/name survived the rewrite
	if !strings.Contains(body, `"id":"call_1"`) || !strings.Contains(body, `"name":"get_weather"`) {
		t.Fatalf("tool_use payload lost: %s", body)
	}
}
