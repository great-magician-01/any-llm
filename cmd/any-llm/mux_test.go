package main

import (
	"database/sql"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/great-magician-01/any-llm/internal/db"
)

// testFrontendFS 模拟一次 npm run build 的产物：index.html + 带哈希的 assets。
func testFrontendFS() fs.FS {
	return fstest.MapFS{
		"index.html":    &fstest.MapFile{Data: []byte("<!doctype html><div id=app>")},
		"assets/app.js": &fstest.MapFile{Data: []byte("console.log(1)")},
		"favicon.ico":   &fstest.MapFile{Data: []byte("ico")},
	}
}

// testDB 开一个临时文件库（不用 :memory:，其连接池语义与真实运行不符）。
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := db.OpenSQLite(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// TestNewMux_SPACacheAndFallback 钉住 SPA 的三条用户可见规则：
// index.html/客户端路由必须 no-cache（否则发版后拿到旧页面）、assets 长缓存、
// 缺失的带扩展名资源必须 404 而不是回退成 HTML。
func TestNewMux_SPACacheAndFallback(t *testing.T) {
	mux := newMux(testFrontendFS(), http.NotFoundHandler(), http.NotFoundHandler(), testDB(t))

	cases := []struct {
		name      string
		target    string
		wantCode  int
		wantCache string
		wantBody  string
	}{
		{"根路径回 index.html", "/", 200, "no-cache, must-revalidate", "<div id=app>"},
		{"assets 长缓存", "/assets/app.js", 200, "public, max-age=31536000, immutable", "console.log"},
		{"客户端路由回退", "/upstreams", 200, "no-cache, must-revalidate", "<div id=app>"},
		{"glass 前缀的客户端路由回退", "/glass/keys", 200, "no-cache, must-revalidate", "<div id=app>"},
		{"缺失的 js 必须 404 而不是回退", "/assets/missing.js", 404, "", ""},
		{"缺失的 css 必须 404", "/nope.css", 404, "", ""},
		{"存在的静态文件原样返回", "/favicon.ico", 200, "", "ico"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest("GET", c.target, nil))

			if w.Code != c.wantCode {
				t.Fatalf("GET %s status=%d want %d (body=%q)", c.target, w.Code, c.wantCode, w.Body.String())
			}
			if c.wantCache != "" {
				if got := w.Header().Get("Cache-Control"); got != c.wantCache {
					t.Errorf("GET %s Cache-Control=%q want %q", c.target, got, c.wantCache)
				}
			}
			if c.wantBody != "" && !strings.Contains(w.Body.String(), c.wantBody) {
				t.Errorf("GET %s body=%q want it to contain %q", c.target, w.Body.String(), c.wantBody)
			}
		})
	}
}

// TestNewMux_GatewayAndAdminMounts 断言三类流量真的挂在各自的路径前缀上：
// /v1/* 走网关、/api/admin/* 走管理 API，且两者都被日志中间件包住（中间件
// 会原样透传下一个 handler）。
func TestNewMux_GatewayAndAdminMounts(t *testing.T) {
	var gwHit, adminHit string
	mux := newMux(testFrontendFS(),
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gwHit = r.URL.Path
			w.WriteHeader(http.StatusTeapot)
		}),
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			adminHit = r.URL.Path
			w.WriteHeader(http.StatusAccepted)
		}),
		testDB(t),
	)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/v1/chat/completions", nil))
	if w.Code != http.StatusTeapot || gwHit != "/v1/chat/completions" {
		t.Fatalf("gateway route: status=%d hit=%q", w.Code, gwHit)
	}

	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/admin/upstreams", nil))
	if w.Code != http.StatusAccepted || adminHit != "/api/admin/upstreams" {
		t.Fatalf("admin route: status=%d hit=%q", w.Code, adminHit)
	}
}

// TestNewMux_Health 钉住 /health 的探活契约：DB 正常时 200 ok、DB 断开时
// 503 degraded，且精确匹配（不落进 SPA 回退），GET 模式同时覆盖 HEAD。
func TestNewMux_Health(t *testing.T) {
	d := testDB(t)
	mux := newMux(testFrontendFS(), http.NotFoundHandler(), http.NotFoundHandler(), d)

	assertHealth := func(method string, wantCode int, wantStatus string) {
		t.Helper()
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(method, "/health", nil))
		if w.Code != wantCode {
			t.Fatalf("%s /health status=%d want %d (body=%q)", method, w.Code, wantCode, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), `"status":"`+wantStatus+`"`) {
			t.Errorf("%s /health body=%q want status %q", method, w.Body.String(), wantStatus)
		}
	}

	assertHealth("GET", http.StatusOK, "ok")
	assertHealth("HEAD", http.StatusOK, "ok")

	// DB 关闭后探活必须转 503，而不是把 ping 失败吞成 ok。
	d.Close()
	assertHealth("GET", http.StatusServiceUnavailable, "degraded")
}
