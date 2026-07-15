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

func TestListKeysMasked(t *testing.T) {
	a, d := setupAPI(t)
	k, _ := model.CreateExtKey(d, "l")
	_ = k
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
	key, _ := resp.Data[0]["Key"].(string)
	if !strings.Contains(key, "****") {
		t.Fatalf("key not masked: %q", key)
	}
}

func TestDeleteKey(t *testing.T) {
	a, d := setupAPI(t)
	k, _ := model.CreateExtKey(d, "l")
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
