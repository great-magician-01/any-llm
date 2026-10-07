package adminapi

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// SQLite 下会话聚合随归档一起关闭：列表返回 disabled 标记，详情返回 400。
func TestConvSessionsDisabledOnSQLite(t *testing.T) {
	a, _ := setupAPI(t)

	req := httptest.NewRequest("GET", "/api/admin/conv-sessions?page=1&size=10", nil)
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

	req = httptest.NewRequest("GET", "/api/admin/conv-sessions/1", nil)
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("status=%d want 400, body=%s", w.Code, w.Body.String())
	}

	// 非法 id → 404；非 GET → 405
	req = httptest.NewRequest("GET", "/api/admin/conv-sessions/abc", nil)
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatalf("status=%d want 404", w.Code)
	}
	req = httptest.NewRequest("POST", "/api/admin/conv-sessions", nil)
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 405 {
		t.Fatalf("status=%d want 405", w.Code)
	}
}
