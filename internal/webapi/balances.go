package webapi

import (
	"database/sql"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/great-magician-01/any-llm/internal/logger"
	"github.com/great-magician-01/any-llm/internal/model"
	"github.com/great-magician-01/any-llm/internal/upstream"
)

// balanceClient is shared by the balance admin handlers for vendor fetches.
// It carries its own 15s timeout — the gateway client has none because it
// serves long-lived SSE streams.
var balanceClient = &http.Client{Timeout: 15 * time.Second}

// refreshAllBalances serves POST /api/admin/balances: fetch every supported,
// enabled upstream's balance/quota right now (concurrently), archive the
// snapshots, and return them. Used for the page-open auto-refresh.
func (a *API) refreshAllBalances(w http.ResponseWriter, r *http.Request) {
	upstreams, err := model.ListUpstreams(a.db)
	if err != nil {
		logger.Error("admin: refresh all balances list upstreams failed", "err", err)
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	snaps := make([]*model.BalanceSnapshot, len(upstreams))
	var wg sync.WaitGroup
	for i := range upstreams {
		u := &upstreams[i]
		if !u.Enabled || upstream.BalanceVendor(u) == "" {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			vendor, payload, err := upstream.FetchBalance(r.Context(), balanceClient, u)
			if err != nil {
				logger.Warn("admin: refresh all balances fetch failed", "upstream", u.Name, "id", u.ID, "err", err)
				return
			}
			snaps[i] = &model.BalanceSnapshot{UpstreamID: u.ID, UpstreamName: u.Name, Vendor: vendor, Payload: payload}
		}()
	}
	wg.Wait()
	out := make([]*model.BalanceSnapshot, 0, len(snaps))
	for _, snap := range snaps {
		if snap == nil {
			continue
		}
		if err := a.writeSync(func(d *sql.DB) error { return model.InsertBalanceSnapshot(d, snap) }); err != nil {
			logger.Error("admin: insert balance snapshot failed", "upstream", snap.UpstreamName, "id", snap.UpstreamID, "err", err)
			continue
		}
		out = append(out, snap)
	}
	writeJSON(w, 200, map[string]any{"data": out})
}

// listLatestBalances serves GET /api/admin/balances: the newest snapshot per
// upstream.
func (a *API) listLatestBalances(w http.ResponseWriter, r *http.Request) {
	snaps, err := model.LatestBalanceSnapshots(a.db)
	if err != nil {
		logger.Error("admin: list latest balances failed", "err", err)
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"data": snaps})
}

// listBalanceHistory serves GET /api/admin/upstreams/:id/balances: one
// upstream's snapshot history, paginated newest-first.
func (a *API) listBalanceHistory(w http.ResponseWriter, r *http.Request, upstreamID int64) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	size, _ := strconv.Atoi(r.URL.Query().Get("size"))
	rows, total, err := model.BalanceSnapshotsList(a.db, upstreamID, page, size)
	if err != nil {
		logger.Error("admin: list balance history failed", "upstream_id", upstreamID, "err", err)
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"data": rows, "total": total})
}

// refreshBalance serves POST /api/admin/upstreams/:id/balances/refresh: fetch
// the vendor balance/quota right now, archive the snapshot, and return it.
func (a *API) refreshBalance(w http.ResponseWriter, r *http.Request, id int64) {
	u, err := model.GetUpstreamByID(a.db, id)
	if err != nil {
		logger.Warn("admin: refresh balance not found", "id", id, "err", err)
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	if upstream.BalanceVendor(u) == "" {
		writeJSON(w, 400, map[string]any{"error": "balance refresh not supported for this upstream"})
		return
	}
	vendor, payload, err := upstream.FetchBalance(r.Context(), balanceClient, u)
	if err != nil {
		logger.Error("admin: refresh balance failed", "upstream", u.Name, "id", id, "err", err)
		writeJSON(w, 502, map[string]any{"error": err.Error()})
		return
	}
	snap := &model.BalanceSnapshot{UpstreamID: u.ID, UpstreamName: u.Name, Vendor: vendor, Payload: payload}
	if err := a.writeSync(func(d *sql.DB) error { return model.InsertBalanceSnapshot(d, snap) }); err != nil {
		logger.Error("admin: insert balance snapshot failed", "upstream", u.Name, "id", id, "err", err)
		writeSyncErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"data": snap})
}
