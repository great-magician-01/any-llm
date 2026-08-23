package webapi

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/great-magician-01/any-llm/internal/model"
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
	uid1, _ := model.CreateUpstream(d, &model.Upstream{Name: "u1", BaseURL: "b", APIKey: "k", Format: "openai"})
	uid2, _ := model.CreateUpstream(d, &model.Upstream{Name: "u2", BaseURL: "b", APIKey: "k", Format: "anthropic"})

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
	var created model.ModelAlias
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
		Data []model.ModelAlias `json:"data"`
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
	var updated model.ModelAlias
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

func TestAliasValidation(t *testing.T) {
	a, d := setupAPI(t)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "u1", BaseURL: "b", APIKey: "k", Format: "openai"})

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
