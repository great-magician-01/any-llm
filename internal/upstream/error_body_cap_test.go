package upstream

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 业务规则：上游错误响应体的读取必须有上限（1 MiB，与 fetch.go / balance.go 一致）。
// 厂商的错误页可能有几 MB，无上限读进内存、再经网关原样回给客户端没有收益；
// 同时「512 截断」只用于日志行，所以对小 body 的 message 提取必须毫无影响。
func TestCallBiz_ErrorBodyReadIsBounded(t *testing.T) {
	t.Run("超大错误页被上限截断", func(t *testing.T) {
		const huge = 3 << 20 // 3 MiB
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(500)
			// 一个超大的合法 JSON 错误体：message 字段本身就有几 MB。
			io.WriteString(w, `{"error":{"message":"`)
			io.WriteString(w, strings.Repeat("x", huge))
			io.WriteString(w, `","type":"server_error"}}`)
		}))
		defer srv.Close()

		c := NewClient(srv.Client())
		_, err := c.Call(context.Background(), bizUpstream(srv.URL, "openai", "sk"), bizTextReq(false), nil)
		var ue *UpstreamError
		if !errors.As(err, &ue) {
			t.Fatalf("error type = %T (%v), want *UpstreamError", err, err)
		}
		if ue.StatusCode != 500 {
			t.Errorf("status = %d, want 500", ue.StatusCode)
		}
		if len(ue.Body) > maxUpstreamErrorBody {
			t.Fatalf("UpstreamError.Body = %d bytes, want <= %d（无上限读取会把几 MB 的错误页整个读进内存）",
				len(ue.Body), maxUpstreamErrorBody)
		}
		// 被截断的 body 已经不是合法 JSON 了，Message() 会回退成原文——
		// 关键是它同样有界，不能是那个几 MB 的原文。
		if msg := ue.Message(); len(msg) > maxUpstreamErrorBody {
			t.Errorf("Message() = %d bytes, want bounded by %d", len(msg), maxUpstreamErrorBody)
		}
	})

	t.Run("上限之内的小错误体原样解析", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(429)
			io.WriteString(w, `{"error":{"message":"rate limited","type":"rate_limit_error"}}`)
		}))
		defer srv.Close()

		c := NewClient(srv.Client())
		_, err := c.Call(context.Background(), bizUpstream(srv.URL, "openai", "sk"), bizTextReq(false), nil)
		var ue *UpstreamError
		if !errors.As(err, &ue) {
			t.Fatalf("error type = %T (%v), want *UpstreamError", err, err)
		}
		if got := ue.Message(); got != "rate limited" {
			t.Fatalf("Message() = %q, want rate limited（读上限不能影响正常错误体）", got)
		}
		if got := ue.ErrorType(); got != "rate_limit_error" {
			t.Errorf("ErrorType() = %q, want rate_limit_error", got)
		}
	})
}
