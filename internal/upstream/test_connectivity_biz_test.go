package upstream

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// test.go（TestConnectivity）之前 0% 覆盖。三层语义是管理端展示与排障的契约：
//   a) 传输失败  → reachable=false, ok=false（没收到任何 HTTP 应答）
//   b) 非 2xx    → reachable=true,  ok=false（401/403 key 无效、404 无模型列表）
//   c) 2xx + 模型列表 → ok=true 且 models 数量正确
// TestConnectivity 永远返回结果对象（不是 Go error），网络问题也只是结果字段。
// ---------------------------------------------------------------------------

// 业务规则：TestConnectivity 把上游应答分成三层，且永远返回非 nil 的结果对象
// （调用方原样回 JSON，网络错误不是端点错误）。
func TestConnectivityBiz_ThreeLayers(t *testing.T) {
	t.Run("a transport failure is reachable=false ok=false", func(t *testing.T) {
		dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		deadURL := dead.URL
		dead.Close() // 端口随即拒绝连接

		res := TestConnectivity(context.Background(), http.DefaultClient, bizUpstream(deadURL, "openai", "k"))
		if res == nil {
			t.Fatal("TestConnectivity returned nil (must always return a result object)")
		}
		if res.OK {
			t.Fatalf("transport failure must not be ok: %+v", res)
		}
		if res.Reachable {
			t.Fatalf("transport failure must be reachable=false: %+v", res)
		}
		if res.Models != nil {
			t.Fatalf("transport failure must not report models: %+v", res)
		}
		if res.Detail == "" {
			t.Fatalf("transport failure must carry a detail: %+v", res)
		}
		if res.Status != 0 {
			t.Fatalf("transport failure must not report an HTTP status, got %d", res.Status)
		}
		if res.LatencyMs < 0 {
			t.Fatalf("latency must be non-negative, got %d", res.LatencyMs)
		}
	})

	t.Run("b non-2xx is reachable=true ok=false", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(401)
			io.WriteString(w, `{"error":{"message":"invalid api key"}}`)
		}))
		defer srv.Close()

		res := TestConnectivity(context.Background(), http.DefaultClient, bizUpstream(srv.URL, "openai", "bad"))
		if res == nil {
			t.Fatal("nil result")
		}
		if !res.Reachable {
			t.Fatalf("a 401 answer means the host is reachable: %+v", res)
		}
		if res.OK {
			t.Fatalf("401 must not be ok: %+v", res)
		}
		if res.Status != 401 {
			t.Fatalf("status = %d, want 401", res.Status)
		}
		if res.Models != nil {
			t.Fatalf("non-2xx must not report models: %+v", res)
		}
		if !strings.Contains(res.Detail, "invalid api key") {
			t.Fatalf("detail = %q, want the upstream body relayed", res.Detail)
		}
	})

	t.Run("c 2xx models list is ok with the right count", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"object":"list","data":[{"id":"a"},{"id":"b"},{"id":"c"}]}`)
		}))
		defer srv.Close()

		res := TestConnectivity(context.Background(), http.DefaultClient, bizUpstream(srv.URL, "openai", "k"))
		if res == nil {
			t.Fatal("nil result")
		}
		if !res.Reachable || !res.OK {
			t.Fatalf("2xx models list must be ok+reachable: %+v", res)
		}
		if res.Status != 200 {
			t.Fatalf("status = %d, want 200", res.Status)
		}
		if res.Models == nil || *res.Models != 3 {
			t.Fatalf("models = %v, want 3", res.Models)
		}
		if res.Detail != "" {
			t.Fatalf("successful probe should not carry a detail: %q", res.Detail)
		}
	})

	t.Run("d 2xx but not a models list stays ok=false", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `<html><body>hello</body></html>`)
		}))
		defer srv.Close()

		res := TestConnectivity(context.Background(), http.DefaultClient, bizUpstream(srv.URL, "openai", "k"))
		if !res.Reachable || res.OK {
			t.Fatalf("a non-models 2xx body must stay ok=false: %+v", res)
		}
		if res.Status != 200 || res.Detail == "" {
			t.Fatalf("expected status=200 with an explanatory detail: %+v", res)
		}
	})
}

// 业务规则：未知 format 无法构造认证头，必须在发请求前以结果对象报错
// （reachable=false，不 panic、不当作 Go error 抛出）。
func TestConnectivityBiz_UnknownFormat(t *testing.T) {
	res := TestConnectivity(context.Background(), http.DefaultClient, bizUpstream("https://example.com", "gemini", "k"))
	if res == nil {
		t.Fatal("nil result")
	}
	if res.Reachable || res.OK {
		t.Fatalf("unknown format must not be reachable: %+v", res)
	}
	if res.Detail == "" {
		t.Fatalf("unknown format must explain itself: %+v", res)
	}
}
