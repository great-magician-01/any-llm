package webapi

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/great-magician-01/any-llm/internal/logger"
	"github.com/great-magician-01/any-llm/internal/model"
)

// 模型别名（固定对外模型）管理：别名是客户端请求用的稳定名称，绑定链按
// 数组顺序即故障转移优先级。

type aliasBindingReq struct {
	UpstreamID int64  `json:"upstream_id"`
	ModelName  string `json:"model_name"`
}

type aliasReq struct {
	Name     string            `json:"name"`
	Bindings []aliasBindingReq `json:"bindings"`
}

// validateAliasReq 校验请求并转成 model 层绑定列表。绑定指向的上游必须
// 存在且活跃；model_name 非空。priority 由数组顺序决定（0 最先尝试）。
func (a *API) validateAliasReq(req *aliasReq) ([]model.AliasBinding, string) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, "name is required"
	}
	if len(name) > 200 {
		return nil, "name too long (max 200 chars)"
	}
	if len(req.Bindings) == 0 {
		return nil, "at least one binding is required"
	}
	bindings := make([]model.AliasBinding, 0, len(req.Bindings))
	for i, b := range req.Bindings {
		if b.UpstreamID == 0 {
			return nil, "bindings[" + strconv.Itoa(i) + "]: upstream_id is required"
		}
		if _, err := model.GetUpstreamByID(a.db, b.UpstreamID); err != nil {
			return nil, "bindings[" + strconv.Itoa(i) + "]: upstream not found"
		}
		modelName := strings.TrimSpace(b.ModelName)
		if modelName == "" {
			return nil, "bindings[" + strconv.Itoa(i) + "]: model_name is required"
		}
		bindings = append(bindings, model.AliasBinding{UpstreamID: b.UpstreamID, ModelName: modelName, Priority: i})
	}
	return bindings, ""
}

func (a *API) listAliases(w http.ResponseWriter, r *http.Request) {
	list, err := model.ListAliases(a.db)
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
	bindings, verr := a.validateAliasReq(&req)
	if verr != "" {
		writeJSON(w, 400, map[string]any{"error": verr})
		return
	}
	alias := &model.ModelAlias{Name: strings.TrimSpace(req.Name), Bindings: bindings}
	var id int64
	if err := a.writeSync(func(d *sql.DB) error {
		var e error
		id, e = model.CreateAlias(d, alias)
		return e
	}); err != nil {
		logger.Error("admin: create alias DB write failed", "name", alias.Name, "err", err)
		writeSyncErr(w, 400, err)
		return
	}
	out, err := model.GetAliasByID(a.db, id)
	if err != nil {
		logger.Error("admin: reload created alias failed", "id", id, "err", err)
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, out)
}

func (a *API) getAlias(w http.ResponseWriter, r *http.Request, id int64) {
	alias, err := model.GetAliasByID(a.db, id)
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	writeJSON(w, 200, alias)
}

func (a *API) updateAlias(w http.ResponseWriter, r *http.Request, id int64) {
	alias, err := model.GetAliasByID(a.db, id)
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
	if req.Bindings != nil {
		bindings, verr := a.validateAliasReq(&aliasReq{Name: alias.Name, Bindings: req.Bindings})
		if verr != "" {
			writeJSON(w, 400, map[string]any{"error": verr})
			return
		}
		alias.Bindings = bindings
	}
	if err := a.writeSync(func(d *sql.DB) error { return model.UpdateAlias(d, alias) }); err != nil {
		logger.Error("admin: update alias DB write failed", "id", id, "name", alias.Name, "err", err)
		writeSyncErr(w, 400, err)
		return
	}
	out, err := model.GetAliasByID(a.db, id)
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, out)
}

func (a *API) deleteAlias(w http.ResponseWriter, r *http.Request, id int64) {
	if err := a.writeSync(func(d *sql.DB) error { return model.DeleteAlias(d, id) }); err != nil {
		logger.Error("admin: delete alias failed", "id", id, "err", err)
		writeSyncErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
