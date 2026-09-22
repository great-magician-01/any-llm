package webapi

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/great-magician-01/any-llm/internal/db"
	"github.com/great-magician-01/any-llm/internal/model"
	"github.com/great-magician-01/any-llm/internal/upstream"
)

func setupAPI(t *testing.T) (*API, *sql.DB) {
	t.Helper()
	// 配置读缓存是 model 包级、进程内的，而每个用例都是一个临时库（见
	// gateway 的 setupGateway）。
	model.ResetConfigCache()
	t.Cleanup(model.ResetConfigCache)
	d, err := db.OpenSQLite(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	a := NewAPI(d, nil, nil)
	return a, d
}

func TestCreateUpstream(t *testing.T) {
	a, _ := setupAPI(t)
	body, _ := json.Marshal(map[string]any{"name": "test", "base_url": "https://api.openai.com", "api_key": "sk-xxx", "format": "openai"})
	req := httptest.NewRequest("POST", "/api/admin/upstreams", bytes.NewReader(body))
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 && w.Code != 201 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["name"] != "test" {
		t.Fatalf("resp=%+v", resp)
	}
}

func TestListUpstreams(t *testing.T) {
	a, d := setupAPI(t)
	model.CreateUpstream(d, &model.Upstream{Name: "u1", BaseURL: "b", APIKey: "k", Format: "openai"})
	model.CreateUpstream(d, &model.Upstream{Name: "u2", BaseURL: "b", APIKey: "k", Format: "anthropic"})
	req := httptest.NewRequest("GET", "/api/admin/upstreams", nil)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d", w.Code)
	}
	var resp struct {
		Data []map[string]any `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Data) != 2 {
		t.Fatalf("len=%d", len(resp.Data))
	}
}

func TestListUpstreamsStatusFilter(t *testing.T) {
	a, d := setupAPI(t)
	model.CreateUpstream(d, &model.Upstream{Name: "on1", BaseURL: "b", APIKey: "k", Format: "openai"})
	model.CreateUpstream(d, &model.Upstream{Name: "on2", BaseURL: "b", APIKey: "k", Format: "anthropic"})
	// CreateUpstream 的 INSERT 不含 enabled 列（DB 默认启用），创建即禁用需读回再改存。
	offID, _ := model.CreateUpstream(d, &model.Upstream{Name: "off", BaseURL: "b", APIKey: "k", Format: "openai"})
	off, _ := model.GetUpstreamByID(d, offID)
	off.Enabled = false
	if err := model.UpdateUpstream(d, off); err != nil {
		t.Fatal(err)
	}

	count := func(url string) int {
		t.Helper()
		req := httptest.NewRequest("GET", url, nil)
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("%s: status=%d body=%s", url, w.Code, w.Body.String())
		}
		var resp struct {
			Data []map[string]any `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &resp)
		return len(resp.Data)
	}
	// 缺省与显式 all 都是全量（其它页面依赖这个口径）
	if n := count("/api/admin/upstreams"); n != 3 {
		t.Fatalf("no status: len=%d want 3", n)
	}
	if n := count("/api/admin/upstreams?status=all"); n != 3 {
		t.Fatalf("status=all: len=%d want 3", n)
	}
	if n := count("/api/admin/upstreams?status=enabled"); n != 2 {
		t.Fatalf("status=enabled: len=%d want 2", n)
	}
	if n := count("/api/admin/upstreams?status=disabled"); n != 1 {
		t.Fatalf("status=disabled: len=%d want 1", n)
	}

	// 未知 status 按全量返回而非 400：该参数引入前任何 status= 都被忽略并返回
	// 全量，硬报错会打破存量的书签/探针/集成调用。
	if n := count("/api/admin/upstreams?status=bogus"); n != 3 {
		t.Fatalf("status=bogus: len=%d want 3 (unknown status falls back to all)", n)
	}
}

func TestDeleteUpstream(t *testing.T) {
	a, d := setupAPI(t)
	id, _ := model.CreateUpstream(d, &model.Upstream{Name: "u1", BaseURL: "b", APIKey: "k", Format: "openai"})
	req := httptest.NewRequest("DELETE", "/api/admin/upstreams/"+strconv.FormatInt(id, 10), nil)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 && w.Code != 204 {
		t.Fatalf("status=%d", w.Code)
	}
	list, _ := model.ListUpstreams(d)
	if len(list) != 0 {
		t.Fatalf("after delete len=%d", len(list))
	}
}

func TestFetchModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"id":"gpt-4o"},{"id":"gpt-4o-mini"}]}`))
	}))
	defer srv.Close()

	a, d := setupAPI(t)
	a.client = upstream.NewClient(http.DefaultClient)
	id, _ := model.CreateUpstream(d, &model.Upstream{Name: "u", BaseURL: srv.URL, APIKey: "k", Format: "openai"})

	req := httptest.NewRequest("POST", "/api/admin/upstreams/"+strconv.FormatInt(id, 10)+"/fetch-models", nil)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	models, _ := model.ListModels(d, id)
	if len(models) != 2 {
		t.Fatalf("models=%d", len(models))
	}
}

// TestModelMultimodalAPI 走 HTTP 层钉住多模态开关：列表返回该字段、新增默认
// 否、显式 true 落库、PUT 能改回去。
func TestModelMultimodalAPI(t *testing.T) {
	a, d := setupAPI(t)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "u", BaseURL: "b", APIKey: "k", Format: "openai"})
	base := "/api/admin/upstreams/" + strconv.FormatInt(uid, 10) + "/models"
	post := func(path string, body map[string]any) int {
		t.Helper()
		b, _ := json.Marshal(body)
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, httptest.NewRequest("POST", path, bytes.NewReader(b)))
		return w.Code
	}
	put := func(path string, body map[string]any) int {
		t.Helper()
		b, _ := json.Marshal(body)
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, httptest.NewRequest("PUT", path, bytes.NewReader(b)))
		return w.Code
	}

	// 不传 multimodal → 默认否
	if code := post(base, map[string]any{"model_name": "plain", "context_length": 1000, "max_output_length": 100}); code != 200 {
		t.Fatalf("add plain status=%d", code)
	}
	// 显式 true → 落库
	if code := post(base, map[string]any{"model_name": "vision", "context_length": 1000, "max_output_length": 100, "multimodal": true}); code != 200 {
		t.Fatalf("add vision status=%d", code)
	}
	ms, err := model.ListModels(d, uid)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]model.UpstreamModel{}
	for _, m := range ms {
		byName[m.ModelName] = m
	}
	if byName["plain"].Multimodal {
		t.Fatalf("absent multimodal must default to false: %+v", byName["plain"])
	}
	if !byName["vision"].Multimodal {
		t.Fatalf("explicit multimodal=true not stored: %+v", byName["vision"])
	}

	// 列表接口要把它吐出来（前端据此显示标记）
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, httptest.NewRequest("GET", base, nil))
	if w.Code != 200 {
		t.Fatalf("list status=%d", w.Code)
	}
	var resp struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, m := range resp.Data {
		if m["model_name"] == "vision" {
			seen = true
			if m["multimodal"] != true {
				t.Fatalf("list payload missing multimodal: %v", m)
			}
		}
	}
	if !seen {
		t.Fatalf("vision model missing from list: %v", resp.Data)
	}

	// PUT 改回 false
	vid := byName["vision"].ID
	if code := put(base+"/"+strconv.FormatInt(vid, 10), map[string]any{"context_length": 1000, "max_output_length": 100, "multimodal": false}); code != 200 {
		t.Fatalf("update status=%d", code)
	}
	got, _ := model.ListModels(d, uid)
	for _, m := range got {
		if m.ID == vid && m.Multimodal {
			t.Fatalf("multimodal not cleared: %+v", m)
		}
	}
}

// TestUpdateUpstream_MaskedKeyNotOverwritten verifies that when the client
// sends back the masked API key placeholder (as the admin UI does when a user
// edits an upstream without re-entering the secret), the gateway must NOT
// overwrite the stored key with the masked string.
func TestUpdateUpstream_MaskedKeyNotOverwritten(t *testing.T) {
	a, d := setupAPI(t)
	const realKey = "sk-abcdefghijklmno1234567890qrstuvwxyz"
	id, _ := model.CreateUpstream(d, &model.Upstream{Name: "u", BaseURL: "https://example.com", APIKey: realKey, Format: "openai"})

	// Simulate the UI echoing back the masked key as returned by the
	// listUpstreams HTTP endpoint (model layer returns raw, webapi masks).
	masked := realKey[:4] + "****" + realKey[len(realKey)-4:]

	body, _ := json.Marshal(map[string]any{
		"name":     "u",
		"base_url": "https://example.com",
		"api_key":  masked,
		"format":   "openai",
	})
	req := httptest.NewRequest("PUT", "/api/admin/upstreams/"+strconv.FormatInt(id, 10), bytes.NewReader(body))
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	// Verify the actual stored key was NOT replaced with the masked string.
	u, _ := model.GetUpstreamByID(d, id)
	if u.APIKey != realKey {
		t.Fatalf("stored key got overwritten: got=%q want=%q", u.APIKey, realKey)
	}
}

// TestUpdateUpstream_EmptyKeyPreserved verifies that sending an empty api_key
// keeps the existing stored value (the standard "no change" signal).
func TestUpdateUpstream_EmptyKeyPreserved(t *testing.T) {
	a, d := setupAPI(t)
	const realKey = "sk-realsecret123"
	id, _ := model.CreateUpstream(d, &model.Upstream{Name: "u", BaseURL: "https://example.com", APIKey: realKey, Format: "openai"})

	body, _ := json.Marshal(map[string]any{
		"name":     "u-renamed",
		"base_url": "https://example.com",
		"api_key":  "",
		"format":   "openai",
	})
	req := httptest.NewRequest("PUT", "/api/admin/upstreams/"+strconv.FormatInt(id, 10), bytes.NewReader(body))
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	u, _ := model.GetUpstreamByID(d, id)
	if u.APIKey != realKey {
		t.Fatalf("stored key changed: got=%q want=%q", u.APIKey, realKey)
	}
	if u.Name != "u-renamed" {
		t.Fatalf("name not updated: %q", u.Name)
	}
}

// TestUpdateUpstream_NewKeyApplied verifies that a real (non-masked) key
// still overwrites the stored value.
func TestUpdateUpstream_NewKeyApplied(t *testing.T) {
	a, d := setupAPI(t)
	id, _ := model.CreateUpstream(d, &model.Upstream{Name: "u", BaseURL: "https://example.com", APIKey: "sk-old", Format: "openai"})

	body, _ := json.Marshal(map[string]any{
		"name":     "u",
		"base_url": "https://example.com",
		"api_key":  "sk-new-real-key",
		"format":   "openai",
	})
	req := httptest.NewRequest("PUT", "/api/admin/upstreams/"+strconv.FormatInt(id, 10), bytes.NewReader(body))
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	u, _ := model.GetUpstreamByID(d, id)
	if u.APIKey != "sk-new-real-key" {
		t.Fatalf("stored key not updated: got=%q", u.APIKey)
	}
}

// TestCreateUpstream_ResponsesFormat verifies the admin API accepts creating
// an upstream in the OpenAI Responses API "responses" format.
func TestCreateUpstream_ResponsesFormat(t *testing.T) {
	a, _ := setupAPI(t)
	body, _ := json.Marshal(map[string]any{"name": "r1", "base_url": "https://api.openai.com", "api_key": "sk-xxx", "format": "responses"})
	req := httptest.NewRequest("POST", "/api/admin/upstreams", bytes.NewReader(body))
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

// TestCreateUpstream_InvalidFormat verifies the admin API rejects an unknown
// upstream format with 400.
func TestCreateUpstream_InvalidFormat(t *testing.T) {
	a, _ := setupAPI(t)
	body, _ := json.Marshal(map[string]any{"name": "r2", "base_url": "https://x", "api_key": "k", "format": "yaml"})
	req := httptest.NewRequest("POST", "/api/admin/upstreams", bytes.NewReader(body))
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("status=%d", w.Code)
	}
}

// TestUpdateUpstream_InvalidFormat verifies the admin API rejects an unknown
// upstream format on update with 400 (app-layer validation; DB CHECK removed).
func TestUpdateUpstream_InvalidFormat(t *testing.T) {
	a, d := setupAPI(t)
	id, _ := model.CreateUpstream(d, &model.Upstream{Name: "u", BaseURL: "https://example.com", APIKey: "k", Format: "openai"})

	body, _ := json.Marshal(map[string]any{"format": "yaml"})
	req := httptest.NewRequest("PUT", "/api/admin/upstreams/"+strconv.FormatInt(id, 10), bytes.NewReader(body))
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

// TestUpdateUpstream_ResponsesFormat verifies the admin API accepts switching
// an upstream to the OpenAI Responses API "responses" format.
func TestUpdateUpstream_ResponsesFormat(t *testing.T) {
	a, d := setupAPI(t)
	id, _ := model.CreateUpstream(d, &model.Upstream{Name: "u", BaseURL: "https://example.com", APIKey: "k", Format: "openai"})

	body, _ := json.Marshal(map[string]any{"format": "responses"})
	req := httptest.NewRequest("PUT", "/api/admin/upstreams/"+strconv.FormatInt(id, 10), bytes.NewReader(body))
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	u, _ := model.GetUpstreamByID(d, id)
	if u.Format != "responses" {
		t.Fatalf("format not updated: %q", u.Format)
	}
}

// TestCreateUpstream_ExplicitDisabled 创建时可显式 enabled=false（预建禁用、
// 配置好模型与别名后再启用）；字段缺省时默认启用（与 DB 默认一致）。
func TestCreateUpstream_ExplicitDisabled(t *testing.T) {
	a, d := setupAPI(t)

	create := func(extra map[string]any) (int64, bool) {
		t.Helper()
		body, _ := json.Marshal(extra)
		req := httptest.NewRequest("POST", "/api/admin/upstreams", bytes.NewReader(body))
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		var resp struct {
			ID      int64 `json:"id"`
			Enabled bool  `json:"enabled"`
		}
		json.Unmarshal(w.Body.Bytes(), &resp)
		return resp.ID, resp.Enabled
	}

	id, enabled := create(map[string]any{"name": "off", "base_url": "https://x", "api_key": "k", "format": "openai", "enabled": false})
	if enabled {
		t.Fatal("response should report disabled")
	}
	u, _ := model.GetUpstreamByID(d, id)
	if u.Enabled {
		t.Fatal("stored upstream should be disabled")
	}

	id, enabled = create(map[string]any{"name": "on", "base_url": "https://x", "api_key": "k", "format": "openai"})
	if !enabled {
		t.Fatal("response should report enabled")
	}
	u, _ = model.GetUpstreamByID(d, id)
	if !u.Enabled {
		t.Fatal("stored upstream should default to enabled")
	}
}

// TestCreateUpstream_MaxConcurrent 并发上限：缺省给默认值 100，显式值生效，
// 显式 0 = 不限，负数拒绝。
func TestCreateUpstream_MaxConcurrent(t *testing.T) {
	a, d := setupAPI(t)

	create := func(extra map[string]any) (int64, int, int) {
		t.Helper()
		base := map[string]any{"name": "u", "base_url": "https://x", "api_key": "k", "format": "openai"}
		for k, v := range extra {
			base[k] = v
		}
		body, _ := json.Marshal(base)
		req := httptest.NewRequest("POST", "/api/admin/upstreams", bytes.NewReader(body))
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, req)
		if w.Code != 200 {
			return 0, 0, w.Code
		}
		var resp struct {
			ID            int64 `json:"id"`
			MaxConcurrent int   `json:"max_concurrent"`
		}
		json.Unmarshal(w.Body.Bytes(), &resp)
		return resp.ID, resp.MaxConcurrent, 200
	}

	// 缺省 → 默认 100
	id, mc, code := create(map[string]any{"name": "d1"})
	if code != 200 || mc != model.DefaultMaxConcurrent {
		t.Fatalf("omitted: code=%d max_concurrent=%d want default %d", code, mc, model.DefaultMaxConcurrent)
	}
	u, _ := model.GetUpstreamByID(d, id)
	if u.MaxConcurrent != model.DefaultMaxConcurrent {
		t.Fatalf("stored max_concurrent=%d", u.MaxConcurrent)
	}

	// 显式 50
	_, mc, code = create(map[string]any{"name": "d2", "max_concurrent": 50})
	if code != 200 || mc != 50 {
		t.Fatalf("explicit 50: code=%d max_concurrent=%d", code, mc)
	}

	// 显式 0 = 不限
	_, mc, code = create(map[string]any{"name": "d3", "max_concurrent": 0})
	if code != 200 || mc != 0 {
		t.Fatalf("explicit 0: code=%d max_concurrent=%d", code, mc)
	}

	// 负数 → 400
	if _, _, code = create(map[string]any{"name": "d4", "max_concurrent": -1}); code != 400 {
		t.Fatalf("negative: code=%d want 400", code)
	}
}

// TestUpdateUpstream_MaxConcurrent 更新并发上限：指针语义——给了就改（含 0 =
// 不限），没给保持现状；负数拒绝。
func TestUpdateUpstream_MaxConcurrent(t *testing.T) {
	a, d := setupAPI(t)
	id, _ := model.CreateUpstream(d, &model.Upstream{Name: "u", BaseURL: "b", APIKey: "k", Format: "openai", MaxConcurrent: 100})

	do := func(body map[string]any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		req := httptest.NewRequest("PUT", "/api/admin/upstreams/"+strconv.FormatInt(id, 10), bytes.NewReader(b))
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, req)
		return w
	}

	if w := do(map[string]any{"max_concurrent": 7}); w.Code != 200 {
		t.Fatalf("set 7 status=%d body=%s", w.Code, w.Body.String())
	}
	u, _ := model.GetUpstreamByID(d, id)
	if u.MaxConcurrent != 7 {
		t.Fatalf("max_concurrent=%d want 7", u.MaxConcurrent)
	}

	// 字段缺省保持现状
	if w := do(map[string]any{"name": "u2"}); w.Code != 200 {
		t.Fatalf("rename status=%d body=%s", w.Code, w.Body.String())
	}
	u, _ = model.GetUpstreamByID(d, id)
	if u.MaxConcurrent != 7 {
		t.Fatalf("absent field must keep current value, got %d", u.MaxConcurrent)
	}

	// 显式 0 = 不限
	if w := do(map[string]any{"max_concurrent": 0}); w.Code != 200 {
		t.Fatalf("set 0 status=%d body=%s", w.Code, w.Body.String())
	}
	u, _ = model.GetUpstreamByID(d, id)
	if u.MaxConcurrent != 0 {
		t.Fatalf("max_concurrent=%d want 0 (unlimited)", u.MaxConcurrent)
	}

	if w := do(map[string]any{"max_concurrent": -5}); w.Code != 400 {
		t.Fatalf("negative status=%d want 400", w.Code)
	}
}

// TestUpdateUpstream_EnableDisable verifies the PATCH-style enabled toggle:
// explicit value flips the state, absent field (old clients sending the full
// form without enabled) preserves it.
func TestUpdateUpstream_EnableDisable(t *testing.T) {
	a, d := setupAPI(t)
	id, _ := model.CreateUpstream(d, &model.Upstream{Name: "u", BaseURL: "b", APIKey: "k", Format: "openai"})

	do := func(body map[string]any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		req := httptest.NewRequest("PUT", "/api/admin/upstreams/"+strconv.FormatInt(id, 10), bytes.NewReader(b))
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, req)
		return w
	}

	if w := do(map[string]any{"enabled": false}); w.Code != 200 {
		t.Fatalf("disable status=%d body=%s", w.Code, w.Body.String())
	}
	u, _ := model.GetUpstreamByID(d, id)
	if u.Enabled {
		t.Fatal("upstream should be disabled")
	}

	// enabled 缺省（旧客户端全量表单不含该字段）→ 保持禁用，其余字段照常更新
	if w := do(map[string]any{"name": "u2"}); w.Code != 200 {
		t.Fatalf("rename status=%d body=%s", w.Code, w.Body.String())
	}
	u, _ = model.GetUpstreamByID(d, id)
	if u.Enabled || u.Name != "u2" {
		t.Fatalf("after rename u=%+v", u)
	}

	if w := do(map[string]any{"enabled": true}); w.Code != 200 {
		t.Fatalf("enable status=%d body=%s", w.Code, w.Body.String())
	}
	u, _ = model.GetUpstreamByID(d, id)
	if !u.Enabled {
		t.Fatal("upstream should be re-enabled")
	}
}

// TestUpstreamExpiry_API 覆盖管理端三态：创建时设置、update 显式设置/清除/
// 缺省保留，以及非法格式 400。缺省保留这一条尤其重要——toggleEnabled 那种
// 只发 {"enabled":...} 的部分 PATCH 不能把有效期清掉。
func TestUpstreamExpiry_API(t *testing.T) {
	a, d := setupAPI(t)

	create := func(extra map[string]any) int64 {
		t.Helper()
		base := map[string]any{"name": "u", "base_url": "https://x", "api_key": "k", "format": "openai"}
		for k, v := range extra {
			base[k] = v
		}
		b, _ := json.Marshal(base)
		req := httptest.NewRequest("POST", "/api/admin/upstreams", bytes.NewReader(b))
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("create status=%d body=%s", w.Code, w.Body.String())
		}
		var resp struct {
			ID int64 `json:"id"`
		}
		json.Unmarshal(w.Body.Bytes(), &resp)
		return resp.ID
	}
	do := func(id int64, body map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		b, _ := json.Marshal(body)
		req := httptest.NewRequest("PUT", "/api/admin/upstreams/"+strconv.FormatInt(id, 10), bytes.NewReader(b))
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, req)
		return w
	}

	at := time.Now().Add(24 * time.Hour).Truncate(time.Second)
	later := at.Add(48 * time.Hour)

	// 创建时设置
	id := create(map[string]any{"expires_at": at})
	if u, _ := model.GetUpstreamByID(d, id); u.ExpiresAt == nil || !u.ExpiresAt.Equal(at) {
		t.Fatalf("create expires_at=%v, want %v", u.ExpiresAt, at)
	}

	// 缺省 → 保留现状（顺带确认其它字段仍能正常更新）
	if w := do(id, map[string]any{"name": "u2"}); w.Code != 200 {
		t.Fatalf("rename status=%d body=%s", w.Code, w.Body.String())
	}
	if u, _ := model.GetUpstreamByID(d, id); u.ExpiresAt == nil || !u.ExpiresAt.Equal(at) {
		t.Fatalf("absent expires_at=%v, want preserved %v", u.ExpiresAt, at)
	}

	// 部分 PATCH（只发 enabled，前端的开关走这条路）也不能清掉有效期
	if w := do(id, map[string]any{"enabled": false}); w.Code != 200 {
		t.Fatalf("toggle status=%d body=%s", w.Code, w.Body.String())
	}
	if u, _ := model.GetUpstreamByID(d, id); u.ExpiresAt == nil || !u.ExpiresAt.Equal(at) {
		t.Fatalf("partial patch cleared expires_at=%v, want %v", u.ExpiresAt, at)
	}

	// 显式设置新值
	if w := do(id, map[string]any{"expires_at": later}); w.Code != 200 {
		t.Fatalf("extend status=%d body=%s", w.Code, w.Body.String())
	}
	if u, _ := model.GetUpstreamByID(d, id); u.ExpiresAt == nil || !u.ExpiresAt.Equal(later) {
		t.Fatalf("extend expires_at=%v, want %v", u.ExpiresAt, later)
	}

	// 显式 null → 清除，恢复永久有效
	if w := do(id, map[string]any{"expires_at": nil}); w.Code != 200 {
		t.Fatalf("clear status=%d body=%s", w.Code, w.Body.String())
	}
	if u, _ := model.GetUpstreamByID(d, id); u.ExpiresAt != nil {
		t.Fatalf("clear expires_at=%v, want nil", u.ExpiresAt)
	}

	// 空串同样视为清除（前端选择器清空时两种都可能发出来）
	if w := do(id, map[string]any{"expires_at": at}); w.Code != 200 {
		t.Fatalf("set status=%d body=%s", w.Code, w.Body.String())
	}
	if w := do(id, map[string]any{"expires_at": ""}); w.Code != 200 {
		t.Fatalf("clear-empty status=%d body=%s", w.Code, w.Body.String())
	}
	if u, _ := model.GetUpstreamByID(d, id); u.ExpiresAt != nil {
		t.Fatalf("clear-empty expires_at=%v, want nil", u.ExpiresAt)
	}

	// 非法格式 → 400，且不写库
	if w := do(id, map[string]any{"expires_at": "not-a-time"}); w.Code != 400 {
		t.Fatalf("invalid status=%d want 400, body=%s", w.Code, w.Body.String())
	}
	if u, _ := model.GetUpstreamByID(d, id); u.ExpiresAt != nil {
		t.Fatalf("invalid expires_at=%v, want nil (unchanged)", u.ExpiresAt)
	}
}

// TestUpstreamExpiry_TimezoneHandling 带 Z / 非本地偏移的输入必须落到同一
// 绝对时刻。SQLite 存 RFC3339 文本无所谓，但 PG 的 TIMESTAMP(0) 写入时丢弃
// 时区只存墙钟（internal/db/pgtime.go），位置不归一就会偏几个时区。
func TestUpstreamExpiry_TimezoneHandling(t *testing.T) {
	a, d := setupAPI(t)
	// 用一个必定非本地的偏移构造目标时刻（UTC 正午）
	want := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)

	b, _ := json.Marshal(map[string]any{"name": "u", "base_url": "https://x", "api_key": "k",
		"format": "openai", "expires_at": want.Format(time.RFC3339)})
	req := httptest.NewRequest("POST", "/api/admin/upstreams", bytes.NewReader(b))
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("create status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	got, _ := model.GetUpstreamByID(d, resp.ID)
	if got.ExpiresAt == nil {
		t.Fatal("expires_at not stored")
	}
	if !got.ExpiresAt.Equal(want) {
		t.Fatalf("expires_at=%v (%s), want same instant as %v",
			got.ExpiresAt, got.ExpiresAt.Format(time.RFC3339), want)
	}
	// 亚秒被截断（与 PG TIMESTAMP(0) 口径一致，前端选择器按秒级回填）
	if got.ExpiresAt.Nanosecond() != 0 {
		t.Fatalf("expires_at has sub-second precision: %v", got.ExpiresAt)
	}
}

// 模型写操作的错误语义：添加已存在的活跃模型返回 409（静默 200 会让管理员以为
// 新配置生效了）；编辑不存在/已软删/跨上游的模型返回 404（更新 0 行不能假成功）。
func TestModelWriteErrorSemantics(t *testing.T) {
	a, d := setupAPI(t)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "u", BaseURL: "b", APIKey: "k", Format: "openai"})
	uid2, _ := model.CreateUpstream(d, &model.Upstream{Name: "u2", BaseURL: "b", APIKey: "k", Format: "openai"})
	base := "/api/admin/upstreams/" + strconv.FormatInt(uid, 10) + "/models"
	call := func(method, path string, body map[string]any) int {
		t.Helper()
		b, _ := json.Marshal(body)
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, httptest.NewRequest(method, path, bytes.NewReader(b)))
		return w.Code
	}

	add := map[string]any{"model_name": "m1", "context_length": 1000, "max_output_length": 100}
	if code := call("POST", base, add); code != 200 {
		t.Fatalf("first add status=%d", code)
	}
	if code := call("POST", base, add); code != 409 {
		t.Fatalf("duplicate add status=%d, want 409", code)
	}

	ms, _ := model.ListModels(d, uid)
	mid := ms[0].ID
	edit := map[string]any{"context_length": 2000, "max_output_length": 200}
	// 跨上游：模型属于 u，路径却指 u2
	cross := "/api/admin/upstreams/" + strconv.FormatInt(uid2, 10) + "/models/" + strconv.FormatInt(mid, 10)
	if code := call("PUT", cross, edit); code != 404 {
		t.Fatalf("cross-upstream edit status=%d, want 404", code)
	}
	// 不存在的模型 id
	if code := call("PUT", base+"/999999", edit); code != 404 {
		t.Fatalf("missing model edit status=%d, want 404", code)
	}
	// 跨上游与不存在这两次失败都不能改动原行
	ms, _ = model.ListModels(d, uid)
	if len(ms) != 1 || ms[0].ContextLength != 1000 {
		t.Fatalf("failed edits should not have touched the row: %+v", ms)
	}
	// 软删后同样 404
	if err := model.DeleteModel(d, mid); err != nil {
		t.Fatal(err)
	}
	if code := call("PUT", base+"/"+strconv.FormatInt(mid, 10), edit); code != 404 {
		t.Fatalf("soft-deleted model edit status=%d, want 404", code)
	}
}
