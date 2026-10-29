package upstream

import (
	"context"
	"database/sql"
	"net/http"
	"sync"
	"time"

	"github.com/great-magician-01/any-llm/internal/db"
	"github.com/great-magician-01/any-llm/internal/logger"
	"github.com/great-magician-01/any-llm/internal/store"
)

// BalancePoller periodically snapshots vendor balance/quota for every
// supported, enabled upstream into the balance_snapshots table. Lifecycle
// mirrors db.Writer: Start launches the loop goroutine, Stop signals it and
// waits for it to exit.
type BalancePoller struct {
	d        *sql.DB
	writer   *db.Writer
	interval time.Duration
	client   *http.Client
	stopCh   chan struct{}
	wg       sync.WaitGroup
	mu       sync.Mutex
	stopped  bool
}

// NewBalancePoller creates a poller writing through w. It uses the shared
// VendorClient (15s timeout) — not the gateway's unbounded one, which is
// tuned for long-lived SSE streams.
func NewBalancePoller(d *sql.DB, w *db.Writer, interval time.Duration) *BalancePoller {
	return &BalancePoller{
		d:        d,
		writer:   w,
		interval: interval,
		client:   VendorClient,
		stopCh:   make(chan struct{}),
	}
}

// Start launches the polling loop. interval <= 0 disables periodic polling
// (manual refresh via the admin API still works) and Start is a no-op.
func (p *BalancePoller) Start() {
	if p.interval <= 0 {
		return
	}
	p.wg.Add(1)
	go p.loop()
}

func (p *BalancePoller) loop() {
	defer p.wg.Done()
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			p.PollOnce(context.Background())
		case <-p.stopCh:
			return
		}
	}
}

// Stop terminates the polling loop and waits for it to exit. Safe to call
// when Start was a no-op, and idempotent.
func (p *BalancePoller) Stop() {
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return
	}
	p.stopped = true
	close(p.stopCh)
	p.mu.Unlock()
	p.wg.Wait()
}

// PollOnce fetches and archives one snapshot per supported, enabled upstream.
// Failures (vendor unreachable, disabled, unsupported) only log and skip that
// upstream. Also used for the manual refresh and the one-shot poll at boot.
func (p *BalancePoller) PollOnce(ctx context.Context) {
	upstreams, err := store.ListUpstreams(p.d, nil)
	if err != nil {
		logger.Error("balance poller: list upstreams failed", "err", err)
		return
	}
	for i := range upstreams {
		u := &upstreams[i]
		// 禁用与已过有效期的上游都不拉取：后者等同失效，别再浪费厂商调用
		if !u.Enabled || u.Expired(time.Now()) {
			continue
		}
		p.pollUpstream(ctx, u)
	}
}

// pollUpstream holds both the process-local and database session lock for the
// whole fetch → record sequence. The deferred release covers vendor failures,
// writer failures and successful records alike.
func (p *BalancePoller) pollUpstream(ctx context.Context, u *store.Upstream) {
	unlock, err := LockBalanceUpstream(ctx, p.d, u.ID)
	if err != nil {
		logger.Warn("balance poller: acquire refresh lock failed", "upstream", u.Name, "id", u.ID, "err", err)
		return
	}
	defer func() {
		if err := unlock(); err != nil {
			logger.Warn("balance poller: release refresh lock failed", "upstream", u.Name, "id", u.ID, "err", err)
		}
	}()

	vendor, payload, err := FetchBalance(ctx, p.client, u)
	if err != nil {
		if BalanceVendor(u) != "" {
			logger.Warn("balance poller: fetch failed", "upstream", u.Name, "id", u.ID, "err", err)
		}
		return
	}
	// 只捕获标量进闭包：捕获 u 会把整个 upstreams 切片（含各家 API key）
	// 钉在 writer 的待写队列里
	name, id := u.Name, u.ID
	snap := &store.BalanceSnapshot{UpstreamID: id, UpstreamName: name, Vendor: vendor, Payload: payload}
	// DoSync 而非 DoAsync：快照+记账是账本语义，writer 背压时静默丢弃任务
	// 会在账本上留下缺口（跨充值缺口的消费被吞掉）；poller 是后台循环，
	// 阻塞等待没有代价
	err = p.writer.DoSync(func(d *sql.DB) error {
		// 快照与账单同一事务落库（见 store.RecordBalanceSnapshot）：记账失败
		// 连快照一起回滚，下次抓取仍与旧基线对比，不会丢消费
		n, err := store.RecordBalanceSnapshot(d, snap)
		switch {
		case err != nil:
			logger.Warn("balance poller: record snapshot/bill failed", "upstream", name, "id", id, "err", err)
		case n > 0:
			logger.Info("balance poller: bill recorded", "upstream", name, "id", id, "entries", n)
		}
		return nil
	})
	if err != nil {
		// DoSync 只在 writer 停机时回错：本次轮询放弃，下个周期再试
		logger.Warn("balance poller: writer not accepting writes", "err", err)
	}
}
