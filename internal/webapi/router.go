package webapi

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/great-magician-01/any-llm/internal/db"
	"github.com/great-magician-01/any-llm/internal/upstream"
)

type API struct {
	db     *sql.DB
	writer *db.Writer
	client *upstream.Client
}

func NewAPI(db *sql.DB, writer *db.Writer, client *upstream.Client) *API {
	return &API{db: db, writer: writer, client: client}
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

func (a *API) writeSync(fn db.WriteFunc) error {
	if a.writer != nil {
		return a.writer.DoSync(fn)
	}
	return fn(a.db)
}

// writeSyncErr writes an HTTP error response for a writeSync error. A writer
// that is shutting down (ErrWriterStopped) yields 503; other errors use the
// caller-supplied default status (typically 400 for client-caused DB errors
// such as constraint violations, 500 for internal failures).
func writeSyncErr(w http.ResponseWriter, status int, err error) {
	if errors.Is(err, db.ErrWriterStopped) {
		writeJSON(w, 503, map[string]any{"error": "server is shutting down"})
		return
	}
	writeJSON(w, status, map[string]any{"error": err.Error()})
}
