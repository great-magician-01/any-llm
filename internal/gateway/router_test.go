package gateway

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/any-llm/internal/db"
	"github.com/great-magician-01/any-llm/internal/model"
	"github.com/great-magician-01/any-llm/internal/upstream"
)

func setupGateway(t *testing.T) (*Gateway, *sql.DB) {
	t.Helper()
	// 配置读缓存是 model 包级、进程内的，而每个用例都是一个临时库：不清就会把
	// 上一个用例的上游/别名条目带到本用例（同名即遮蔽）。
	model.ResetConfigCache()
	t.Cleanup(model.ResetConfigCache)
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
	model.AddModel(d, uid, "gpt-4o", false, 0, 0, false)
	model.AddModel(d, uid, "gpt-4o-mini", false, 0, 0, false)
	k, _ := model.CreateExtKey(d, "l", "", 0, 0, nil)

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
	model.AddModel(d, uid, "gpt-4o", false, 0, 0, false)
	model.AddModel(d, uid, "gpt-4o-mini", false, 0, 0, false)
	model.CreateAlias(d, &model.ModelAlias{Name: "fast", Bindings: []model.AliasBinding{{UpstreamID: uid, ModelName: "gpt-4o"}}})
	k, _ := model.CreateExtKey(d, "restricted", "", 0, 0, []string{"oai/gpt-4o", "fast"})

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
	model.CreateExtKey(d, "l", "", 0, 0, nil)
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
	k, _ := model.CreateExtKey(d, "l", "", 0, 0, nil)
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
	k, _ := model.CreateExtKey(d, "l", "", 0, 0, nil)
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
	k, _ := model.CreateExtKey(d, "l", "", 0, 0, nil)
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
	model.AddModel(d, off, "m1", false, 0, 0, false)
	model.AddModel(d, on, "m2", false, 0, 0, false)
	model.CreateAlias(d, &model.ModelAlias{Name: "dead-alias", Bindings: []model.AliasBinding{{UpstreamID: off, ModelName: "m1"}}})
	model.CreateAlias(d, &model.ModelAlias{Name: "live-alias", Bindings: []model.AliasBinding{{UpstreamID: on, ModelName: "m2"}}})

	u, _ := model.GetUpstreamByID(d, off)
	u.Enabled = false
	if err := model.UpdateUpstream(d, u); err != nil {
		t.Fatal(err)
	}
	k, _ := model.CreateExtKey(d, "l", "", 0, 0, nil)

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

// 直连请求已过有效期的上游 → 404，与禁用分开报（该去续期，不是去点启用）。
func TestRouteExpiredUpstream(t *testing.T) {
	g, d := setupGateway(t)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "old", BaseURL: "b", APIKey: "k", Format: "openai"})
	u, _ := model.GetUpstreamByID(d, uid)
	// 仍启用，只是有效期已过——两个维度必须互相独立
	at := time.Now().Add(-time.Hour).Truncate(time.Second)
	u.ExpiresAt = &at
	if err := model.UpdateUpstream(d, u); err != nil {
		t.Fatal(err)
	}
	k, _ := model.CreateExtKey(d, "l", "", 0, 0, nil)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"old/m","messages":[]}`))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatalf("status=%d want 404, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "is expired") {
		t.Fatalf("body=%s", w.Body.String())
	}
}

// /v1/models 同样不列出已过期上游的模型与别名；到期判定独立于 enabled
// （上游仍处于启用状态，只是有效期过了）。
func TestModelsEndpointExcludesExpired(t *testing.T) {
	g, d := setupGateway(t)
	old, _ := model.CreateUpstream(d, &model.Upstream{Name: "old", BaseURL: "b", APIKey: "k", Format: "openai"})
	on, _ := model.CreateUpstream(d, &model.Upstream{Name: "on", BaseURL: "b", APIKey: "k", Format: "openai"})
	model.AddModel(d, old, "m1", false, 0, 0, false)
	model.AddModel(d, on, "m2", false, 0, 0, false)
	model.CreateAlias(d, &model.ModelAlias{Name: "dead-alias", Bindings: []model.AliasBinding{{UpstreamID: old, ModelName: "m1"}}})
	model.CreateAlias(d, &model.ModelAlias{Name: "live-alias", Bindings: []model.AliasBinding{{UpstreamID: on, ModelName: "m2"}}})

	u, _ := model.GetUpstreamByID(d, old)
	at := time.Now().Add(-time.Hour).Truncate(time.Second)
	u.ExpiresAt = &at
	if err := model.UpdateUpstream(d, u); err != nil {
		t.Fatal(err)
	}
	if !u.Enabled {
		t.Fatal("expiry must not touch enabled")
	}
	k, _ := model.CreateExtKey(d, "l", "", 0, 0, nil)

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
	if ids["old/m1"] || ids["dead-alias"] {
		t.Fatalf("expired upstream leaked to /v1/models: %+v", ids)
	}
	if !ids["on/m2"] || !ids["live-alias"] {
		t.Fatalf("unexpired upstream missing: %+v", ids)
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
	model.AddModel(d, uid, "gpt-4o", false, 0, 0, false)
	// key with daily limit of 100, already used 100
	k, _ := model.CreateExtKey(d, "l", "", 100, 0, nil)
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
	model.AddModel(d, uid, "gpt-4o", false, 0, 0, false)
	k, _ := model.CreateExtKey(d, "l", "", 0, 50, nil)
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
	model.AddModel(d, uid, "gpt-4o", false, 0, 0, false)
	k, _ := model.CreateExtKey(d, "l", "", 0, 0, nil)
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
	model.AddModel(d, uid, "gpt-4o", false, 0, 0, false)
	k, _ := model.CreateExtKey(d, "l", "", 1000, 5000, nil)
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
	model.AddModel(d, uid, "gpt-4o", false, 0, 0, false)
	model.AddModel(d, uid, "gpt-4o-mini", false, 0, 0, false)
	k, _ := model.CreateExtKey(d, "restricted", "", 0, 0, []string{"oai/gpt-4o"})

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
	model.AddModel(d, uid, "gpt-4o", false, 0, 0, false)
	k, _ := model.CreateExtKey(d, "restricted", "", 0, 0, []string{"oai/gpt-4o"})
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
	model.AddModel(d, uid, "gpt-4o", false, 0, 0, false)
	model.CreateAlias(d, &model.ModelAlias{Name: "fast", Bindings: []model.AliasBinding{{UpstreamID: uid, ModelName: "gpt-4o"}}})

	for _, tc := range []struct{ allow, model string }{
		{"fast", "oai/gpt-4o"}, // 只列别名 → 直连名被拒
		{"oai/gpt-4o", "fast"}, // 只列直连名 → 别名被拒
	} {
		// 名称取白名单条目：同一 DB 里要建两个 key，名称不能重复
		k, _ := model.CreateExtKey(d, tc.allow, "", 0, 0, []string{tc.allow})
		req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"`+tc.model+`","messages":[]}`))
		req.Header.Set("Authorization", "Bearer "+k.Key)
		w := httptest.NewRecorder()
		g.ServeHTTP(w, req)
		if w.Code != 403 {
			t.Fatalf("allow=%q model=%q status=%d want 403, body=%s", tc.allow, tc.model, w.Code, w.Body.String())
		}
	}
}

// ---------------------------------------------------------------------------
// 配置读缓存：写路径失效必须一路打到网关上
// ---------------------------------------------------------------------------

// fakeUpstream 起一个返回固定 OpenAI 补全的假上游，让请求能真正走完 dispatch。
func fakeUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":5,"total_tokens":10}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// postCompletion 用给定 key 打一次补全请求，返回状态码。
func postCompletion(g *Gateway, key, modelName string) int {
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"`+modelName+`","messages":[]}`))
	req.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	return w.Code
}

// 禁用 key 后同一个 key 必须立刻 401：缓存里那份已过期的行不能继续放行。
// 失效失效漏了的话，这里读到的是内存里的旧 enabled=true。
func TestKeyDisableInvalidatesAuthCache(t *testing.T) {
	g, d := setupGateway(t)
	k, _ := model.CreateExtKey(d, "l", "", 0, 0, nil)

	listModels := func() int {
		req := httptest.NewRequest("GET", "/v1/models", nil)
		req.Header.Set("Authorization", "Bearer "+k.Key)
		w := httptest.NewRecorder()
		g.ServeHTTP(w, req)
		return w.Code
	}
	if code := listModels(); code != 200 {
		t.Fatalf("first request status=%d want 200", code)
	}
	cur, _ := model.GetExtKeyByID(d, k.ID)
	if err := model.UpdateExtKey(d, k.ID, cur.Label, cur.Remark, false, cur.DailyTokenLimit, cur.MonthlyTokenLimit, cur.AllowedModels); err != nil {
		t.Fatal(err)
	}
	if code := listModels(); code != 401 {
		t.Fatalf("disabled key status=%d want 401 (auth cache not invalidated?)", code)
	}
}

// 别名绑定的上游被禁用后，候选必须从链中消失 → 404 has no available bindings。
func TestUpstreamDisableInvalidatesAliasCache(t *testing.T) {
	srv := fakeUpstream(t)
	g, d := setupGateway(t)
	g.client = upstream.NewClient(http.DefaultClient)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "oai", BaseURL: srv.URL, APIKey: "k", Format: "openai"})
	model.CreateAlias(d, &model.ModelAlias{Name: "fast", Bindings: []model.AliasBinding{{UpstreamID: uid, ModelName: "gpt-4o"}}})
	k, _ := model.CreateExtKey(d, "l", "", 0, 0, nil)

	if code := postCompletion(g, k.Key, "fast"); code != 200 {
		t.Fatalf("alias request status=%d want 200, body missing fake upstream?", code)
	}
	u, _ := model.GetUpstreamByID(d, uid)
	u.Enabled = false
	if err := model.UpdateUpstream(d, u); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"fast","messages":[]}`))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != 404 || !strings.Contains(w.Body.String(), "no available bindings") {
		t.Fatalf("status=%d want 404 has no available bindings, body=%s", w.Code, w.Body.String())
	}
}

// 直连路由同理：上游禁用后走内存那份行也要能看出已禁用。
func TestUpstreamDisableInvalidatesDirectRouteCache(t *testing.T) {
	srv := fakeUpstream(t)
	g, d := setupGateway(t)
	g.client = upstream.NewClient(http.DefaultClient)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "oai", BaseURL: srv.URL, APIKey: "k", Format: "openai"})
	k, _ := model.CreateExtKey(d, "l", "", 0, 0, nil)

	if code := postCompletion(g, k.Key, "oai/gpt-4o"); code != 200 {
		t.Fatalf("direct request status=%d want 200", code)
	}
	u, _ := model.GetUpstreamByID(d, uid)
	u.Enabled = false
	if err := model.UpdateUpstream(d, u); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"oai/gpt-4o","messages":[]}`))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != 404 || !strings.Contains(w.Body.String(), "is disabled") {
		t.Fatalf("status=%d want 404 is disabled, body=%s", w.Code, w.Body.String())
	}
}

// 缓存命中不打库：把数据库关掉之后，别名请求仍然能成功走完——鉴权、别名解析、
// 上游行全在内存里。
//
// 只测别名路由：直连路由（name/model）每次都先用模型字符串探一次别名表，未命中
// 不缓存（否则任意随机字符串都能把缓存撑大），所以它关了库必然失败，这是有意
// 为之，不是回归。
func TestCachedReadsDoNotHitDatabase(t *testing.T) {
	srv := fakeUpstream(t)
	g, d := setupGateway(t)
	g.client = upstream.NewClient(http.DefaultClient)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "oai", BaseURL: srv.URL, APIKey: "k", Format: "openai"})
	model.AddModel(d, uid, "gpt-4o", false, 0, 0, false)
	model.CreateAlias(d, &model.ModelAlias{Name: "fast", Bindings: []model.AliasBinding{{UpstreamID: uid, ModelName: "gpt-4o"}}})
	k, _ := model.CreateExtKey(d, "l", "", 0, 0, nil)

	// 预热：key、别名、内嵌的上游行各读一次进缓存（token 限额均为 0，不查库）
	if code := postCompletion(g, k.Key, "fast"); code != 200 {
		t.Fatalf("warmup status=%d want 200", code)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if code := postCompletion(g, k.Key, "fast"); code != 200 {
		t.Fatalf("after db close status=%d want 200 (a cached read still hit the db?)", code)
	}
}

// /v1/models 在无可见模型时必须返回 "data":[] 而非 "data":null——nil 切片编码
// 成 null，遍历列表的 OpenAI 客户端会在 null 上报错。上游全部被禁用/过期跳过
// 时同样走到这里。
func TestModelsEndpointEmptyListEncodesArray(t *testing.T) {
	g, d := setupGateway(t)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "my-openai", BaseURL: "b", APIKey: "k", Format: "openai"})
	model.AddModel(d, uid, "gpt-4o", false, 0, 0, false)
	// 白名单匹配不到任何模型
	k, _ := model.CreateExtKey(d, "l", "", 0, 0, []string{"other/nope"})

	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status=%d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"data":[]`) {
		t.Fatalf("empty model list should encode as [], got %s", body)
	}
}
