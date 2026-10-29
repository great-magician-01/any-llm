package upstream

import (
	"context"
	"database/sql"
	"encoding/json"
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

// 业务规则：PollOnce 的「归档快照 + 余额联动记账」是一条链——两次抓取之间余额
// 下降时账单必须落库（DoSync 保证任务不丢；RecordBalanceSnapshot 保证快照与
// 账单同进退）。真厂商 fetch 用假服务器替代。
func TestBalancePollerBiz_PollOnceRecordsBills(t *testing.T) {
	d := bizOpenDB(t)
	w := bizStartWriter(t, d)
	var body atomic.Value
	body.Store(bizDeepSeekBody)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, body.Load().(string))
	}))
	t.Cleanup(srv.Close)
	registerTestHost(t, srv.URL, VendorDeepSeek)
	bizCreateUpstream(t, d, "ds", srv.URL)

	p := NewBalancePoller(d, w, time.Hour)
	t.Cleanup(p.Stop)
	p.PollOnce(context.Background()) // 首次：只有基线快照，不记账

	body.Store(`{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"108.35","granted_balance":"10.00","topped_up_balance":"98.35"}]}`)
	p.PollOnce(context.Background()) // 余额下降 → 自动记账

	// PollOnce 走 DoSync，返回即落库，无需等待异步写入
	bills, total, err := store.BillsList(d, nil, time.Time{}, time.Time{}, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || bills[0].AmountMicros != 1_650_000 || bills[0].Kind != store.BillKindAuto {
		t.Fatalf("bills=%+v (total=%d), want one auto bill of 1.65", bills, total)
	}
}

// 同一上游的并发 PollOnce 必须把抓取与入账作为一个临界区：旧响应不能在新响应
// 之后落库，否则充值后的旧余额会被推导成额外支出。
func TestBalancePollerBiz_ConcurrentPollsSerializeFetchAndRecord(t *testing.T) {
	d := bizOpenDB(t)
	w := bizStartWriter(t, d)
	firstStarted := make(chan struct{})
	secondStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch atomic.AddInt32(&calls, 1) {
		case 1:
			close(firstStarted)
			<-releaseFirst
			io.WriteString(w, `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"90.00"}]}`)
		default:
			close(secondStarted)
			io.WriteString(w, `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"110.00"}]}`)
		}
	}))
	t.Cleanup(srv.Close)
	registerTestHost(t, srv.URL, VendorDeepSeek)
	u := bizCreateUpstream(t, d, "ds", srv.URL)
	baseline := &store.BalanceSnapshot{
		UpstreamID: u.ID, UpstreamName: u.Name, Vendor: VendorDeepSeek,
		Payload: json.RawMessage(`{"kind":"balance","balances":[{"currency":"CNY","total":"100.00"}]}`),
	}
	if err := store.InsertBalanceSnapshot(d, baseline); err != nil {
		t.Fatal(err)
	}

	p := NewBalancePoller(d, w, time.Hour)
	t.Cleanup(p.Stop)
	done := make(chan struct{}, 2)
	go func() { p.PollOnce(context.Background()); done <- struct{}{} }()
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		close(releaseFirst)
		t.Fatal("first poll did not reach vendor")
	}
	go func() { p.PollOnce(context.Background()); done <- struct{}{} }()

	overlapped := false
	select {
	case <-secondStarted:
		// 旧响应还没落库时第二个请求就抓取，正是会产生错账的交错。
		overlapped = true
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseFirst)
	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("concurrent polls did not finish")
		}
	}
	if overlapped {
		t.Fatal("same-upstream fetch started before the earlier fetch was recorded")
	}
	// 实际变化是 100 → 90（支出 10）→ 110（充值 20），只能记 10。
	// 若 110 抢先落库，再写 90，就会错误地记成 20。
	bills, total, err := store.BillsList(d, nil, time.Time{}, time.Time{}, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || bills[0].AmountMicros != 10_000_000 {
		t.Fatalf("bills=%+v total=%d, want one 10.00 bill", bills, total)
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
