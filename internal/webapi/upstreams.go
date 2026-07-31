package webapi

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/great-magician-01/any-llm/internal/logger"
	"github.com/great-magician-01/any-llm/internal/model"
	"github.com/great-magician-01/any-llm/internal/upstream"
)

func (a *API) listUpstreams(w http.ResponseWriter, r *http.Request) {
	list, err := model.ListUpstreams(a.db)
	if err != nil {
		logger.Error("admin: list upstreams failed", "err", err)
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	for i := range list {
		list[i].APIKey = mask(list[i].APIKey)
	}
	writeJSON(w, 200, map[string]any{"data": list})
}

func (a *API) createUpstream(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name              string `json:"name"`
		BaseURL           string `json:"base_url"`
		APIKey            string `json:"api_key"`
		Format            string `json:"format"`
		DailyTokenLimit   int    `json:"daily_token_limit"`
		MonthlyTokenLimit int    `json:"monthly_token_limit"`
		FetchModels       bool   `json:"fetch_models"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		logger.Warn("admin: create upstream invalid JSON", "err", err)
		writeJSON(w, 400, map[string]any{"error": "invalid JSON"})
		return
	}
	if req.Format != "openai" && req.Format != "anthropic" && req.Format != "responses" {
		logger.Warn("admin: create upstream invalid format", "format", req.Format)
		writeJSON(w, 400, map[string]any{"error": "format must be openai, anthropic or responses"})
		return
	}
	if req.DailyTokenLimit < 0 || req.MonthlyTokenLimit < 0 {
		logger.Warn("admin: create upstream negative token limit", "daily", req.DailyTokenLimit, "monthly", req.MonthlyTokenLimit)
		writeJSON(w, 400, map[string]any{"error": "token limits must be >= 0"})
		return
	}
	u := &model.Upstream{Name: req.Name, BaseURL: req.BaseURL, APIKey: req.APIKey, Format: req.Format,
		DailyTokenLimit: req.DailyTokenLimit, MonthlyTokenLimit: req.MonthlyTokenLimit}
	var id int64
	if err := a.writeSync(func(d *sql.DB) error {
		var e error
		id, e = model.CreateUpstream(d, u)
		return e
	}); err != nil {
		logger.Error("admin: create upstream DB write failed", "name", req.Name, "err", err)
		writeSyncErr(w, 400, err)
		return
	}
	if req.FetchModels && a.client != nil {
		u.ID = id
		names, err := upstream.FetchModels(r.Context(), a.client.HTTP(), u)
		if err == nil {
			a.writeSync(func(d *sql.DB) error { return model.ReplaceModels(d, id, names) })
		} else {
			logger.Warn("admin: create upstream fetch models failed", "name", req.Name, "id", id, "err", err)
		}
	}
	u, _ = model.GetUpstreamByID(a.db, id)
	u.APIKey = mask(u.APIKey)
	writeJSON(w, 200, u)
}

func (a *API) getUpstream(w http.ResponseWriter, r *http.Request, id int64) {
	u, err := model.GetUpstreamByID(a.db, id)
	if err != nil {
		logger.Warn("admin: get upstream not found", "id", id, "err", err)
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	u.APIKey = mask(u.APIKey)
	writeJSON(w, 200, u)
}

func (a *API) updateUpstream(w http.ResponseWriter, r *http.Request, id int64) {
	u, err := model.GetUpstreamByID(a.db, id)
	if err != nil {
		logger.Warn("admin: update upstream not found", "id", id, "err", err)
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	var req struct {
		Name              string `json:"name"`
		BaseURL           string `json:"base_url"`
		APIKey            string `json:"api_key"`
		Format            string `json:"format"`
		DailyTokenLimit   *int   `json:"daily_token_limit"`
		MonthlyTokenLimit *int   `json:"monthly_token_limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		logger.Warn("admin: update upstream invalid JSON", "id", id, "err", err)
		writeJSON(w, 400, map[string]any{"error": "invalid JSON"})
		return
	}
	if req.DailyTokenLimit != nil && *req.DailyTokenLimit < 0 {
		writeJSON(w, 400, map[string]any{"error": "daily_token_limit must be >= 0"})
		return
	}
	if req.MonthlyTokenLimit != nil && *req.MonthlyTokenLimit < 0 {
		writeJSON(w, 400, map[string]any{"error": "monthly_token_limit must be >= 0"})
		return
	}
	if req.Name != "" {
		u.Name = req.Name
	}
	if req.BaseURL != "" {
		u.BaseURL = req.BaseURL
	}
	// Skip API key update when:
	//   - the client sent an empty value (standard "no change" signal), or
	//   - the client sent back the masked placeholder returned by listUpstreams
	//     (e.g. "sk-y****T5qX"). Without this guard, editing an upstream in the
	//     admin UI would overwrite the real key with the masked display string.
	if req.APIKey != "" && !isMaskedKey(req.APIKey) {
		u.APIKey = req.APIKey
	}
	if req.Format != "" && req.Format != "openai" && req.Format != "anthropic" && req.Format != "responses" {
		logger.Warn("admin: update upstream invalid format", "format", req.Format)
		writeJSON(w, 400, map[string]any{"error": "format must be openai, anthropic or responses"})
		return
	}
	if req.Format != "" {
		u.Format = req.Format
	}
	if req.DailyTokenLimit != nil {
		u.DailyTokenLimit = *req.DailyTokenLimit
	}
	if req.MonthlyTokenLimit != nil {
		u.MonthlyTokenLimit = *req.MonthlyTokenLimit
	}
	if err := a.writeSync(func(d *sql.DB) error { return model.UpdateUpstream(d, u) }); err != nil {
		logger.Error("admin: update upstream DB write failed", "id", id, "name", u.Name, "err", err)
		writeSyncErr(w, 400, err)
		return
	}
	u.APIKey = mask(u.APIKey)
	writeJSON(w, 200, u)
}

func (a *API) deleteUpstream(w http.ResponseWriter, r *http.Request, id int64) {
	if err := a.writeSync(func(d *sql.DB) error { return model.DeleteUpstream(d, id) }); err != nil {
		logger.Error("admin: delete upstream failed", "id", id, "err", err)
		writeSyncErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *API) fetchModels(w http.ResponseWriter, r *http.Request, id int64) {
	u, err := model.GetUpstreamByID(a.db, id)
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	if a.client == nil {
		writeJSON(w, 500, map[string]any{"error": "upstream client not configured"})
		return
	}
	names, err := upstream.FetchModels(r.Context(), a.client.HTTP(), u)
	if err != nil {
		logger.Error("admin: fetch models failed", "upstream", u.Name, "id", id, "err", err)
		writeJSON(w, 502, map[string]any{"error": err.Error()})
		return
	}
	if err := a.writeSync(func(d *sql.DB) error { return model.ReplaceModels(d, id, names) }); err != nil {
		logger.Error("admin: replace models DB write failed", "upstream", u.Name, "id", id, "err", err)
		writeSyncErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"models": names})
}

func (a *API) handleModels(w http.ResponseWriter, r *http.Request, upstreamID int64, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case "GET":
			models, err := model.ListModels(a.db, upstreamID)
			if err != nil {
				logger.Error("admin: list models failed", "upstream_id", upstreamID, "err", err)
				writeJSON(w, 500, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, 200, map[string]any{"data": models})
		case "POST":
			var req struct {
				ModelName       string `json:"model_name"`
				ContextLength   int    `json:"context_length"`
				MaxOutputLength int    `json:"max_output_length"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			if req.ContextLength < 0 || req.MaxOutputLength < 0 {
				writeJSON(w, 400, map[string]any{"error": "lengths must be >= 0"})
				return
			}
			if err := a.writeSync(func(d *sql.DB) error {
				return model.AddModel(d, upstreamID, req.ModelName, true, req.ContextLength, req.MaxOutputLength)
			}); err != nil {
				logger.Error("admin: add model failed", "upstream_id", upstreamID, "model", req.ModelName, "err", err)
				writeSyncErr(w, 400, err)
				return
			}
			writeJSON(w, 200, map[string]any{"ok": true})
		default:
			http.Error(w, "method not allowed", 405)
		}
		return
	}
	if rest[0] == "" {
		http.NotFound(w, r)
		return
	}
	mid := parseID(rest[0])
	switch r.Method {
	case "DELETE":
		if err := a.writeSync(func(d *sql.DB) error { return model.DeleteModel(d, mid) }); err != nil {
			logger.Error("admin: delete model failed", "model_id", mid, "err", err)
			writeSyncErr(w, 400, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	case "PUT":
		var req struct {
			ContextLength   *int `json:"context_length"`
			MaxOutputLength *int `json:"max_output_length"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, 400, map[string]any{"error": "invalid JSON"})
			return
		}
		cl, ml := model.DefaultModelContextLength, model.DefaultModelMaxOutputLength
		if req.ContextLength != nil {
			cl = *req.ContextLength
		}
		if req.MaxOutputLength != nil {
			ml = *req.MaxOutputLength
		}
		if cl < 0 || ml < 0 {
			writeJSON(w, 400, map[string]any{"error": "lengths must be >= 0"})
			return
		}
		if err := a.writeSync(func(d *sql.DB) error { return model.UpdateModel(d, mid, cl, ml) }); err != nil {
			writeSyncErr(w, 400, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	default:
		http.NotFound(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func mask(s string) string {
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + "****" + s[len(s)-4:]
}

// isMaskedKey reports whether s looks like a value produced by mask() —
// i.e. the placeholder echoed back by a UI that displayed the masked key
// rather than the real secret. We treat any string containing the literal
// "****" segment as masked, which covers both the short-key form ("****")
// and the long-key form ("abcd****wxyz").
func isMaskedKey(s string) bool {
	return strings.Contains(s, "****")
}
