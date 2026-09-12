package webapi

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/great-magician-01/any-llm/internal/logger"
	"github.com/great-magician-01/any-llm/internal/model"
)

// allowed_models 白名单的条目上限与单条长度上限（对外模型名，别名或
// upstream/model）。条目不必对应已存在的模型/别名——允许预先配置。
const (
	maxAllowedModels    = 256
	maxAllowedModelName = 256
)

// normalizeAllowedModels 归一化 key 的模型白名单：trim、去空项、按序去重；
// 空 = 不限（nil）。返回错误文案与 nil 表示拒绝（400）。
func normalizeAllowedModels(models []string) ([]string, string) {
	out := make([]string, 0, len(models))
	seen := make(map[string]bool, len(models))
	for _, m := range models {
		m = strings.TrimSpace(m)
		if m == "" || seen[m] {
			continue
		}
		if len(m) > maxAllowedModelName {
			return nil, "allowed model names must be at most 256 characters"
		}
		seen[m] = true
		out = append(out, m)
	}
	if len(out) > maxAllowedModels {
		return nil, "too many allowed models (max 256)"
	}
	if len(out) == 0 {
		return nil, ""
	}
	return out, ""
}

func (a *API) listKeys(w http.ResponseWriter, r *http.Request) {
	list, err := model.ListExtKeys(a.db)
	if err != nil {
		logger.Error("admin: list keys failed", "err", err)
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"data": list})
}

func (a *API) createKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Label             string   `json:"label"`
		DailyTokenLimit   int      `json:"daily_token_limit"`
		MonthlyTokenLimit int      `json:"monthly_token_limit"`
		AllowedModels     []string `json:"allowed_models"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.DailyTokenLimit < 0 || req.MonthlyTokenLimit < 0 {
		logger.Warn("admin: create key negative token limit", "daily", req.DailyTokenLimit, "monthly", req.MonthlyTokenLimit)
		writeJSON(w, 400, map[string]any{"error": "token limits must be >= 0"})
		return
	}
	allowed, badReq := normalizeAllowedModels(req.AllowedModels)
	if badReq != "" {
		logger.Warn("admin: create key invalid allowed_models", "error", badReq)
		writeJSON(w, 400, map[string]any{"error": badReq})
		return
	}
	var k *model.ExtKey
	if err := a.writeSync(func(d *sql.DB) error {
		var e error
		k, e = model.CreateExtKey(d, req.Label, req.DailyTokenLimit, req.MonthlyTokenLimit, allowed)
		return e
	}); err != nil {
		logger.Error("admin: create key DB write failed", "label", req.Label, "err", err)
		writeSyncErr(w, 400, err)
		return
	}
	logger.Info("admin: key created", "id", k.ID, "label", k.Label, "enabled", k.Enabled)
	writeJSON(w, 200, map[string]any{
		"id": k.ID, "key": k.Key, "label": k.Label, "enabled": k.Enabled,
		"daily_token_limit": k.DailyTokenLimit, "monthly_token_limit": k.MonthlyTokenLimit,
		"allowed_models": k.AllowedModels,
	})
}

// deleteKey serves DELETE /api/admin/keys/{id}.
func (a *API) deleteKey(w http.ResponseWriter, r *http.Request, id int64) {
	if err := a.writeSync(func(d *sql.DB) error { return model.DeleteExtKey(d, id) }); err != nil {
		logger.Error("admin: delete key failed", "id", id, "err", err)
		writeSyncErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *API) updateKey(w http.ResponseWriter, r *http.Request, id int64) {
	var req struct {
		Label             *string   `json:"label"`
		Enabled           *bool     `json:"enabled"`
		DailyTokenLimit   *int      `json:"daily_token_limit"`
		MonthlyTokenLimit *int      `json:"monthly_token_limit"`
		AllowedModels     *[]string `json:"allowed_models"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		logger.Warn("admin: update key invalid JSON", "id", id, "err", err)
		writeJSON(w, 400, map[string]any{"error": "invalid JSON"})
		return
	}
	// Fetch current values; PATCH semantics — nil fields keep existing value.
	cur, err := model.GetExtKeyByID(a.db, id)
	if err != nil {
		logger.Warn("admin: update key not found", "id", id, "err", err)
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
	// 白名单：nil 保留现值；显式传 [] = 清除限制（全部可用）。
	allowed := cur.AllowedModels
	if req.AllowedModels != nil {
		var badReq string
		allowed, badReq = normalizeAllowedModels(*req.AllowedModels)
		if badReq != "" {
			logger.Warn("admin: update key invalid allowed_models", "id", id, "error", badReq)
			writeJSON(w, 400, map[string]any{"error": badReq})
			return
		}
	}
	if daily < 0 || monthly < 0 {
		writeJSON(w, 400, map[string]any{"error": "token limits must be >= 0"})
		return
	}
	if err := a.writeSync(func(d *sql.DB) error {
		return model.UpdateExtKey(d, id, label, enabled, daily, monthly, allowed)
	}); err != nil {
		logger.Error("admin: update key DB write failed", "id", id, "err", err)
		writeSyncErr(w, 400, err)
		return
	}
	updated, err := model.GetExtKeyByID(a.db, id)
	if err != nil {
		logger.Error("admin: update key re-fetch failed", "id", id, "err", err)
		writeJSON(w, 500, map[string]any{"error": "key updated but re-fetch failed: " + err.Error()})
		return
	}
	writeJSON(w, 200, updated)
}
