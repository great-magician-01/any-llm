package webapi

import (
	"database/sql"
	"net/http"
	"strconv"
	"time"

	"github.com/great-magician-01/any-llm/internal/logger"
	"github.com/great-magician-01/any-llm/internal/model"
	"github.com/great-magician-01/any-llm/internal/upstream"
)

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
	// Own timeout-bounded client (15s), like the poller's — the gateway
	// client has no timeout because it serves long-lived SSE streams.
	client := &http.Client{Timeout: 15 * time.Second}
	vendor, payload, err := upstream.FetchBalance(r.Context(), client, u)
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
