package upstream

import (
	"context"
	"database/sql"
	"net/http"
	"sync"
	"time"

	"github.com/great-magician-01/any-llm/internal/db"
	"github.com/great-magician-01/any-llm/internal/logger"
	"github.com/great-magician-01/any-llm/internal/model"
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

// NewBalancePoller creates a poller writing through w. It uses its own
// 15s-timeout HTTP client (not the gateway's unbounded one, which is tuned
// for long-lived SSE streams).
func NewBalancePoller(d *sql.DB, w *db.Writer, interval time.Duration) *BalancePoller {
	return &BalancePoller{
		d:        d,
		writer:   w,
		interval: interval,
		client:   &http.Client{Timeout: 15 * time.Second},
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
	upstreams, err := model.ListUpstreams(p.d)
	if err != nil {
		logger.Error("balance poller: list upstreams failed", "err", err)
		return
	}
	for i := range upstreams {
		u := &upstreams[i]
		if !u.Enabled {
			continue
		}
		vendor, payload, err := FetchBalance(ctx, p.client, u)
		if err != nil {
			if BalanceVendor(u) != "" {
				logger.Warn("balance poller: fetch failed", "upstream", u.Name, "id", u.ID, "err", err)
			}
			continue
		}
		snap := &model.BalanceSnapshot{UpstreamID: u.ID, UpstreamName: u.Name, Vendor: vendor, Payload: payload}
		p.writer.DoAsync(func(d *sql.DB) error { return model.InsertBalanceSnapshot(d, snap) })
	}
}
