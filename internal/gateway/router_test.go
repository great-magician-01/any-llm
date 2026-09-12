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
	k, _ := model.CreateExtKey(d, "l", 0, 0, nil)

	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+k.Key)
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

// /v1/models 强制 ext key 鉴权：无 key / 无效 key → 401。
func TestModelsEndpointRequiresKey(t *testing.T) {
	g, _ := setupGateway(t)

	// 无 key
	req := httptest.NewRequest("GET", "/v1/models", nil)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("no key status=%d want 401, body=%s", w.Code, w.Body.String())
	}

	// 无效 key
	req = httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer all-sk-invalid")
	w = httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("invalid key status=%d want 401, body=%s", w.Code, w.Body.String())
	}
}

// /v1/models 按 key 白名单过滤：受限 key 只看到已列的模型与别名。
func TestModelsEndpointFilteredByAllowedModels(t *testing.T) {
	g, d := setupGateway(t)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "oai", BaseURL: "b", APIKey: "k", Format: "openai"})
	model.AddModel(d, uid, "gpt-4o", false, 0, 0)
	model.AddModel(d, uid, "gpt-4o-mini", false, 0, 0)
	model.CreateAlias(d, &model.ModelAlias{Name: "fast", Bindings: []model.AliasBinding{{UpstreamID: uid, ModelName: "gpt-4o"}}})
	k, _ := model.CreateExtKey(d, "restricted", 0, 0, []string{"oai/gpt-4o", "fast"})

	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	ids := map[string]bool{}
	for _, m := range resp.Data {
		ids[m.ID] = true
	}
	if !ids["oai/gpt-4o"] || !ids["fast"] {
		t.Fatalf("allowed entries missing: %+v", ids)
	}
	if ids["oai/gpt-4o-mini"] {
		t.Fatalf("unlisted model leaked: %+v", ids)
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
	model.CreateExtKey(d, "l", 0, 0, nil)
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
	k, _ := model.CreateExtKey(d, "l", 0, 0, nil)
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
	k, _ := model.CreateExtKey(d, "l", 0, 0, nil)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"nomodelslash","messages":[]}`))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("status=%d want 400", w.Code)
	}
}

// 直连请求禁用的上游 → 404，错误消息明确说明已禁用（区别于 not found）。
func TestRouteDisabledUpstream(t *testing.T) {
	g, d := setupGateway(t)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "off", BaseURL: "b", APIKey: "k", Format: "openai"})
	u, _ := model.GetUpstreamByID(d, uid)
	u.Enabled = false
	if err := model.UpdateUpstream(d, u); err != nil {
		t.Fatal(err)
	}
	k, _ := model.CreateExtKey(d, "l", 0, 0, nil)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"off/m","messages":[]}`))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatalf("status=%d want 404, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "is disabled") {
		t.Fatalf("body=%s", w.Body.String())
	}
}

// /v1/models 不列出禁用上游的模型，也不列出仅指向禁用上游的别名。
func TestModelsEndpointExcludesDisabled(t *testing.T) {
	g, d := setupGateway(t)
	off, _ := model.CreateUpstream(d, &model.Upstream{Name: "off", BaseURL: "b", APIKey: "k", Format: "openai"})
	on, _ := model.CreateUpstream(d, &model.Upstream{Name: "on", BaseURL: "b", APIKey: "k", Format: "openai"})
	model.AddModel(d, off, "m1", false, 0, 0)
	model.AddModel(d, on, "m2", false, 0, 0)
	model.CreateAlias(d, &model.ModelAlias{Name: "dead-alias", Bindings: []model.AliasBinding{{UpstreamID: off, ModelName: "m1"}}})
	model.CreateAlias(d, &model.ModelAlias{Name: "live-alias", Bindings: []model.AliasBinding{{UpstreamID: on, ModelName: "m2"}}})

	u, _ := model.GetUpstreamByID(d, off)
	u.Enabled = false
	if err := model.UpdateUpstream(d, u); err != nil {
		t.Fatal(err)
	}
	k, _ := model.CreateExtKey(d, "l", 0, 0, nil)

	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+k.Key)
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
	ids := map[string]bool{}
	for _, m := range resp.Data {
		ids[m.ID] = true
	}
	if ids["off/m1"] || ids["dead-alias"] {
		t.Fatalf("disabled upstream leaked to /v1/models: %+v", ids)
	}
	if !ids["on/m2"] || !ids["live-alias"] {
		t.Fatalf("enabled upstream missing: %+v", ids)
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
	k, _ := model.CreateExtKey(d, "l", 100, 0, nil)
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
	k, _ := model.CreateExtKey(d, "l", 0, 50, nil)
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
	k, _ := model.CreateExtKey(d, "l", 0, 0, nil)
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
	k, _ := model.CreateExtKey(d, "l", 1000, 5000, nil)
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

// 受限 key 请求未列模型 → 403 permission_error；openai 与 anthropic 两种
// 入站格式都生效；缺 model 字段的畸形请求仍走后续 400，不误报 403。
func TestModelNotAllowedForRestrictedKey(t *testing.T) {
	g, d := setupGateway(t)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "oai", BaseURL: "b", APIKey: "k", Format: "openai"})
	model.AddModel(d, uid, "gpt-4o", false, 0, 0)
	model.AddModel(d, uid, "gpt-4o-mini", false, 0, 0)
	k, _ := model.CreateExtKey(d, "restricted", 0, 0, []string{"oai/gpt-4o"})

	for _, tc := range []struct{ path, body string }{
		{"/v1/chat/completions", `{"model":"oai/gpt-4o-mini","messages":[]}`},
		{"/v1/messages", `{"model":"oai/gpt-4o-mini","messages":[]}`},
	} {
		req := httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
		req.Header.Set("Authorization", "Bearer "+k.Key)
		w := httptest.NewRecorder()
		g.ServeHTTP(w, req)
		if w.Code != 403 {
			t.Fatalf("%s status=%d want 403, body=%s", tc.path, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "permission_error") {
			t.Fatalf("%s error type: %s", tc.path, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "not allowed") {
			t.Fatalf("%s message: %s", tc.path, w.Body.String())
		}
	}

	// 缺 model 字段：留给后续 400
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[]}`))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("missing model status=%d want 400, body=%s", w.Code, w.Body.String())
	}
}

// 受限 key 请求已列模型 → 正常放行（假上游）。
func TestModelAllowedForRestrictedKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":5,"total_tokens":10}}`))
	}))
	defer srv.Close()

	g, d := setupGateway(t)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "oai", BaseURL: srv.URL, APIKey: "k", Format: "openai"})
	model.AddModel(d, uid, "gpt-4o", false, 0, 0)
	k, _ := model.CreateExtKey(d, "restricted", 0, 0, []string{"oai/gpt-4o"})
	g.client = upstream.NewClient(http.DefaultClient)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"oai/gpt-4o","messages":[{"role":"user","content":"hi"}],"max_tokens":50}`))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

// 白名单条目是对外名：key 限定某个别名后，直连名即使指向同一模型也被拒；
// 反之 key 限定直连名后，别名也得列入才可用。
func TestAllowedModelsMatchPublicName(t *testing.T) {
	g, d := setupGateway(t)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "oai", BaseURL: "b", APIKey: "k", Format: "openai"})
	model.AddModel(d, uid, "gpt-4o", false, 0, 0)
	model.CreateAlias(d, &model.ModelAlias{Name: "fast", Bindings: []model.AliasBinding{{UpstreamID: uid, ModelName: "gpt-4o"}}})

	for _, tc := range []struct{ allow, model string }{
		{"fast", "oai/gpt-4o"}, // 只列别名 → 直连名被拒
		{"oai/gpt-4o", "fast"}, // 只列直连名 → 别名被拒
	} {
		k, _ := model.CreateExtKey(d, "l", 0, 0, []string{tc.allow})
		req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"`+tc.model+`","messages":[]}`))
		req.Header.Set("Authorization", "Bearer "+k.Key)
		w := httptest.NewRecorder()
		g.ServeHTTP(w, req)
		if w.Code != 403 {
			t.Fatalf("allow=%q model=%q status=%d want 403, body=%s", tc.allow, tc.model, w.Code, w.Body.String())
		}
	}
}
