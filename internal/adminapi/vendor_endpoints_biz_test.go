package adminapi

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/great-magician-01/any-llm/internal/store"
	"github.com/great-magician-01/any-llm/internal/upstream"
)

// ---------------------------------------------------------------------------
// 厂商相关管理端点：余额刷新（balances.go）与连通性测试（upstreams.go +
// upstream/test.go）。helper 前缀 biz。
// ---------------------------------------------------------------------------

const bizDeepSeekJSON = `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"110.00","granted_balance":"10.00","topped_up_balance":"100.00"}]}`

// bizVendorTransport 把「支持的厂商 host」请求改写到本地假服务器：生产代码按
// base-URL 的 host 判定厂商（upstream.BalanceVendor），测试里没法注册假 host，
// 所以保留真实 host、只改传输目标。
type bizVendorTransport struct{ target *url.URL }

func (t bizVendorTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r2 := r.Clone(r.Context())
	r2.URL.Scheme = t.target.Scheme
	r2.URL.Host = t.target.Host
	r2.Host = ""
	return http.DefaultTransport.RoundTrip(r2)
}

// bizRedirectVendorClient 让 balances 的两个 handler 打到假厂商服务器上。
func bizRedirectVendorClient(t *testing.T, srv *httptest.Server) {
	t.Helper()
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	old := balanceClient
	balanceClient = &http.Client{Transport: bizVendorTransport{target: target}, Timeout: 5 * time.Second}
	t.Cleanup(func() { balanceClient = old })
}

// bizNewUpstream 建一个默认启用的上游并读回（拿 id / 默认 enabled）。
func bizNewUpstream(t *testing.T, d *sql.DB, name, baseURL string) *store.Upstream {
	t.Helper()
	id, err := store.CreateUpstream(d, &store.Upstream{Name: name, BaseURL: baseURL, APIKey: "k", Format: "openai", MaxConcurrent: 1})
	if err != nil {
		t.Fatal(err)
	}
	u, err := store.GetUpstreamByID(d, id)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// 业务规则：POST /api/admin/upstreams/:id/balances/refresh —— 厂商失败必须返回
// 502（上游/网关侧问题，不是管理员请求写错），且不得写入任何快照；厂商不受支持
// 返回 400（请求本身不该发）。id 不存在返回 404。
func TestBalancesBiz_RefreshVendorFailure502AndUnsupported400(t *testing.T) {
	t.Run("vendor failure → 502 and no snapshot", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/user/balance" {
				t.Errorf("vendor path = %q, want /user/balance", r.URL.Path)
			}
			w.WriteHeader(500)
			io.WriteString(w, `{"error":"vendor exploded"}`)
		}))
		defer srv.Close()
		bizRedirectVendorClient(t, srv)

		a, d := setupAPI(t)
		uid := bizNewUpstream(t, d, "ds", "https://api.deepseek.com")

		w := doAliasReq(t, a, "POST", "/api/admin/upstreams/"+bizItoa(uid.ID)+"/balances/refresh", nil)
		if w.Code != 502 {
			t.Fatalf("status=%d want 502, body=%s", w.Code, w.Body.String())
		}
		var resp map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if msg, _ := resp["error"].(string); msg == "" || !strings.Contains(msg, "500") {
			t.Fatalf("502 body must explain the vendor status: %+v", resp)
		}
		snaps, err := store.LatestBalanceSnapshots(d)
		if err != nil {
			t.Fatal(err)
		}
		if len(snaps) != 0 {
			t.Fatalf("failed refresh must not archive anything: %+v", snaps)
		}
	})

	t.Run("unsupported vendor → 400", func(t *testing.T) {
		a, d := setupAPI(t)
		uid := bizNewUpstream(t, d, "oai", "https://api.openai.com/v1")

		w := doAliasReq(t, a, "POST", "/api/admin/upstreams/"+bizItoa(uid.ID)+"/balances/refresh", nil)
		if w.Code != 400 {
			t.Fatalf("status=%d want 400, body=%s", w.Code, w.Body.String())
		}
		var resp map[string]any
		json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["error"] == nil {
			t.Fatalf("400 must carry an error message: %+v", resp)
		}
	})

	t.Run("happy path archives the snapshot", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, bizDeepSeekJSON)
		}))
		defer srv.Close()
		bizRedirectVendorClient(t, srv)

		a, d := setupAPI(t)
		uid := bizNewUpstream(t, d, "ds", "https://api.deepseek.com")

		w := doAliasReq(t, a, "POST", "/api/admin/upstreams/"+bizItoa(uid.ID)+"/balances/refresh", nil)
		if w.Code != 200 {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		var resp struct {
			Data store.BalanceSnapshot `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if resp.Data.Vendor != "deepseek" || resp.Data.UpstreamID != uid.ID {
			t.Fatalf("snapshot = %+v", resp.Data)
		}
		if !strings.Contains(string(resp.Data.Payload), `"kind":"balance"`) {
			t.Fatalf("payload not normalized: %s", resp.Data.Payload)
		}
		snaps, err := store.LatestBalanceSnapshots(d)
		if err != nil {
			t.Fatal(err)
		}
		if len(snaps) != 1 {
			t.Fatalf("snapshot not archived: %+v", snaps)
		}
	})
}

// 业务规则：POST /api/admin/balances（页面打开时的全量实时刷新）只为「厂商受支持 +
// 启用 + 未过期」的上游抓取并归档——禁用、已过期、不支持厂商的上游必须被跳过，
// 且跳过发生在发请求之前（假厂商端点命中数只能是 1）。这是对既有
// TestRefreshAllBalances_UnsupportedOnly 的强化版：那个用例只有一个不支持厂商的
// 上游，断言恒真，无法发现「禁用/过期上游照样被拉取」的回归。
func TestBalancesBiz_RefreshAllSkipsDisabledExpiredUnsupported(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		io.WriteString(w, bizDeepSeekJSON)
	}))
	defer srv.Close()
	bizRedirectVendorClient(t, srv)

	a, d := setupAPI(t)

	enabled := bizNewUpstream(t, d, "enabled", "https://api.deepseek.com")

	disabled := bizNewUpstream(t, d, "disabled", "https://api.deepseek.com")
	disabled.Enabled = false
	if err := store.UpdateUpstream(d, disabled); err != nil {
		t.Fatal(err)
	}

	expired := bizNewUpstream(t, d, "expired", "https://api.deepseek.com")
	past := time.Now().Add(-time.Hour)
	expired.ExpiresAt = &past
	if err := store.UpdateUpstream(d, expired); err != nil {
		t.Fatal(err)
	}

	bizNewUpstream(t, d, "unsupported", "https://api.openai.com/v1")

	w := doAliasReq(t, a, "POST", "/api/admin/balances", nil)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Data []store.BalanceSnapshot `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("data=%+v want exactly one snapshot (only enabled+unexpired+supported)", resp.Data)
	}
	if resp.Data[0].UpstreamID != enabled.ID {
		t.Fatalf("snapshot belongs to id=%d, want enabled id=%d", resp.Data[0].UpstreamID, enabled.ID)
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Fatalf("vendor endpoint hit %d times, want 1 (disabled/expired/unsupported must be skipped before the fetch)", n)
	}
	snaps, err := store.LatestBalanceSnapshots(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 1 || snaps[0].UpstreamID != enabled.ID {
		t.Fatalf("archived snapshots=%+v want only the enabled upstream", snaps)
	}
}

// 业务规则：POST /api/admin/upstreams/test 的契约是「连通性差是结果不是端点错误」
// ——传输失败也必须 200，body 是 {ok,reachable,latency_ms,detail} 结果对象，
// 顶层不能出现 error（否则前端会当成请求失败而不是上游不可达）。
func TestBalancesBiz_ConnectivityTestIsAlwaysAResult(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	a, _ := setupAPI(t)
	a.client = upstream.NewClient(http.DefaultClient)
	w := doAliasReq(t, a, "POST", "/api/admin/upstreams/test", map[string]any{
		"base_url": deadURL, "api_key": "k", "format": "openai",
	})
	if w.Code != 200 {
		t.Fatalf("connectivity test must be 200 even when unreachable, got %d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if _, isErr := resp["error"]; isErr {
		t.Fatalf("unreachable upstream must be a result object, not an error: %s", w.Body.String())
	}
	if resp["ok"] != false || resp["reachable"] != false {
		t.Fatalf("transport failure must map to ok=false reachable=false: %+v", resp)
	}
	if detail, _ := resp["detail"].(string); detail == "" {
		t.Fatalf("transport failure must carry a detail: %+v", resp)
	}
	if _, hasModels := resp["models"]; hasModels {
		t.Fatalf("transport failure must not report models: %+v", resp)
	}
}
