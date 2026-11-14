package adminapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/great-magician-01/any-llm/internal/logger"
	"github.com/great-magician-01/any-llm/internal/store"
)

// 模型别名（固定对外模型）管理：别名是客户端请求用的稳定名称，绑定链按
// 数组顺序即故障转移优先级。一个别名可存多套命名配置（profile），
// active_profile 指定当前生效的一套；管理端通过 activate 端点手动切换。

type aliasBindingReq struct {
	UpstreamID int64  `json:"upstream_id"`
	ModelName  string `json:"model_name"`
	Profile    string `json:"profile"`
}

type aliasReq struct {
	Name          string            `json:"name"`
	ActiveProfile string            `json:"active_profile"`
	Bindings      []aliasBindingReq `json:"bindings"`
}

// validateAliasBindings 校验绑定列表并转成 model 层结构。绑定指向的上游必须
// 存在；model_name 非空；profile 归一（空 → default）。priority 由数组顺序决定。
func (a *API) validateAliasBindings(reqs []aliasBindingReq) ([]store.AliasBinding, string) {
	if len(reqs) == 0 {
		return nil, "at least one binding is required"
	}
	bindings := make([]store.AliasBinding, 0, len(reqs))
	for i, b := range reqs {
		if b.UpstreamID == 0 {
			return nil, "bindings[" + strconv.Itoa(i) + "]: upstream_id is required"
		}
		if _, err := store.GetUpstreamByID(a.db, b.UpstreamID); err != nil {
			return nil, "bindings[" + strconv.Itoa(i) + "]: upstream not found"
		}
		modelName := strings.TrimSpace(b.ModelName)
		if modelName == "" {
			return nil, "bindings[" + strconv.Itoa(i) + "]: model_name is required"
		}
		profile, err := store.NormalizeAliasProfile(b.Profile)
		if err != nil {
			return nil, "bindings[" + strconv.Itoa(i) + "]: " + err.Error()
		}
		bindings = append(bindings, store.AliasBinding{UpstreamID: b.UpstreamID, ModelName: modelName, Priority: i, Profile: profile})
	}
	return bindings, ""
}

// resolveActiveProfile 解析生效配置：请求显式给了就必须存在于绑定里；
// 没给则优先保留 fallback（更新场景的现值），fallback 已不存在时取第一套配置。
// bindings 必须非空。
func resolveActiveProfile(requested string, bindings []store.AliasBinding, fallback string) (string, string) {
	if strings.TrimSpace(requested) != "" {
		p, err := store.NormalizeAliasProfile(requested)
		if err != nil {
			return "", err.Error()
		}
		for _, b := range bindings {
			if b.Profile == p {
				return p, ""
			}
		}
		return "", "active_profile " + strconv.Quote(p) + " has no bindings"
	}
	if fallback != "" {
		for _, b := range bindings {
			if b.Profile == fallback {
				return fallback, ""
			}
		}
	}
	return bindings[0].Profile, ""
}

// validateAliasName 校验创建/改名时的对外模型名。
func validateAliasName(name string) string {
	if name == "" {
		return "name is required"
	}
	if len(name) > 200 {
		return "name too long (max 200 chars)"
	}
	return ""
}

func (a *API) listAliases(w http.ResponseWriter, r *http.Request) {
	list, err := store.ListAliases(a.db)
	if err != nil {
		logger.Error("admin: list aliases failed", "err", err)
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"data": list})
}

func (a *API) createAlias(w http.ResponseWriter, r *http.Request) {
	var req aliasReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		logger.Warn("admin: create alias invalid JSON", "err", err)
		writeJSON(w, 400, map[string]any{"error": "invalid JSON"})
		return
	}
	name := strings.TrimSpace(req.Name)
	if verr := validateAliasName(name); verr != "" {
		writeJSON(w, 400, map[string]any{"error": verr})
		return
	}
	bindings, verr := a.validateAliasBindings(req.Bindings)
	if verr != "" {
		writeJSON(w, 400, map[string]any{"error": verr})
		return
	}
	active, verr := resolveActiveProfile(req.ActiveProfile, bindings, "")
	if verr != "" {
		writeJSON(w, 400, map[string]any{"error": verr})
		return
	}
	alias := &store.ModelAlias{Name: name, ActiveProfile: active, Bindings: bindings}
	var id int64
	if err := a.writeSync(func(d *sql.DB) error {
		var e error
		id, e = store.CreateAlias(d, alias)
		return e
	}); err != nil {
		logger.Error("admin: create alias DB write failed", "name", alias.Name, "err", err)
		writeSyncErr(w, 400, err)
		return
	}
	out, err := store.GetAliasByID(a.db, id)
	if err != nil {
		logger.Error("admin: reload created alias failed", "id", id, "err", err)
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, out)
}

func (a *API) getAlias(w http.ResponseWriter, r *http.Request, id int64) {
	alias, err := store.GetAliasByID(a.db, id)
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	writeJSON(w, 200, alias)
}

func (a *API) updateAlias(w http.ResponseWriter, r *http.Request, id int64) {
	alias, err := store.GetAliasByID(a.db, id)
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	var req aliasReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"error": "invalid JSON"})
		return
	}
	if strings.TrimSpace(req.Name) != "" {
		alias.Name = strings.TrimSpace(req.Name)
	}
	// bindings 给了就整体替换（数组顺序即优先级）；不给则保留现有绑定。
	// active_profile 同理：给了就切（目标配置必须存在），不给则保留现值；
	// 仅改绑定且删掉了当前生效配置时，自动选第一套配置，避免留下空指针。
	if req.Bindings != nil {
		bindings, verr := a.validateAliasBindings(req.Bindings)
		if verr != "" {
			writeJSON(w, 400, map[string]any{"error": verr})
			return
		}
		active, verr := resolveActiveProfile(req.ActiveProfile, bindings, alias.ActiveProfile)
		if verr != "" {
			writeJSON(w, 400, map[string]any{"error": verr})
			return
		}
		alias.Bindings = bindings
		alias.ActiveProfile = active
	} else if strings.TrimSpace(req.ActiveProfile) != "" {
		if len(alias.Bindings) == 0 {
			writeJSON(w, 400, map[string]any{"error": "alias has no bindings"})
			return
		}
		active, verr := resolveActiveProfile(req.ActiveProfile, alias.Bindings, "")
		if verr != "" {
			writeJSON(w, 400, map[string]any{"error": verr})
			return
		}
		alias.ActiveProfile = active
	}
	if err := a.writeSync(func(d *sql.DB) error { return store.UpdateAlias(d, alias) }); err != nil {
		logger.Error("admin: update alias DB write failed", "id", id, "name", alias.Name, "err", err)
		writeSyncErr(w, 400, err)
		return
	}
	out, err := store.GetAliasByID(a.db, id)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, out)
}

func (a *API) deleteAlias(w http.ResponseWriter, r *http.Request, id int64) {
	if err := a.writeSync(func(d *sql.DB) error { return store.DeleteAlias(d, id) }); err != nil {
		logger.Error("admin: delete alias failed", "id", id, "err", err)
		writeSyncErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// activateAlias 一键切换别名的生效配置。只改 active_profile，不重写绑定，
// 因此可以在列表页直接切换而不碰编辑表单；目标配置必须至少有一条绑定。
// 网关缓存由 store 写路径逐出，切换后下一个请求立即生效。
func (a *API) activateAlias(w http.ResponseWriter, r *http.Request, id int64) {
	alias, err := store.GetAliasByID(a.db, id)
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	var req struct {
		Profile string `json:"profile"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"error": "invalid JSON"})
		return
	}
	profile, err := store.NormalizeAliasProfile(req.Profile)
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	exists := false
	for _, b := range alias.Bindings {
		if b.Profile == profile {
			exists = true
			break
		}
	}
	if !exists {
		writeJSON(w, 400, map[string]any{"error": "profile " + strconv.Quote(profile) + " has no bindings"})
		return
	}
	if err := a.writeSync(func(d *sql.DB) error { return store.SetAliasActiveProfile(d, id, profile) }); err != nil {
		switch {
		case errors.Is(err, store.ErrAliasProfileNotFound):
			writeJSON(w, 400, map[string]any{"error": "profile " + strconv.Quote(profile) + " has no bindings"})
		case errors.Is(err, sql.ErrNoRows):
			writeJSON(w, 404, map[string]any{"error": "not found"})
		default:
			logger.Error("admin: activate alias profile DB write failed", "id", id, "profile", profile, "err", err)
			writeSyncErr(w, 400, err)
		}
		return
	}
	logger.Info("admin: alias active profile switched", "id", id, "alias", alias.Name, "from", alias.ActiveProfile, "to", profile)
	out, err := store.GetAliasByID(a.db, id)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, out)
}
