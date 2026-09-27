package upstream

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/great-magician-01/any-llm/internal/db"
	"github.com/great-magician-01/any-llm/internal/store"
)

// ---------------------------------------------------------------------------
// balance_poller.go 之前 0% 覆盖。本文件钉住轮询生命周期与「该抓谁/不该抓谁」。
// ---------------------------------------------------------------------------

const bizDeepSeekBody = `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"110.00","granted_balance":"10.00","topped_up_balance":"100.00"}]}`

// bizOpenDB 建一个临时 SQLite 库（与其它包内测试同口径：配置读缓存是进程内的，
// 每个用例都是新库，必须重置）。
func bizOpenDB(t *testing.T) *sql.DB {
	t.Helper()
	store.ResetConfigCache()
	t.Cleanup(store.ResetConfigCache)
	d, err := db.OpenSQLite(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// bizStartWriter 启动 db.Writer 供 poller 归档快照（PollOnce 走 DoAsync）。
func bizStartWriter(t *testing.T, d *sql.DB) *db.Writer {
	t.Helper()
	w := db.NewWriter(d, 128)
	w.Start()
	t.Cleanup(w.Stop)
	return w
}

// bizVendorServer 起一个假的厂商余额端点，并把它的 host 注册成 deepseek 厂商，
// 返回命中计数（用于断言「跳过」是真的没发请求）。
func bizVendorServer(t *testing.T, status int, body string) (*httptest.Server, *int32) {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	registerTestHost(t, srv.URL, VendorDeepSeek)
	return srv, &hits
}

// bizCreateUpstream 建一个默认启用的上游并读回（CreateUpstream 不含 enabled 列，
// 读回才能拿到 DB 默认值与 id）。
func bizCreateUpstream(t *testing.T, d *sql.DB, name, baseURL string) *store.Upstream {
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

func bizSaveUpstream(t *testing.T, d *sql.DB, u *store.Upstream) {
	t.Helper()
	if err := store.UpdateUpstream(d, u); err != nil {
		t.Fatal(err)
	}
}

// bizWaitSnapshots 等异步写入落地（DoAsync），最多等 timeout。
func bizWaitSnapshots(t *testing.T, d *sql.DB, want int, timeout time.Duration) []store.BalanceSnapshot {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		snaps, err := store.LatestBalanceSnapshots(d)
		if err != nil {
			t.Fatal(err)
		}
		if len(snaps) >= want || time.Now().After(deadline) {
			return snaps
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// 业务规则：interval<=0（ANY_LLM_BALANCE_INTERVAL=0，文档承诺「禁用所有自动抓取，
// 手动刷新仍可用」）时 Start 必须是 no-op——不轮询、不抓取、不写快照；此时 Stop
// 仍要安全且幂等（Start 没起 goroutine，Stop 不能被自己阻塞/panic）。
func TestBalancePollerBiz_ZeroIntervalStartIsNoop(t *testing.T) {
	d := bizOpenDB(t)
	w := bizStartWriter(t, d)
	srv, hits := bizVendorServer(t, 200, bizDeepSeekBody)
	bizCreateUpstream(t, d, "ds", srv.URL)

	p := NewBalancePoller(d, w, 0)
	t.Cleanup(p.Stop)
	p.Start()
	p.Start() // 反复 Start 也不该起多个循环
	time.Sleep(250 * time.Millisecond)
	p.Stop()
	p.Stop() // 幂等

	if n := atomic.LoadInt32(hits); n != 0 {
		t.Fatalf("interval=0 must not fetch, vendor hit %d times", n)
	}
	if snaps, err := store.LatestBalanceSnapshots(d); err != nil {
		t.Fatal(err)
	} else if len(snaps) != 0 {
		t.Fatalf("interval=0 must not archive snapshots, got %+v", snaps)
	}
}

// 业务规则：interval>0 时 Start 后到点执行抓取并归档；Stop 之后必须立刻停止
// （不再发任何厂商请求），且 Stop 幂等。
func TestBalancePollerBiz_StartPollsThenStops(t *testing.T) {
	d := bizOpenDB(t)
	w := bizStartWriter(t, d)
	srv, hits := bizVendorServer(t, 200, bizDeepSeekBody)
	bizCreateUpstream(t, d, "ds", srv.URL)

	p := NewBalancePoller(d, w, 50*time.Millisecond)
	t.Cleanup(p.Stop)
	p.Start()

	snaps := bizWaitSnapshots(t, d, 1, 3*time.Second)
	if len(snaps) != 1 {
		t.Fatalf("expected one archived snapshot, got %+v", snaps)
	}
	if snaps[0].Vendor != VendorDeepSeek {
		t.Fatalf("snapshot vendor = %q, want %q", snaps[0].Vendor, VendorDeepSeek)
	}
	if n := atomic.LoadInt32(hits); n < 1 {
		t.Fatalf("poller never hit the vendor endpoint (hits=%d)", n)
	}

	p.Stop()
	p.Stop() // 幂等：第二次不能 panic/阻塞
	after := atomic.LoadInt32(hits)
	time.Sleep(250 * time.Millisecond)
	if n := atomic.LoadInt32(hits); n != after {
		t.Fatalf("poller kept fetching after Stop: %d -> %d", after, n)
	}
}

// 业务规则：PollOnce（定时轮询、启动时的一次性抓取、手动刷新共用）只为「厂商受支持
// + 启用 + 未过期」的上游写 balance_snapshots；禁用、已过期、不支持厂商的上游一律
// 跳过——且跳过必须发生在发请求之前（厂商端点命中数只能是 1）。
func TestBalancePollerBiz_PollOnceSkipsDisabledExpiredUnsupported(t *testing.T) {
	d := bizOpenDB(t)
	w := bizStartWriter(t, d)
	srv, hits := bizVendorServer(t, 200, bizDeepSeekBody)

	enabled := bizCreateUpstream(t, d, "enabled", srv.URL)

	disabled := bizCreateUpstream(t, d, "disabled", srv.URL)
	disabled.Enabled = false
	bizSaveUpstream(t, d, disabled)

	expired := bizCreateUpstream(t, d, "expired", srv.URL)
	past := time.Now().Add(-time.Hour)
	expired.ExpiresAt = &past
	bizSaveUpstream(t, d, expired)

	// 支持的厂商之外（host 不在 vendor 表里）：连请求都不该发
	bizCreateUpstream(t, d, "unsupported", "https://api.openai.com/v1")

	p := NewBalancePoller(d, w, time.Hour)
	t.Cleanup(p.Stop)
	p.PollOnce(context.Background())

	snaps := bizWaitSnapshots(t, d, 1, 2*time.Second)
	if len(snaps) != 1 {
		t.Fatalf("snapshots = %+v, want exactly one (only the enabled+supported upstream)", snaps)
	}
	if snaps[0].UpstreamID != enabled.ID || snaps[0].UpstreamName != "enabled" {
		t.Fatalf("snapshot belongs to %q(id=%d), want enabled(id=%d)", snaps[0].UpstreamName, snaps[0].UpstreamID, enabled.ID)
	}
	if n := atomic.LoadInt32(hits); n != 1 {
		t.Fatalf("vendor endpoint hit %d times, want 1 (disabled/expired/unsupported must be skipped before the fetch)", n)
	}
}
