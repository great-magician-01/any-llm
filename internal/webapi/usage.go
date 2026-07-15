package webapi

import (
	"net/http"
	"strconv"

	"github.com/great-magician-01/any-llm/internal/model"
)

func (a *API) handleUsage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/admin/usage/summary" && r.Method == "GET" {
		groupBy := r.URL.Query().Get("group_by")
		from := r.URL.Query().Get("from")
		to := r.URL.Query().Get("to")
		summaries, err := model.UsageSummaryByGroup(a.db, groupBy, from, to)
		if err != nil {
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
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"data": records, "total": total})
		return
	}
	http.NotFound(w, r)
}
