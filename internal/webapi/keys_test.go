package webapi

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/great-magician-01/any-llm/internal/model"
)

func TestCreateKey(t *testing.T) {
	a, d := setupAPI(t)
	body, _ := json.Marshal(map[string]any{"label": "my-key", "remark": "for tests"})
	req := httptest.NewRequest("POST", "/api/admin/keys", bytes.NewReader(body))
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d", w.Code)
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	key, _ := resp["key"].(string)
	if !strings.HasPrefix(key, "all-sk-") {
		t.Fatalf("key=%q", key)
	}
	if resp["label"] != "my-key" || resp["remark"] != "for tests" {
		t.Fatalf("label/remark not echoed: %v", resp)
	}
	list, _ := model.ListExtKeys(d)
	if len(list) != 1 || list[0].Label != "my-key" || list[0].Remark != "for tests" {
		t.Fatalf("persisted=%+v", list)
	}
}

func TestKeyNameUnique(t *testing.T) {
	a, d := setupAPI(t)
	model.CreateExtKey(d, "taken", "", 0, 0, nil)

	// 建同名 key：400，且不落库
	body, _ := json.Marshal(map[string]any{"label": "taken"})
	req := httptest.NewRequest("POST", "/api/admin/keys", bytes.NewReader(body))
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "already exists") {
		t.Fatalf("duplicate label status=%d body=%s", w.Code, w.Body.String())
	}
	if list, _ := model.ListExtKeys(d); len(list) != 1 {
		t.Fatalf("rejected duplicate must not insert: len=%d", len(list))
	}

	// 改成占用中的名字：400，原名保留
	other, _ := model.CreateExtKey(d, "other", "", 0, 0, nil)
	body, _ = json.Marshal(map[string]any{"label": "taken"})
	req = httptest.NewRequest("PUT", "/api/admin/keys/"+strconv.FormatInt(other.ID, 10), bytes.NewReader(body))
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("rename onto taken label status=%d body=%s", w.Code, w.Body.String())
	}
	if got, _ := model.GetExtKeyByID(d, other.ID); got.Label != "other" {
		t.Fatalf("rejected rename persisted: label=%q", got.Label)
	}

	// 保留自己名字的部分更新不受影响
	body, _ = json.Marshal(map[string]any{"label": "other", "remark": "r"})
	req = httptest.NewRequest("PUT", "/api/admin/keys/"+strconv.FormatInt(other.ID, 10), bytes.NewReader(body))
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("keep own label status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestListKeysFullKey(t *testing.T) {
	a, d := setupAPI(t)
	k, _ := model.CreateExtKey(d, "l", "", 0, 0, nil)
	req := httptest.NewRequest("GET", "/api/admin/keys", nil)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d", w.Code)
	}
	var resp struct {
		Data []map[string]any `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Data) != 1 {
		t.Fatalf("len=%d", len(resp.Data))
	}
	key, _ := resp.Data[0]["key"].(string)
	if key != k.Key {
		t.Fatalf("key=%q want full key %q", key, k.Key)
	}
}

func TestDeleteKey(t *testing.T) {
	a, d := setupAPI(t)
	k, _ := model.CreateExtKey(d, "l", "", 0, 0, nil)
	req := httptest.NewRequest("DELETE", "/api/admin/keys/"+strconv.FormatInt(k.ID, 10), nil)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d", w.Code)
	}
	list, _ := model.ListExtKeys(d)
	if len(list) != 0 {
		t.Fatalf("after delete len=%d", len(list))
	}
}

func TestUpdateKeyLimits(t *testing.T) {
	a, d := setupAPI(t)
	k, _ := model.CreateExtKey(d, "l", "", 0, 0, nil)
	body, _ := json.Marshal(map[string]any{
		"daily_token_limit":   1000,
		"monthly_token_limit": 50000,
		"label":               "renamed",
		"remark":              "renamed remark",
	})
	req := httptest.NewRequest("PUT", "/api/admin/keys/"+strconv.FormatInt(k.ID, 10), bytes.NewReader(body))
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	got, _ := model.GetExtKeyByID(d, k.ID)
	if got.DailyTokenLimit != 1000 || got.MonthlyTokenLimit != 50000 {
		t.Fatalf("limits not persisted: %+v", got)
	}
	if got.Label != "renamed" {
		t.Fatalf("label=%q want renamed", got.Label)
	}
	if got.Remark != "renamed remark" {
		t.Fatalf("remark=%q want renamed remark", got.Remark)
	}
}

func TestUpdateKeyNegativeLimitRejected(t *testing.T) {
	a, d := setupAPI(t)
	k, _ := model.CreateExtKey(d, "l", "", 100, 200, nil)
	body, _ := json.Marshal(map[string]any{"daily_token_limit": -5})
	req := httptest.NewRequest("PUT", "/api/admin/keys/"+strconv.FormatInt(k.ID, 10), bytes.NewReader(body))
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("status=%d want 400", w.Code)
	}
	// existing limits unchanged
	got, _ := model.GetExtKeyByID(d, k.ID)
	if got.DailyTokenLimit != 100 || got.MonthlyTokenLimit != 200 {
		t.Fatalf("limits changed on rejected update: %+v", got)
	}
}

func TestUpdateKeyPartialNoChange(t *testing.T) {
	a, d := setupAPI(t)
	k, _ := model.CreateExtKey(d, "l", "keep-me", 100, 200, nil)
	// Only send label; limits and remark must be preserved
	body, _ := json.Marshal(map[string]any{"label": "only-label"})
	req := httptest.NewRequest("PUT", "/api/admin/keys/"+strconv.FormatInt(k.ID, 10), bytes.NewReader(body))
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	got, _ := model.GetExtKeyByID(d, k.ID)
	if got.DailyTokenLimit != 100 || got.MonthlyTokenLimit != 200 {
		t.Fatalf("limits changed on partial update: %+v", got)
	}
	if got.Label != "only-label" {
		t.Fatalf("label=%q want only-label", got.Label)
	}
	if got.Remark != "keep-me" {
		t.Fatalf("remark=%q want keep-me (untouched by partial update)", got.Remark)
	}
}

func TestCreateKeyAllowedModels(t *testing.T) {
	a, d := setupAPI(t)
	body, _ := json.Marshal(map[string]any{
		"label":          "restricted",
		"allowed_models": []string{" deepseek/deepseek-chat ", "", "gpt", "gpt"},
	})
	req := httptest.NewRequest("POST", "/api/admin/keys", bytes.NewReader(body))
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		AllowedModels []string `json:"allowed_models"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.AllowedModels) != 2 || resp.AllowedModels[0] != "deepseek/deepseek-chat" || resp.AllowedModels[1] != "gpt" {
		t.Fatalf("allowed_models=%v want trimmed+deduped", resp.AllowedModels)
	}
	list, _ := model.ListExtKeys(d)
	if len(list) != 1 || len(list[0].AllowedModels) != 2 {
		t.Fatalf("persisted allowed_models=%v", list)
	}
}

func TestUpdateKeyAllowedModels(t *testing.T) {
	a, d := setupAPI(t)
	k, _ := model.CreateExtKey(d, "l", "", 0, 0, nil)

	// 设置白名单
	body, _ := json.Marshal(map[string]any{"allowed_models": []string{"gpt"}})
	req := httptest.NewRequest("PUT", "/api/admin/keys/"+strconv.FormatInt(k.ID, 10), bytes.NewReader(body))
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("set status=%d body=%s", w.Code, w.Body.String())
	}
	got, _ := model.GetExtKeyByID(d, k.ID)
	if len(got.AllowedModels) != 1 || got.AllowedModels[0] != "gpt" {
		t.Fatalf("set allowed_models=%v", got.AllowedModels)
	}

	// 部分更新不碰白名单
	body, _ = json.Marshal(map[string]any{"label": "only-label"})
	req = httptest.NewRequest("PUT", "/api/admin/keys/"+strconv.FormatInt(k.ID, 10), bytes.NewReader(body))
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("partial status=%d body=%s", w.Code, w.Body.String())
	}
	got, _ = model.GetExtKeyByID(d, k.ID)
	if len(got.AllowedModels) != 1 || got.AllowedModels[0] != "gpt" {
		t.Fatalf("partial update should keep allowlist: %v", got.AllowedModels)
	}

	// 传空数组 = 清除限制
	body, _ = json.Marshal(map[string]any{"allowed_models": []string{}})
	req = httptest.NewRequest("PUT", "/api/admin/keys/"+strconv.FormatInt(k.ID, 10), bytes.NewReader(body))
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("clear status=%d body=%s", w.Code, w.Body.String())
	}
	got, _ = model.GetExtKeyByID(d, k.ID)
	if got.AllowedModels != nil {
		t.Fatalf("cleared allowed_models=%v want nil", got.AllowedModels)
	}
}

func TestUpdateKeyAllowedModelsRejected(t *testing.T) {
	a, d := setupAPI(t)
	k, _ := model.CreateExtKey(d, "l", "", 0, 0, nil)

	tooLong := strings.Repeat("m", 257)
	body, _ := json.Marshal(map[string]any{"allowed_models": []string{tooLong}})
	req := httptest.NewRequest("PUT", "/api/admin/keys/"+strconv.FormatInt(k.ID, 10), bytes.NewReader(body))
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("status=%d want 400", w.Code)
	}
	got, _ := model.GetExtKeyByID(d, k.ID)
	if got.AllowedModels != nil {
		t.Fatalf("allowlist changed on rejected update: %v", got.AllowedModels)
	}
}
