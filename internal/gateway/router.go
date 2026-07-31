package gateway

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/great-magician-01/any-llm/internal/db"
	"github.com/great-magician-01/any-llm/internal/logger"
	"github.com/great-magician-01/any-llm/internal/model"
	"github.com/great-magician-01/any-llm/internal/upstream"
)

type Gateway struct {
	db       *sql.DB
	writer   *db.Writer
	client   *upstream.Client
	sessions *SessionStore
}

func New(db *sql.DB, writer *db.Writer, client *upstream.Client) *Gateway {
	return &Gateway{db: db, writer: writer, client: client, sessions: NewSessionStore(db, sessionTTL)}
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/v1/models" && r.Method == "GET":
		g.handleModels(w, r)
	case r.URL.Path == "/v1/chat/completions" && r.Method == "POST":
		g.handleCompletion(w, r, "openai")
	case r.URL.Path == "/v1/messages" && r.Method == "POST":
		g.handleCompletion(w, r, "anthropic")
	case r.URL.Path == "/v1/responses" && r.Method == "POST":
		g.handleCompletion(w, r, "responses")
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
			logger.Warn("gateway: list models failed, skipping upstream", "upstream", u.Name, "upstream_id", u.ID, "err", err)
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
	if g.writer != nil {
		g.writer.DoAsync(func(d *sql.DB) error { return model.TouchExtKey(d, k.ID) })
	} else {
		if err := model.TouchExtKey(g.db, k.ID); err != nil {
			logger.Warn("gateway: touch ext key failed", "key_id", k.ID, "err", err)
		}
	}

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

	if err := g.checkTokenLimits(k, u); err != nil {
		if le, ok := err.(*limitError); ok {
			WriteError(w, 429, inFormat, le.message, "rate_limit_error")
			logger.Info("token limit exceeded",
				"key_id", k.ID, "key_label", k.Label,
				"upstream", u.Name, "scope", le.scope,
				"used", le.used, "limit", le.limit,
			)
			return
		}
		logger.Error("gateway: check token limits DB error", "key_id", k.ID, "upstream", u.Name, "err", err)
		WriteError(w, 500, inFormat, "failed to check token limits: "+err.Error(), "internal_error")
		return
	}

	g.dispatch(w, r, inFormat, k, u, realModel, body)
}

// limitError indicates an ext key or upstream has exceeded its daily or
// monthly token quota. Callers should map it to HTTP 429.
type limitError struct {
	scope   string // "ext_key_daily" | "ext_key_monthly" | "upstream_daily" | "upstream_monthly"
	used    int
	limit   int
	message string
}

func (e *limitError) Error() string { return e.message }

// checkTokenLimits verifies the ext key and upstream are within their daily
// and monthly token quotas, based on usage_records aggregated over local-day /
// local-month windows ending at the current time. A limit of 0 means unbounded.
// Returns a *limitError when exceeded, or a wrapped error on DB failure.
func (g *Gateway) checkTokenLimits(k *model.ExtKey, u *model.Upstream) error {
	now := time.Now()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	dayEnd := dayStart.Add(24 * time.Hour)
	monthEnd := monthStart.AddDate(0, 1, 0)

	if k.DailyTokenLimit > 0 {
		used, err := model.SumTokens(g.db, &k.ID, nil, dayStart, dayEnd)
		if err != nil {
			return err
		}
		if used >= k.DailyTokenLimit {
			return &limitError{scope: "ext_key_daily", used: used, limit: k.DailyTokenLimit,
				message: "daily token limit exceeded for API key"}
		}
	}
	if k.MonthlyTokenLimit > 0 {
		used, err := model.SumTokens(g.db, &k.ID, nil, monthStart, monthEnd)
		if err != nil {
			return err
		}
		if used >= k.MonthlyTokenLimit {
			return &limitError{scope: "ext_key_monthly", used: used, limit: k.MonthlyTokenLimit,
				message: "monthly token limit exceeded for API key"}
		}
	}
	if u.DailyTokenLimit > 0 {
		used, err := model.SumTokens(g.db, nil, &u.ID, dayStart, dayEnd)
		if err != nil {
			return err
		}
		if used >= u.DailyTokenLimit {
			return &limitError{scope: "upstream_daily", used: used, limit: u.DailyTokenLimit,
				message: "daily token limit exceeded for upstream"}
		}
	}
	if u.MonthlyTokenLimit > 0 {
		used, err := model.SumTokens(g.db, nil, &u.ID, monthStart, monthEnd)
		if err != nil {
			return err
		}
		if used >= u.MonthlyTokenLimit {
			return &limitError{scope: "upstream_monthly", used: used, limit: u.MonthlyTokenLimit,
				message: "monthly token limit exceeded for upstream"}
		}
	}
	return nil
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
