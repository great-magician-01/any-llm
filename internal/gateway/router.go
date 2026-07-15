package gateway

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/great-magician-01/any-llm/internal/model"
	"github.com/great-magician-01/any-llm/internal/upstream"
)

type Gateway struct {
	db     *sql.DB
	client *upstream.Client
}

func New(db *sql.DB, client *upstream.Client) *Gateway {
	return &Gateway{db: db, client: client}
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/v1/models" && r.Method == "GET":
		g.handleModels(w, r)
	case r.URL.Path == "/v1/chat/completions" && r.Method == "POST":
		g.handleCompletion(w, r, "openai")
	case r.URL.Path == "/v1/messages" && r.Method == "POST":
		g.handleCompletion(w, r, "anthropic")
	default:
		http.NotFound(w, r)
	}
}

func (g *Gateway) handleModels(w http.ResponseWriter, r *http.Request) {
	upstreams, err := model.ListUpstreams(g.db)
	if err != nil {
		WriteError(w, 500, "openai", "failed to list upstreams", "internal_error")
		return
	}
	type modelObj struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
	}
	var data []modelObj
	for _, u := range upstreams {
		models, err := model.ListModels(g.db, u.ID)
		if err != nil {
			continue
		}
		for _, m := range models {
			data = append(data, modelObj{
				ID:      u.Name + "/" + m.ModelName,
				Object:  "model",
				Created: u.CreatedAt.Unix(),
			})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
}

func (g *Gateway) handleCompletion(w http.ResponseWriter, r *http.Request, inFormat string) {
	extKey := extractKey(r)
	if extKey == "" {
		WriteError(w, 401, inFormat, "missing API key", "authentication_error")
		return
	}
	if !model.IsValidKeyFormat(extKey) {
		WriteError(w, 401, inFormat, "invalid API key format", "authentication_error")
		return
	}
	k, err := model.GetExtKey(g.db, extKey)
	if err != nil || !k.Enabled {
		WriteError(w, 401, inFormat, "invalid API key", "authentication_error")
		return
	}
	model.TouchExtKey(g.db, k.ID)

	body, err := readBody(r)
	if err != nil {
		WriteError(w, 400, inFormat, "failed to read request body", "invalid_request_error")
		return
	}

	var probe struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		WriteError(w, 400, inFormat, "invalid JSON body", "invalid_request_error")
		return
	}

	name, realModel, ok := splitModel(probe.Model)
	if !ok {
		WriteError(w, 400, inFormat, "model must be in 'name/model' format", "invalid_request_error")
		return
	}

	u, err := model.GetUpstreamByName(g.db, name)
	if err != nil {
		WriteError(w, 404, inFormat, "upstream '"+name+"' not found", "not_found_error")
		return
	}

	g.dispatch(w, r, inFormat, k, u, realModel, body)
}

func extractKey(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	if key := r.Header.Get("x-api-key"); key != "" {
		return key
	}
	return ""
}

func splitModel(m string) (name, model string, ok bool) {
	idx := strings.Index(m, "/")
	if idx <= 0 || idx >= len(m)-1 {
		return "", "", false
	}
	return m[:idx], m[idx+1:], true
}

func readBody(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	return readAll(r.Body)
}

func readAll(r io.Reader) ([]byte, error) {
	return io.ReadAll(r)
}

func (g *Gateway) dispatch(w http.ResponseWriter, r *http.Request, inFormat string, key *model.ExtKey, u *model.Upstream, realModel string, body []byte) {
	WriteError(w, 501, inFormat, "gateway not yet implemented", "internal_error")
}
