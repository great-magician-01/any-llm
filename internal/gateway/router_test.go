package gateway

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/great-magician-01/any-llm/internal/db"
	"github.com/great-magician-01/any-llm/internal/model"
	"github.com/great-magician-01/any-llm/internal/upstream"
)

func setupGateway(t *testing.T) (*Gateway, *sql.DB) {
	t.Helper()
	d, err := db.OpenSQLite(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	g := New(d, nil, nil)
	return g, d
}

func TestModelsEndpoint(t *testing.T) {
	g, d := setupGateway(t)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "my-openai", BaseURL: "b", APIKey: "k", Format: "openai"})
	model.AddModel(d, uid, "gpt-4o", false, 0, 0)
	model.AddModel(d, uid, "gpt-4o-mini", false, 0, 0)

	req := httptest.NewRequest("GET", "/v1/models", nil)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status=%d", w.Code)
	}
	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Data) != 2 {
		t.Fatalf("models=%d", len(resp.Data))
	}
	ids := map[string]bool{}
	for _, m := range resp.Data {
		ids[m.ID] = true
	}
	if !ids["my-openai/gpt-4o"] || !ids["my-openai/gpt-4o-mini"] {
		t.Fatalf("model ids=%+v", ids)
	}
}

func TestAuthMissingKey(t *testing.T) {
	g, _ := setupGateway(t)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"x/y","messages":[]}`))
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("status=%d want 401", w.Code)
	}
}

func TestAuthInvalidKey(t *testing.T) {
	g, d := setupGateway(t)
	model.CreateExtKey(d, "l", 0, 0)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"x/y","messages":[]}`))
	req.Header.Set("Authorization", "Bearer all-sk-invalid")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("status=%d want 401", w.Code)
	}
}

func TestRouteModelNotFound(t *testing.T) {
	g, d := setupGateway(t)
	k, _ := model.CreateExtKey(d, "l", 0, 0)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"nonexistent/model","messages":[]}`))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatalf("status=%d want 404", w.Code)
	}
}

func TestRouteInvalidModelFormat(t *testing.T) {
	g, d := setupGateway(t)
	k, _ := model.CreateExtKey(d, "l", 0, 0)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"nomodelslash","messages":[]}`))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("status=%d want 400", w.Code)
	}
}

// /v1/responses 走 responses 入站格式：无 key 401、错误形状与 openai 一致
func TestResponsesRoute(t *testing.T) {
	gw, _ := setupGateway(t) // router_test.go 的现有辅助：(*Gateway, *sql.DB)
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"x/y"}`)))
	if rec.Code != 401 {
		t.Fatalf("status=%d want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"error"`) {
		t.Fatalf("error shape: %s", rec.Body.String())
	}
}

func TestExtKeyDailyTokenLimitExceeded(t *testing.T) {
	g, d := setupGateway(t)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "oai", BaseURL: "b", APIKey: "k", Format: "openai"})
	model.AddModel(d, uid, "gpt-4o", false, 0, 0)
	// key with daily limit of 100, already used 100
	k, _ := model.CreateExtKey(d, "l", 100, 0)
	model.InsertUsage(d, &model.UsageRecord{
		ExtKeyID: &k.ID, UpstreamID: &uid, UpstreamName: "oai", Model: "gpt-4o",
		InFormat: "openai", UpFormat: "openai", TotalTokens: 100, Status: "ok",
	})
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"oai/gpt-4o","messages":[]}`))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != 429 {
		t.Fatalf("status=%d want 429, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "rate_limit_error") {
		t.Fatalf("body=%s", w.Body.String())
	}
}

func TestExtKeyMonthlyTokenLimitExceeded(t *testing.T) {
	g, d := setupGateway(t)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "oai", BaseURL: "b", APIKey: "k", Format: "openai"})
	model.AddModel(d, uid, "gpt-4o", false, 0, 0)
	k, _ := model.CreateExtKey(d, "l", 0, 50)
	model.InsertUsage(d, &model.UsageRecord{
		ExtKeyID: &k.ID, UpstreamID: &uid, UpstreamName: "oai", Model: "gpt-4o",
		InFormat: "openai", UpFormat: "openai", TotalTokens: 50, Status: "ok",
	})
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"oai/gpt-4o","messages":[]}`))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != 429 {
		t.Fatalf("status=%d want 429", w.Code)
	}
}

func TestUpstreamDailyTokenLimitExceeded(t *testing.T) {
	g, d := setupGateway(t)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "oai", BaseURL: "b", APIKey: "k", Format: "openai", DailyTokenLimit: 100})
	model.AddModel(d, uid, "gpt-4o", false, 0, 0)
	k, _ := model.CreateExtKey(d, "l", 0, 0)
	model.InsertUsage(d, &model.UsageRecord{
		ExtKeyID: &k.ID, UpstreamID: &uid, UpstreamName: "oai", Model: "gpt-4o",
		InFormat: "openai", UpFormat: "openai", TotalTokens: 100, Status: "ok",
	})
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"oai/gpt-4o","messages":[]}`))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != 429 {
		t.Fatalf("status=%d want 429", w.Code)
	}
}

func TestTokenLimitNotExceeded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":5,"total_tokens":10}}`))
	}))
	defer srv.Close()

	g, d := setupGateway(t)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "oai", BaseURL: srv.URL, APIKey: "k", Format: "openai", DailyTokenLimit: 1000})
	model.AddModel(d, uid, "gpt-4o", false, 0, 0)
	k, _ := model.CreateExtKey(d, "l", 1000, 5000)
	model.InsertUsage(d, &model.UsageRecord{
		ExtKeyID: &k.ID, UpstreamID: &uid, UpstreamName: "oai", Model: "gpt-4o",
		InFormat: "openai", UpFormat: "openai", TotalTokens: 50, Status: "ok",
	})
	g.client = upstream.NewClient(http.DefaultClient)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"oai/gpt-4o","messages":[{"role":"user","content":"hi"}],"max_tokens":50}`))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code == 429 {
		t.Fatalf("should not be rate limited, got 429 body=%s", w.Body.String())
	}
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
