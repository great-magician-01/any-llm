package adminapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/great-magician-01/any-llm/internal/logger"
	"github.com/great-magician-01/any-llm/internal/store"
	"github.com/great-magician-01/any-llm/internal/upstream"
)

func (a *API) listUpstreams(w http.ResponseWriter, r *http.Request) {
	// ?status=enabled|disabled|all：缺省 all（Dashboard/Keys/Aliases 等页面
	// 依赖全量口径，不能动）；上游管理页默认显式传 enabled。未知值同样按全量
	// 返回：该参数引入前任何 status= 都被忽略并返回全量，硬 400 会打破存量的
	// 书签/探针/集成调用。
	var enabled *bool
	if s := r.URL.Query().Get("status"); s == "enabled" || s == "disabled" {
		v := s == "enabled"
		enabled = &v
	}
	list, err := store.ListUpstreams(a.db, enabled)
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
		Name              string  `json:"name"`
		BaseURL           string  `json:"base_url"`
		APIKey            string  `json:"api_key"`
		Format            string  `json:"format"`
		Remark            string  `json:"remark"`
		Enabled           *bool   `json:"enabled"`
		ExpiresAt         optTime `json:"expires_at"`
		DailyTokenLimit   int     `json:"daily_token_limit"`
		MonthlyTokenLimit int     `json:"monthly_token_limit"`
		MaxConcurrent     *int    `json:"max_concurrent"`
		FetchModels       bool    `json:"fetch_models"`
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
	if req.MaxConcurrent != nil && *req.MaxConcurrent < 0 {
		writeJSON(w, 400, map[string]any{"error": "max_concurrent must be >= 0 (0 = unlimited)"})
		return
	}
	// 缺省给默认并发上限；显式 0 = 不限。
	maxConcurrent := store.DefaultMaxConcurrent
	if req.MaxConcurrent != nil {
		maxConcurrent = *req.MaxConcurrent
	}
	u := &store.Upstream{Name: req.Name, BaseURL: req.BaseURL, APIKey: req.APIKey, Format: req.Format, Remark: req.Remark,
		DailyTokenLimit: req.DailyTokenLimit, MonthlyTokenLimit: req.MonthlyTokenLimit, MaxConcurrent: maxConcurrent}
	if req.ExpiresAt.set {
		u.ExpiresAt = req.ExpiresAt.value()
	}
	var id int64
	if err := a.writeSync(func(d *sql.DB) error {
		var e error
		id, e = store.CreateUpstream(d, u)
		return e
	}); err != nil {
		logger.Error("admin: create upstream DB write failed", "name", req.Name, "err", err)
		writeSyncErr(w, 400, err)
		return
	}
	// 显式 enabled=false：创建即禁用（缺省启用，与 DB 默认一致），供「预建禁用、
	// 配置好模型与别名后再上线」的流程。读回完整行再改存，遵守 UpdateUpstream
	// 的「先 Get 再存」约定。
	if req.Enabled != nil && !*req.Enabled {
		if err := a.writeSync(func(d *sql.DB) error {
			nu, e := store.GetUpstreamByID(d, id)
			if e != nil {
				return e
			}
			nu.Enabled = false
			return store.UpdateUpstream(d, nu)
		}); err != nil {
			logger.Error("admin: disable new upstream failed", "id", id, "err", err)
			writeSyncErr(w, 400, err)
			return
		}
	}
	if req.FetchModels && a.client != nil {
		u.ID = id
		names, err := upstream.FetchModels(r.Context(), a.client.HTTP(), u)
		if err == nil {
			a.writeSync(func(d *sql.DB) error { return store.ReplaceModels(d, id, names) })
		} else {
			logger.Warn("admin: create upstream fetch models failed", "name", req.Name, "id", id, "err", err)
		}
	}
	// 读回完整行返回（created_at 等字段只有库里有）。读回失败（瞬时 DB 错误，
	// 或行被并发软删）不能吞：u 会是 nil，下一行 mask 直接 panic。行已建好，
	// 如实报 500，前端重拉列表即可看到。
	u, err := store.GetUpstreamByID(a.db, id)
	if err != nil {
		logger.Error("admin: reload created upstream failed", "id", id, "err", err)
		writeJSON(w, 500, map[string]any{"error": "upstream created but reload failed: " + err.Error()})
		return
	}
	u.APIKey = mask(u.APIKey)
	writeJSON(w, 200, u)
}

func (a *API) getUpstream(w http.ResponseWriter, r *http.Request, id int64) {
	u, err := store.GetUpstreamByID(a.db, id)
	if err != nil {
		logger.Warn("admin: get upstream not found", "id", id, "err", err)
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	u.APIKey = mask(u.APIKey)
	writeJSON(w, 200, u)
}

func (a *API) updateUpstream(w http.ResponseWriter, r *http.Request, id int64) {
	// 404 预检：行不存在直接拒。真正的合并以 writeSync 闭包里的重读为准（见下）。
	if _, err := store.GetUpstreamByID(a.db, id); err != nil {
		logger.Warn("admin: update upstream not found", "id", id, "err", err)
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	var req struct {
		Name    string `json:"name"`
		BaseURL string `json:"base_url"`
		APIKey  string `json:"api_key"`
		Format  string `json:"format"`
		// 指针区分「没给」与「给了空串」：只带 enabled 的 PATCH（如列表页开关）
		// 不得顺手清空备注；显式空串才是清空。
		Remark            *string `json:"remark"`
		Enabled           *bool   `json:"enabled"`
		ExpiresAt         optTime `json:"expires_at"`
		DailyTokenLimit   *int    `json:"daily_token_limit"`
		MonthlyTokenLimit *int    `json:"monthly_token_limit"`
		MaxConcurrent     *int    `json:"max_concurrent"`
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
	if req.MaxConcurrent != nil && *req.MaxConcurrent < 0 {
		writeJSON(w, 400, map[string]any{"error": "max_concurrent must be >= 0 (0 = unlimited)"})
		return
	}
	if req.Format != "" && req.Format != "openai" && req.Format != "anthropic" && req.Format != "responses" {
		logger.Warn("admin: update upstream invalid format", "format", req.Format)
		writeJSON(w, 400, map[string]any{"error": "format must be openai, anthropic or responses"})
		return
	}
	// 读-改-写整个放进 writeSync 闭包：writer 串行化所有 DB 写，闭包里重读到的
	// 是上一次写提交后的最新行，合并 PATCH 与全量覆写之间不会有别的写插入。
	// 若在闭包外读好再写，一个并发写（完整保存/配置导入）会被这次写覆盖回旧值
	// ——开关切换这种只带 enabled 的 PATCH 会把别人刚存的 expires_at/api_key/限额
	// 全部抹回。
	var merged *store.Upstream
	if err := a.writeSync(func(d *sql.DB) error {
		u, e := store.GetUpstreamByID(d, id)
		if e != nil {
			return e // 预检之后又被并发软删：报 400，列表重拉即消失
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
		if req.Format != "" {
			u.Format = req.Format
		}
		if req.Remark != nil {
			u.Remark = *req.Remark
		}
		if req.DailyTokenLimit != nil {
			u.DailyTokenLimit = *req.DailyTokenLimit
		}
		if req.MonthlyTokenLimit != nil {
			u.MonthlyTokenLimit = *req.MonthlyTokenLimit
		}
		if req.MaxConcurrent != nil {
			u.MaxConcurrent = *req.MaxConcurrent
		}
		if req.Enabled != nil {
			u.Enabled = *req.Enabled
		}
		// 必须在 UpdateUpstream（全量覆盖）之前合并；缺省保留现状，null 清除
		if req.ExpiresAt.set {
			u.ExpiresAt = req.ExpiresAt.value()
		}
		merged = u
		return store.UpdateUpstream(d, u)
	}); err != nil {
		logger.Error("admin: update upstream DB write failed", "id", id, "err", err)
		writeSyncErr(w, 400, err)
		return
	}
	merged.APIKey = mask(merged.APIKey)
	writeJSON(w, 200, merged)
}

func (a *API) deleteUpstream(w http.ResponseWriter, r *http.Request, id int64) {
	if err := a.writeSync(func(d *sql.DB) error { return store.DeleteUpstream(d, id) }); err != nil {
		logger.Error("admin: delete upstream failed", "id", id, "err", err)
		writeSyncErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *API) fetchModels(w http.ResponseWriter, r *http.Request, id int64) {
	u, err := store.GetUpstreamByID(a.db, id)
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
	if err := a.writeSync(func(d *sql.DB) error { return store.ReplaceModels(d, id, names) }); err != nil {
		logger.Error("admin: replace models DB write failed", "upstream", u.Name, "id", id, "err", err)
		writeSyncErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"models": names})
}

// listModels serves GET /api/admin/upstreams/{id}/models.
func (a *API) listModels(w http.ResponseWriter, r *http.Request, upstreamID int64) {
	models, err := store.ListModels(a.db, upstreamID)
	if err != nil {
		logger.Error("admin: list models failed", "upstream_id", upstreamID, "err", err)
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"data": models})
}

// addModel serves POST /api/admin/upstreams/{id}/models.
func (a *API) addModel(w http.ResponseWriter, r *http.Request, upstreamID int64) {
	var req struct {
		ModelName       string `json:"model_name"`
		ContextLength   int    `json:"context_length"`
		MaxOutputLength int    `json:"max_output_length"`
		// 缺省 false：新模型默认非多模态。
		Multimodal bool `json:"multimodal"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.ContextLength < 0 || req.MaxOutputLength < 0 {
		writeJSON(w, 400, map[string]any{"error": "lengths must be >= 0"})
		return
	}
	if err := a.writeSync(func(d *sql.DB) error {
		return store.AddModel(d, upstreamID, store.UpstreamModel{
			ModelName: req.ModelName, Manual: true,
			ContextLength: req.ContextLength, MaxOutputLength: req.MaxOutputLength,
			Multimodal: req.Multimodal,
		})
	}); err != nil {
		if errors.Is(err, store.ErrModelExists) {
			// 已存在的模型走编辑：添加对已存在同名模型静默 200 会让管理员以为
			// 刚配的字段生效了，其实什么都没改。
			writeJSON(w, 409, map[string]any{"error": "model already exists; use edit to change it"})
			return
		}
		logger.Error("admin: add model failed", "upstream_id", upstreamID, "model", req.ModelName, "err", err)
		writeSyncErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// deleteModel serves DELETE /api/admin/upstreams/{id}/models/{mid}.
func (a *API) deleteModel(w http.ResponseWriter, r *http.Request, upstreamID, mid int64) {
	if err := a.writeSync(func(d *sql.DB) error { return store.DeleteModel(d, mid) }); err != nil {
		logger.Error("admin: delete model failed", "model_id", mid, "err", err)
		writeSyncErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// updateModel serves PUT /api/admin/upstreams/{id}/models/{mid}.
func (a *API) updateModel(w http.ResponseWriter, r *http.Request, upstreamID, mid int64) {
	var req struct {
		ContextLength   *int `json:"context_length"`
		MaxOutputLength *int `json:"max_output_length"`
		// 缺省 false：PUT 全量语义下未给出即恢复默认（与长度缺省回默认值一致）。
		Multimodal bool `json:"multimodal"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"error": "invalid JSON"})
		return
	}
	cl, ml := store.DefaultModelContextLength, store.DefaultModelMaxOutputLength
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
	if err := a.writeSync(func(d *sql.DB) error {
		return store.UpdateModel(d, upstreamID, store.UpstreamModel{
			ID: mid, ContextLength: cl, MaxOutputLength: ml, Multimodal: req.Multimodal,
		})
	}); err != nil {
		// 模型不存在、已软删或不属于路径里的上游：404 而非假成功。
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, 404, map[string]any{"error": "model not found"})
			return
		}
		writeSyncErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
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
