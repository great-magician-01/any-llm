package webapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/great-magician-01/any-llm/internal/logger"
	"github.com/great-magician-01/any-llm/internal/model"
)

func (a *API) handleUsage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/admin/usage/summary" && r.Method == "GET" {
		groupBy := r.URL.Query().Get("group_by")
		from := r.URL.Query().Get("from")
		to := r.URL.Query().Get("to")
		summaries, err := model.UsageSummaryByGroup(a.db, groupBy, from, to)
		if err != nil {
			logger.Error("admin: usage summary failed", "group_by", groupBy, "err", err)
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"data": summaries})
		return
	}
	if r.URL.Path == "/api/admin/usage/records" && r.Method == "GET" {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		size, _ := strconv.Atoi(r.URL.Query().Get("size"))
		records, total, err := model.UsageRecordsList(a.db, page, size)
		if err != nil {
			logger.Error("admin: usage records list failed", "page", page, "size", size, "err", err)
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"data": records, "total": total})
		return
	}
	if r.URL.Path == "/api/admin/usage/daily" && r.Method == "GET" {
		days, _ := strconv.Atoi(r.URL.Query().Get("days"))
		from := r.URL.Query().Get("from")
		to := r.URL.Query().Get("to")
		stats, err := model.UsageDailyStats(a.db, days, from, to)
		if err != nil {
			logger.Error("admin: usage daily stats failed", "days", days, "from", from, "to", to, "err", err)
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"data": stats})
		return
	}
	// GET /api/admin/usage/key/:id    — daily/monthly token totals for an ext key
	// GET /api/admin/usage/upstream/:id — daily/monthly token totals for an upstream
	if r.Method == "GET" {
		if strings.HasPrefix(r.URL.Path, "/api/admin/usage/key/") {
			id := parseID(strings.TrimPrefix(r.URL.Path, "/api/admin/usage/key/"))
			if id == 0 {
				http.NotFound(w, r)
				return
			}
			writeUsageTotals(w, a, &id, nil)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/admin/usage/upstream/") {
			id := parseID(strings.TrimPrefix(r.URL.Path, "/api/admin/usage/upstream/"))
			if id == 0 {
				http.NotFound(w, r)
				return
			}
			writeUsageTotals(w, a, nil, &id)
			return
		}
	}
	http.NotFound(w, r)
}

func writeUsageTotals(w http.ResponseWriter, a *API, extKeyID, upstreamID *int64) {
	now := time.Now()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	dayEnd := dayStart.Add(24 * time.Hour)
	monthEnd := monthStart.AddDate(0, 1, 0)
	daily, err := model.SumTokens(a.db, extKeyID, upstreamID, dayStart, dayEnd)
	if err != nil {
		logger.Error("admin: usage daily totals failed", "ext_key_id", extKeyID, "upstream_id", upstreamID, "err", err)
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	monthly, err := model.SumTokens(a.db, extKeyID, upstreamID, monthStart, monthEnd)
	if err != nil {
		logger.Error("admin: usage monthly totals failed", "ext_key_id", extKeyID, "upstream_id", upstreamID, "err", err)
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{
		"daily_tokens":   daily,
		"monthly_tokens": monthly,
	})
}
