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
	a, _ := setupAPI(t)
	body, _ := json.Marshal(map[string]any{"label": "my-key"})
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
}

func TestListKeysFullKey(t *testing.T) {
	a, d := setupAPI(t)
	k, _ := model.CreateExtKey(d, "l", 0, 0, nil)
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
	k, _ := model.CreateExtKey(d, "l", 0, 0, nil)
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
	k, _ := model.CreateExtKey(d, "l", 0, 0, nil)
	body, _ := json.Marshal(map[string]any{
		"daily_token_limit":   1000,
		"monthly_token_limit": 50000,
		"label":               "renamed",
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
}

func TestUpdateKeyNegativeLimitRejected(t *testing.T) {
	a, d := setupAPI(t)
	k, _ := model.CreateExtKey(d, "l", 100, 200, nil)
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
	k, _ := model.CreateExtKey(d, "l", 100, 200, nil)
	// Only send label; limits must be preserved
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
	k, _ := model.CreateExtKey(d, "l", 0, 0, nil)

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
	k, _ := model.CreateExtKey(d, "l", 0, 0, nil)

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
