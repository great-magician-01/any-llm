package webapi

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/great-magician-01/any-llm/internal/model"
)

func (a *API) handleKeys(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		a.listKeys(w, r)
	case "POST":
		a.createKey(w, r)
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (a *API) handleKeyItem(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/admin/keys/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id := parseID(parts[0])
	if id == 0 {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case "DELETE":
		if err := a.writeSync(func(d *sql.DB) error { return model.DeleteExtKey(d, id) }); err != nil {
			writeSyncErr(w, 400, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	case "PUT":
		a.updateKey(w, r, id)
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (a *API) listKeys(w http.ResponseWriter, r *http.Request) {
	list, err := model.ListExtKeys(a.db)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"data": list})
}

func (a *API) createKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Label             string `json:"label"`
		DailyTokenLimit   int    `json:"daily_token_limit"`
		MonthlyTokenLimit int    `json:"monthly_token_limit"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.DailyTokenLimit < 0 || req.MonthlyTokenLimit < 0 {
		writeJSON(w, 400, map[string]any{"error": "token limits must be >= 0"})
		return
	}
	var k *model.ExtKey
	if err := a.writeSync(func(d *sql.DB) error {
		var e error
		k, e = model.CreateExtKey(d, req.Label, req.DailyTokenLimit, req.MonthlyTokenLimit)
		return e
	}); err != nil {
		writeSyncErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{
		"id": k.ID, "key": k.Key, "label": k.Label, "enabled": k.Enabled,
		"daily_token_limit": k.DailyTokenLimit, "monthly_token_limit": k.MonthlyTokenLimit,
	})
}

func (a *API) updateKey(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		Label             *string `json:"label"`
		Enabled           *bool   `json:"enabled"`
		DailyTokenLimit   *int    `json:"daily_token_limit"`
		MonthlyTokenLimit *int    `json:"monthly_token_limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"error": "invalid JSON"})
		return
	}
	// Fetch current values; PATCH semantics — nil fields keep existing value.
	cur, err := model.GetExtKeyByID(a.db, id)
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "key not found"})
		return
	}
	label := cur.Label
	if req.Label != nil {
		label = *req.Label
	}
	enabled := cur.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	daily := cur.DailyTokenLimit
	if req.DailyTokenLimit != nil {
		daily = *req.DailyTokenLimit
	}
	monthly := cur.MonthlyTokenLimit
	if req.MonthlyTokenLimit != nil {
		monthly = *req.MonthlyTokenLimit
	}
	if daily < 0 || monthly < 0 {
		writeJSON(w, 400, map[string]any{"error": "token limits must be >= 0"})
		return
	}
	if err := a.writeSync(func(d *sql.DB) error {
		return model.UpdateExtKey(d, id, label, enabled, daily, monthly)
	}); err != nil {
		writeSyncErr(w, 400, err)
		return
	}
	updated, err := model.GetExtKeyByID(a.db, id)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": "key updated but re-fetch failed: " + err.Error()})
		return
	}
	updated.Key = model.MaskKey(updated.Key)
	writeJSON(w, 200, updated)
}
