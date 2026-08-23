package webapi

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// SQLite 下对话归档关闭：列表返回 disabled 标记（前端据此提示需要 PG），
// 详情返回 400。
func TestConversationsDisabledOnSQLite(t *testing.T) {
	a, _ := setupAPI(t)

	req := httptest.NewRequest("GET", "/api/admin/conversations?page=1&size=10", nil)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Data     []any `json:"data"`
		Total    int   `json:"total"`
		Disabled bool  `json:"disabled"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Disabled || resp.Total != 0 || len(resp.Data) != 0 {
		t.Fatalf("resp=%+v", resp)
	}

	req = httptest.NewRequest("GET", "/api/admin/conversations/1", nil)
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("status=%d want 400, body=%s", w.Code, w.Body.String())
	}

	// 非法 id → 404；非 GET → 405
	req = httptest.NewRequest("GET", "/api/admin/conversations/abc", nil)
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatalf("status=%d want 404", w.Code)
	}
	req = httptest.NewRequest("POST", "/api/admin/conversations", nil)
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 405 {
		t.Fatalf("status=%d want 405", w.Code)
	}
}
