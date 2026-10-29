package main

import (
	"database/sql"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/great-magician-01/any-llm/internal/gateway"
)

// newMux 组装四类流量：/v1/*（公共网关）、/api/admin/*（HMAC 会话鉴权的管理
// API）、GET /health（未鉴权探活，处理器见 health.go）与 /*（内嵌 SPA）。
//
// SPA 分支承载两条用户可见的缓存契约，改错了很难在本地发现：
//   - assets/ 下的构建产物带内容哈希，可以长缓存（immutable，一年）；
//   - 其余一切（index.html 与客户端路由回退）必须 no-cache，否则发新版本后
//     浏览器会一直用旧的 index.html 去加载已不存在的旧 assets。
//
// 另外：找不到路径且**带扩展名**时返回 404，而不是回退成 index.html —— 把 HTML
// 当 JS/CSS 返回会让浏览器报一堆语法错误，掩盖真正的 404。
func newMux(frontendFS fs.FS, gatewayHandler, adminHandler http.Handler, d *sql.DB) *http.ServeMux {
	spa := http.FileServer(http.FS(frontendFS))

	mux := http.NewServeMux()
	mux.Handle("/v1/", gateway.LoggingMiddleware(gatewayHandler, "gateway"))
	mux.Handle("/api/admin/", gateway.LoggingMiddleware(adminHandler, "admin"))
	// /health 刻意不包日志中间件：探活高频轮询，逐条进日志只是噪音。
	mux.Handle("GET /health", newHealthHandler(d))
	// 非 GET/HEAD 也必须留在健康端点的语义内；否则会被下面的 SPA
	// catch-all 接管，在 DB 已断开时仍返回 200 index.html，造成假健康。
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
	})
	// Go 1.22+: "/" only matches the exact root path; use /{$} for
	// that and /{pathname...} as the catch-all.
	mux.HandleFunc("/{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, must-revalidate")
		r.URL.Path = "/"
		spa.ServeHTTP(w, r)
	})
	mux.HandleFunc("/{pathname...}", func(w http.ResponseWriter, r *http.Request) {
		rel := r.PathValue("pathname")
		if rel != "" {
			if f, err := frontendFS.Open(rel); err == nil {
				f.Close()
				if strings.HasPrefix(rel, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				spa.ServeHTTP(w, r)
				return
			}
			if path.Ext(rel) != "" {
				http.NotFound(w, r)
				return
			}
		}
		w.Header().Set("Cache-Control", "no-cache, must-revalidate")
		r.URL.Path = "/"
		spa.ServeHTTP(w, r)
	})
	return mux
}
