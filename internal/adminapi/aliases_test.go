package adminapi

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/great-magician-01/any-llm/internal/store"
)

func doAliasReq(t *testing.T, a *API, method, path string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	var body *bytes.Reader
	if payload != nil {
		b, _ := json.Marshal(payload)
		body = bytes.NewReader(b)
	} else {
		body = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, body)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	return w
}

func TestAliasCRUD(t *testing.T) {
	a, d := setupAPI(t)
	uid1, _ := store.CreateUpstream(d, &store.Upstream{Name: "u1", BaseURL: "b", APIKey: "k", Format: "openai"})
	uid2, _ := store.CreateUpstream(d, &store.Upstream{Name: "u2", BaseURL: "b", APIKey: "k", Format: "anthropic"})

	// create
	w := doAliasReq(t, a, "POST", "/api/admin/aliases", map[string]any{
		"name": "fixed-gpt",
		"bindings": []map[string]any{
			{"upstream_id": uid1, "model_name": "gpt-4o"},
			{"upstream_id": uid2, "model_name": "claude-sonnet"},
		},
	})
	if w.Code != 200 {
		t.Fatalf("create status=%d body=%s", w.Code, w.Body.String())
	}
	var created store.ModelAlias
	json.Unmarshal(w.Body.Bytes(), &created)
	if created.ID == 0 || len(created.Bindings) != 2 || created.Bindings[0].UpstreamName != "u1" || created.Bindings[1].UpstreamName != "u2" {
		t.Fatalf("created=%+v", created)
	}

	// list
	w = doAliasReq(t, a, "GET", "/api/admin/aliases", nil)
	if w.Code != 200 {
		t.Fatalf("list status=%d", w.Code)
	}
	var listResp struct {
		Data []store.ModelAlias `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &listResp)
	if len(listResp.Data) != 1 || listResp.Data[0].Name != "fixed-gpt" || len(listResp.Data[0].Bindings) != 2 {
		t.Fatalf("list=%+v", listResp.Data)
	}

	// get
	w = doAliasReq(t, a, "GET", "/api/admin/aliases/"+strconv.FormatInt(created.ID, 10), nil)
	if w.Code != 200 {
		t.Fatalf("get status=%d", w.Code)
	}

	// update：改名 + 整体替换绑定（顺序即优先级）
	w = doAliasReq(t, a, "PUT", "/api/admin/aliases/"+strconv.FormatInt(created.ID, 10), map[string]any{
		"name": "fixed-gpt-2",
		"bindings": []map[string]any{
			{"upstream_id": uid2, "model_name": "claude-opus"},
		},
	})
	if w.Code != 200 {
		t.Fatalf("update status=%d body=%s", w.Code, w.Body.String())
	}
	var updated store.ModelAlias
	json.Unmarshal(w.Body.Bytes(), &updated)
	if updated.Name != "fixed-gpt-2" || len(updated.Bindings) != 1 || updated.Bindings[0].ModelName != "claude-opus" || updated.Bindings[0].Priority != 0 {
		t.Fatalf("updated=%+v", updated)
	}

	// delete
	w = doAliasReq(t, a, "DELETE", "/api/admin/aliases/"+strconv.FormatInt(created.ID, 10), nil)
	if w.Code != 200 {
		t.Fatalf("delete status=%d", w.Code)
	}
	w = doAliasReq(t, a, "GET", "/api/admin/aliases/"+strconv.FormatInt(created.ID, 10), nil)
	if w.Code != 404 {
		t.Fatalf("get after delete status=%d want 404", w.Code)
	}
}

// TestAliasProfilesCRUDAndActivate 覆盖多配置的管理端接口：创建时按 profile
// 分组、activate 一键切换、只改绑定时的 active 解析、以及目标配置校验。
func TestAliasProfilesCRUDAndActivate(t *testing.T) {
	a, d := setupAPI(t)
	uid1, _ := store.CreateUpstream(d, &store.Upstream{Name: "u1", BaseURL: "b", APIKey: "k", Format: "openai"})
	uid2, _ := store.CreateUpstream(d, &store.Upstream{Name: "u2", BaseURL: "b", APIKey: "k", Format: "openai"})

	w := doAliasReq(t, a, "POST", "/api/admin/aliases", map[string]any{
		"name":           "deepseek",
		"active_profile": "峰期",
		"bindings": []map[string]any{
			{"upstream_id": uid1, "model_name": "peak-1", "profile": "峰期"},
			{"upstream_id": uid2, "model_name": "valley-1", "profile": "谷期"},
			{"upstream_id": uid2, "model_name": "peak-2", "profile": "峰期"},
		},
	})
	if w.Code != 200 {
		t.Fatalf("create status=%d body=%s", w.Code, w.Body.String())
	}
	var created store.ModelAlias
	json.Unmarshal(w.Body.Bytes(), &created)
	if created.ActiveProfile != "峰期" || len(created.Bindings) != 3 {
		t.Fatalf("created=%+v", created)
	}
	for i, want := range []string{"峰期", "谷期", "峰期"} {
		if created.Bindings[i].Profile != want || created.Bindings[i].Priority != i {
			t.Fatalf("binding[%d]=%+v", i, created.Bindings[i])
		}
	}

	// 一键切换：只发 profile，不重写绑定
	w = doAliasReq(t, a, "POST", "/api/admin/aliases/"+strconv.FormatInt(created.ID, 10)+"/activate", map[string]any{"profile": "谷期"})
	if w.Code != 200 {
		t.Fatalf("activate status=%d body=%s", w.Code, w.Body.String())
	}
	var switched store.ModelAlias
	json.Unmarshal(w.Body.Bytes(), &switched)
	if switched.ActiveProfile != "谷期" || len(switched.Bindings) != 3 {
		t.Fatalf("switched=%+v", switched)
	}

	// 未知配置 / 不存在的别名
	w = doAliasReq(t, a, "POST", "/api/admin/aliases/"+strconv.FormatInt(created.ID, 10)+"/activate", map[string]any{"profile": "不存在"})
	if w.Code != 400 {
		t.Fatalf("unknown profile status=%d body=%s", w.Code, w.Body.String())
	}
	w = doAliasReq(t, a, "POST", "/api/admin/aliases/9999/activate", map[string]any{"profile": "谷期"})
	if w.Code != 404 {
		t.Fatalf("missing alias status=%d body=%s", w.Code, w.Body.String())
	}

	// 只更新名称/绑定、不给 active：保留仍在的“谷期”
	w = doAliasReq(t, a, "PUT", "/api/admin/aliases/"+strconv.FormatInt(created.ID, 10), map[string]any{
		"bindings": []map[string]any{
			{"upstream_id": uid2, "model_name": "valley-1", "profile": "谷期"},
		},
	})
	if w.Code != 200 {
		t.Fatalf("update status=%d body=%s", w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &switched)
	if switched.ActiveProfile != "谷期" || len(switched.Bindings) != 1 {
		t.Fatalf("update keep active=%+v", switched)
	}

	// 删掉当前生效配置且未指定 active：自动选第一套，避免别名空转
	w = doAliasReq(t, a, "PUT", "/api/admin/aliases/"+strconv.FormatInt(created.ID, 10), map[string]any{
		"bindings": []map[string]any{
			{"upstream_id": uid1, "model_name": "peak-1", "profile": "峰期"},
		},
	})
	if w.Code != 200 {
		t.Fatalf("update fallback status=%d body=%s", w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &switched)
	if switched.ActiveProfile != "峰期" {
		t.Fatalf("auto active=%q", switched.ActiveProfile)
	}

	// 显式指定不存在的 active → 400，且不改库
	w = doAliasReq(t, a, "PUT", "/api/admin/aliases/"+strconv.FormatInt(created.ID, 10), map[string]any{
		"active_profile": "不存在",
	})
	if w.Code != 400 {
		t.Fatalf("bad active status=%d body=%s", w.Code, w.Body.String())
	}
	if got, _ := store.GetAliasByID(d, created.ID); got.ActiveProfile != "峰期" {
		t.Fatalf("failed update changed alias: %+v", got)
	}
}

// TestAliasProfilesValidation 配置名归一与非法值：空 profile 落 default，
// 超长/active 不存在返回 400。
func TestAliasProfilesValidation(t *testing.T) {
	a, d := setupAPI(t)
	uid, _ := store.CreateUpstream(d, &store.Upstream{Name: "u", BaseURL: "b", APIKey: "k", Format: "openai"})

	// 空 profile → default；未给 active → default
	w := doAliasReq(t, a, "POST", "/api/admin/aliases", map[string]any{
		"name":     "legacy",
		"bindings": []map[string]any{{"upstream_id": uid, "model_name": "m"}},
	})
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var created store.ModelAlias
	json.Unmarshal(w.Body.Bytes(), &created)
	if created.ActiveProfile != store.DefaultAliasProfile || created.Bindings[0].Profile != store.DefaultAliasProfile {
		t.Fatalf("defaults=%+v", created)
	}

	long := strings.Repeat("峰", 65)
	cases := []map[string]any{
		{ // active 指向不存在的配置
			"name": "bad-active", "active_profile": "谷期",
			"bindings": []map[string]any{{"upstream_id": uid, "model_name": "m", "profile": "峰期"}},
		},
		{ // 配置名超长
			"name":     "bad-long",
			"bindings": []map[string]any{{"upstream_id": uid, "model_name": "m", "profile": long}},
		},
	}
	for i, c := range cases {
		w := doAliasReq(t, a, "POST", "/api/admin/aliases", c)
		if w.Code != 400 {
			t.Fatalf("case %d: status=%d body=%s", i, w.Code, w.Body.String())
		}
	}
}

func TestAliasValidation(t *testing.T) {
	a, d := setupAPI(t)
	uid, _ := store.CreateUpstream(d, &store.Upstream{Name: "u1", BaseURL: "b", APIKey: "k", Format: "openai"})

	cases := []map[string]any{
		{"name": "", "bindings": []map[string]any{{"upstream_id": uid, "model_name": "m"}}}, // 缺 name
		{"name": "x"}, // 缺 bindings
		{"name": "x", "bindings": []map[string]any{{"upstream_id": 9999, "model_name": "m"}}}, // 上游不存在
		{"name": "x", "bindings": []map[string]any{{"upstream_id": uid, "model_name": ""}}},   // 缺 model_name
		{"name": "x", "bindings": []map[string]any{{"upstream_id": 0, "model_name": "m"}}},    // 缺 upstream_id
	}
	for i, c := range cases {
		w := doAliasReq(t, a, "POST", "/api/admin/aliases", c)
		if w.Code != 400 {
			t.Fatalf("case %d: status=%d want 400, body=%s", i, w.Code, w.Body.String())
		}
	}
}
