package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"
)

// dbPingTimeout bounds the /health DB probe: a hung database (e.g. a network
// partition to PG/MySQL) must surface as a clean 503, not as a probe that
// hangs until the orchestrator kills the process.
const dbPingTimeout = 2 * time.Second

// healthPayload 字段顺序固定，方便人肉 curl 检查。
type healthPayload struct {
	Status string `json:"status"`
	DB     string `json:"db"`
}

// newHealthHandler 构造 GET /health 的处理器：进程存活 + DB 连通性。该端点
// 未鉴权（探活探针的惯例），因此只回传 coarse 状态，不透出具体错误 —— ping
// 的报错可能带 DB 主机名。注册处刻意不包日志中间件：探活高频轮询，逐条进
// 日志只会制造噪音（见 mux.go）。
func newHealthHandler(d *sql.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), dbPingTimeout)
		defer cancel()

		payload := healthPayload{Status: "ok", DB: "up"}
		code := http.StatusOK
		if err := d.PingContext(ctx); err != nil {
			payload.Status = "degraded"
			payload.DB = "down"
			code = http.StatusServiceUnavailable
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(payload)
	})
}
