package webapi

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"

	"github.com/great-magician-01/any-llm/internal/upstream"
)

type API struct {
	db     *sql.DB
	client *upstream.Client
}

func NewAPI(db *sql.DB, client *upstream.Client) *API {
	return &API{db: db, client: client}
}

func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/admin/upstreams", a.handleUpstreams)
	mux.HandleFunc("/api/admin/upstreams/", a.handleUpstreamItem)
	mux.HandleFunc("/api/admin/keys", a.handleKeys)
	mux.HandleFunc("/api/admin/keys/", a.handleKeyItem)
	mux.HandleFunc("/api/admin/usage/", a.handleUsage)
	return mux
}

func (a *API) handleUpstreams(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		a.listUpstreams(w, r)
	case "POST":
		a.createUpstream(w, r)
	default:
		http.Error(w, "method not allowed", 405)
	}
}

func (a *API) handleUpstreamItem(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/admin/upstreams/")
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
	if len(parts) == 1 {
		switch r.Method {
		case "GET":
			a.getUpstream(w, r, id)
		case "PUT":
			a.updateUpstream(w, r, id)
		case "DELETE":
			a.deleteUpstream(w, r, id)
		default:
			http.Error(w, "method not allowed", 405)
		}
		return
	}
	switch parts[1] {
	case "fetch-models":
		if r.Method == "POST" {
			a.fetchModels(w, r, id)
		} else {
			http.Error(w, "method not allowed", 405)
		}
	case "models":
		a.handleModels(w, r, id, parts[2:])
	default:
		http.NotFound(w, r)
	}
}

func parseID(s string) int64 {
	var id int64
	fmt.Sscanf(s, "%d", &id)
	return id
}
