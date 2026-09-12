package webapi

import (
	"net/http/httptest"
	"testing"
)

// TestRouterEdgeCases pins down the ServeMux method-pattern routing: strict
// {id} parsing (the old fmt.Sscanf parseID silently accepted "123abc" as 123)
// and exact path shapes (trailing segments were previously ignored, so e.g.
// DELETE /api/admin/keys/1/extra deleted key 1).
func TestRouterEdgeCases(t *testing.T) {
	a, _ := setupAPI(t)
	h := a.Handler()

	cases := []struct {
		method string
		path   string
		want   int
	}{
		// 非数字 / 非正数 ID → 404（旧实现会把 "123abc" 解析成 123）
		{"GET", "/api/admin/upstreams/abc", 404},
		{"GET", "/api/admin/upstreams/123abc", 404},
		{"GET", "/api/admin/upstreams/0", 404},
		{"GET", "/api/admin/upstreams/-5", 404},
		{"GET", "/api/admin/aliases/xyz", 404},
		{"GET", "/api/admin/usage/key/abc", 404},
		{"GET", "/api/admin/usage/upstream/abc", 404},
		{"GET", "/api/admin/conversations/abc", 404},
		{"DELETE", "/api/admin/upstreams/1/models/abc", 404},

		// 多余的路径段不再被忽略（旧实现 DELETE /keys/1/extra 会删掉 key 1）
		{"DELETE", "/api/admin/keys/1/extra", 404},
		{"GET", "/api/admin/usage/key/1/extra", 404},
		{"DELETE", "/api/admin/upstreams/1/models/2/extra", 404},

		// 尾斜杠不匹配任何模式
		{"GET", "/api/admin/upstreams/", 404},
		{"GET", "/api/admin/keys/", 404},
		{"GET", "/api/admin/usage/", 404},

		// 路径存在但方法不允许 → 405（ServeMux 自动应答）
		{"PUT", "/api/admin/upstreams", 405},
		{"DELETE", "/api/admin/aliases", 405},
		{"PATCH", "/api/admin/keys/1", 405},
		{"POST", "/api/admin/conversations", 405},
		{"POST", "/api/admin/config/export", 405},
		{"GET", "/api/admin/config/import", 405},

		// 未注册路径 → 404
		{"GET", "/api/admin/unknown", 404},
		{"GET", "/api/admin/upstreams/1/unknown", 404},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, c.path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != c.want {
			t.Errorf("%s %s: status=%d want=%d", c.method, c.path, w.Code, c.want)
		}
	}
}
