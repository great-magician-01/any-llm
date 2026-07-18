package webapi

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/great-magician-01/any-llm/internal/model"
	"github.com/great-magician-01/any-llm/internal/upstream"
)

func (a *API) listUpstreams(w http.ResponseWriter, r *http.Request) {
	list, err := model.ListUpstreams(a.db)
	if err != nil {
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
		Name        string `json:"name"`
		BaseURL     string `json:"base_url"`
		APIKey      string `json:"api_key"`
		Format      string `json:"format"`
		FetchModels bool   `json:"fetch_models"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"error": "invalid JSON"})
		return
	}
	if req.Format != "openai" && req.Format != "anthropic" {
		writeJSON(w, 400, map[string]any{"error": "format must be openai or anthropic"})
		return
	}
	u := &model.Upstream{Name: req.Name, BaseURL: req.BaseURL, APIKey: req.APIKey, Format: req.Format}
	var id int64
	if err := a.writeSync(func(d *sql.DB) error {
		var e error
		id, e = model.CreateUpstream(d, u)
		return e
	}); err != nil {
		writeSyncErr(w, 400, err)
		return
	}
	if req.FetchModels && a.client != nil {
		u.ID = id
		names, err := upstream.FetchModels(r.Context(), a.client.HTTP(), u)
		if err == nil {
			a.writeSync(func(d *sql.DB) error { return model.ReplaceModels(d, id, names) })
		}
	}
	u, _ = model.GetUpstreamByID(a.db, id)
	u.APIKey = mask(u.APIKey)
	writeJSON(w, 200, u)
}

func (a *API) getUpstream(w http.ResponseWriter, r *http.Request, id int64) {
	u, err := model.GetUpstreamByID(a.db, id)
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	u.APIKey = mask(u.APIKey)
	writeJSON(w, 200, u)
}

func (a *API) updateUpstream(w http.ResponseWriter, r *http.Request, id int64) {
	u, err := model.GetUpstreamByID(a.db, id)
	if err != nil {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	var req struct {
		Name    string `json:"name"`
		BaseURL string `json:"base_url"`
		APIKey  string `json:"api_key"`
		Format  string `json:"format"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"error": "invalid JSON"})
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
	if req.Format != "" {
		u.Format = req.Format
	}
	if err := a.writeSync(func(d *sql.DB) error { return model.UpdateUpstream(d, u) }); err != nil {
		writeSyncErr(w, 400, err)
		return
	}
	u.APIKey = mask(u.APIKey)
	writeJSON(w, 200, u)
}

func (a *API) deleteUpstream(w http.ResponseWriter, r *http.Request, id int64) {
	if err := a.writeSync(func(d *sql.DB) error { return model.DeleteUpstream(d, id) }); err != nil {
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
		writeJSON(w, 502, map[string]any{"error": err.Error()})
		return
	}
	if err := a.writeSync(func(d *sql.DB) error { return model.ReplaceModels(d, id, names) }); err != nil {
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
				writeJSON(w, 500, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, 200, map[string]any{"data": models})
		case "POST":
			var req struct {
				ModelName string `json:"model_name"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			if err := a.writeSync(func(d *sql.DB) error { return model.AddModel(d, upstreamID, req.ModelName, true) }); err != nil {
				writeSyncErr(w, 400, err)
				return
			}
			writeJSON(w, 200, map[string]any{"ok": true})
		default:
			http.Error(w, "method not allowed", 405)
		}
		return
	}
	if rest[0] != "" && r.Method == "DELETE" {
		mid := parseID(rest[0])
		if err := a.writeSync(func(d *sql.DB) error { return model.DeleteModel(d, mid) }); err != nil {
			writeSyncErr(w, 400, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
		return
	}
	http.NotFound(w, r)
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
