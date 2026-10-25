package adminapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/great-magician-01/any-llm/internal/logger"
	"github.com/great-magician-01/any-llm/internal/store"
)

// usageSummary serves GET /api/admin/usage/summary?group_by=&from=&to=.
func (a *API) usageSummary(w http.ResponseWriter, r *http.Request) {
	groupBy := r.URL.Query().Get("group_by")
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")
	summaries, err := store.UsageSummaryByGroup(a.db, groupBy, from, to)
	if err != nil {
		if errors.Is(err, store.ErrInvalidGroupBy) {
			writeJSON(w, 400, map[string]any{"error": err.Error()})
			return
		}
		logger.Error("admin: usage summary failed", "group_by", groupBy, "err", err)
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"data": summaries})
}

// usageRecords serves GET /api/admin/usage/records?page=&size=&model=.
// model 非空时按模型精确筛选（请求明细页的模型下拉框）。
func (a *API) usageRecords(w http.ResponseWriter, r *http.Request) {
	page, size := pageParams(r)
	model := r.URL.Query().Get("model")
	records, total, err := store.UsageRecordsList(a.db, page, size, model)
	if err != nil {
		logger.Error("admin: usage records list failed", "page", page, "size", size, "model", model, "err", err)
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"data": records, "total": total})
}

// usageModels serves GET /api/admin/usage/models — usage_records 里出现过的
// 模型名去重清单，作为请求明细筛选下拉框的选项。
func (a *API) usageModels(w http.ResponseWriter, r *http.Request) {
	models, err := store.UsageModels(a.db)
	if err != nil {
		logger.Error("admin: usage models list failed", "err", err)
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"data": models})
}

// usageDaily serves GET /api/admin/usage/daily?days=&from=&to=.
func (a *API) usageDaily(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")
	stats, err := store.UsageDailyStats(a.db, days, from, to)
	if err != nil {
		logger.Error("admin: usage daily stats failed", "days", days, "from", from, "to", to, "err", err)
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"data": stats})
}

// usageKeyTotals serves GET /api/admin/usage/key/{id} — daily/monthly token
// totals for an ext key.
func (a *API) usageKeyTotals(w http.ResponseWriter, r *http.Request, id int64) {
	writeUsageTotals(w, a, &id, nil)
}

// usageUpstreamTotals serves GET /api/admin/usage/upstream/{id} — daily/monthly
// token totals for an upstream.
func (a *API) usageUpstreamTotals(w http.ResponseWriter, r *http.Request, id int64) {
	writeUsageTotals(w, a, nil, &id)
}

func writeUsageTotals(w http.ResponseWriter, a *API, extKeyID, upstreamID *int64) {
	dayStart, dayEnd, monthStart, monthEnd := store.TokenWindows(time.Now())
	daily, err := store.SumTokens(a.db, extKeyID, upstreamID, dayStart, dayEnd)
	if err != nil {
		logger.Error("admin: usage daily totals failed", "ext_key_id", extKeyID, "upstream_id", upstreamID, "err", err)
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	monthly, err := store.SumTokens(a.db, extKeyID, upstreamID, monthStart, monthEnd)
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
