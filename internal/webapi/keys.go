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
	if r.Method != "DELETE" {
		http.Error(w, "method not allowed", 405)
		return
	}
	if err := a.writeSync(func(d *sql.DB) error { return model.DeleteExtKey(d, id) }); err != nil {
		writeSyncErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
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
		Label string `json:"label"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	var k *model.ExtKey
	if err := a.writeSync(func(d *sql.DB) error {
		var e error
		k, e = model.CreateExtKey(d, req.Label)
		return e
	}); err != nil {
		writeSyncErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{
		"id": k.ID, "key": k.Key, "label": k.Label, "enabled": k.Enabled,
	})
}
