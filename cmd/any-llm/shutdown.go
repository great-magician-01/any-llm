package main

import (
	"context"
	"net/http"
	"time"

	"github.com/great-magician-01/any-llm/internal/logger"
)

// drainAndClose 在 timeout 内排空在途请求，超时则强制关闭连接。
//
// 关服契约（见 AGENTS.md 的 graceful shutdown）：收到 SIGTERM/SIGINT 后给在途
// 请求（含长连接 SSE）一个有限的排空窗口，超时后强制关闭——绝不能无限等待，
// 否则一个挂着的流会让进程永远退不掉。返回是否走了强制关闭这条路。
func drainAndClose(srv *http.Server, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		// Shutdown only fails when the drain deadline passes (e.g. long-lived
		// streams still open); force-close and exit cleanly.
		logger.Warn("graceful drain timed out, force-closing connections", "err", err)
		_ = srv.Close()
		return true
	}
	return false
}
