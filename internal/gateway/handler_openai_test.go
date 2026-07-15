package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/great-magician-01/any-llm/internal/model"
	"github.com/great-magician-01/any-llm/internal/upstream"
)

func TestCompletion_NonStreamOpenAI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"Hello!"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`))
	}))
	defer srv.Close()

	g, d := setupGateway(t)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "oai", BaseURL: srv.URL, APIKey: "sk-test", Format: "openai"})
	model.AddModel(d, uid, "gpt-4o", false)
	k, _ := model.CreateExtKey(d, "test")
	g.client = upstream.NewClient(http.DefaultClient)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"oai/gpt-4o","messages":[{"role":"user","content":"hi"}],"max_tokens":50}`))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Hello!") {
		t.Fatalf("body=%s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "usage") {
		t.Fatalf("no usage in response: %s", w.Body.String())
	}

	records, total, _ := model.UsageRecordsList(d, 1, 10)
	if total != 1 {
		t.Fatalf("usage records=%d", total)
	}
	if records[0].TotalTokens != 15 {
		t.Fatalf("recorded tokens=%d", records[0].TotalTokens)
	}
}

func TestCompletion_StreamOpenAI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		w.Write([]byte("data: {\"id\":\"c1\",\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Hi\"}}]}\n\n"))
		f.Flush()
		w.Write([]byte("data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\n"))
		f.Flush()
		w.Write([]byte("data: [DONE]\n\n"))
		f.Flush()
	}))
	defer srv.Close()

	g, d := setupGateway(t)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "oai", BaseURL: srv.URL, APIKey: "sk-test", Format: "openai"})
	model.AddModel(d, uid, "gpt-4o", false)
	k, _ := model.CreateExtKey(d, "test")
	g.client = upstream.NewClient(http.DefaultClient)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"oai/gpt-4o","messages":[{"role":"user","content":"hi"}],"max_tokens":50,"stream":true}`))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status=%d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "data: ") {
		t.Fatalf("no SSE data: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "[DONE]") {
		t.Fatalf("no [DONE]: %s", w.Body.String())
	}
	records, total, _ := model.UsageRecordsList(d, 1, 10)
	if total != 1 {
		t.Fatalf("usage records=%d", total)
	}
	if records[0].TotalTokens != 5 {
		t.Fatalf("recorded tokens=%d", records[0].TotalTokens)
	}
	if !records[0].Stream {
		t.Fatal("stream flag not set")
	}
}

func TestCompletion_CrossFormat_AnthropicInOpenAIUp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"cross-format works"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":4,"total_tokens":11}}`))
	}))
	defer srv.Close()

	g, d := setupGateway(t)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "oai", BaseURL: srv.URL, APIKey: "sk-test", Format: "openai"})
	model.AddModel(d, uid, "gpt-4o", false)
	k, _ := model.CreateExtKey(d, "test")
	g.client = upstream.NewClient(http.DefaultClient)

	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"oai/gpt-4o","max_tokens":50,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("x-api-key", k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "cross-format works") {
		t.Fatalf("body=%s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"type":"message"`) {
		t.Fatalf("not anthropic format: %s", w.Body.String())
	}
	records, _, _ := model.UsageRecordsList(d, 1, 10)
	if records[0].InFormat != "anthropic" || records[0].UpFormat != "openai" {
		t.Fatalf("formats=%+v", records[0])
	}
}
