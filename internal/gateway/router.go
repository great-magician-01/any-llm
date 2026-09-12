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
		if !u.Enabled { // 禁用的上游不对外暴露模型（行保留，重新启用即恢复）
			continue
		}
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
	// 固定对外模型（别名）：以别名本身作为模型 id，客户端可发现并直接请求。
	aliases, err := model.ListAliases(g.db)
	if err != nil {
		logger.Warn("gateway: list aliases failed, skipping", "err", err)
	} else {
		for _, a := range aliases {
			hasUsable := false
			for _, b := range a.Bindings {
				if b.UpstreamName != "" && b.UpstreamEnabled { // 绑定指向的上游仍活跃且启用
					hasUsable = true
					break
				}
			}
			if !hasUsable {
				continue
			}
			data = append(data, modelObj{
				ID:      a.Name,
				Object:  "model",
				Created: a.CreatedAt.Unix(),
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

	// 按 key 的模型白名单：条目为对外模型名（别名或 upstream/model，与
	// /v1/models 列出的 id 一致），空 = 不限；仅精确匹配。缺 model 字段的
	// 畸形请求留给后续 400，不在这里误报 403。
	if probe.Model != "" && !k.AllowsModel(probe.Model) {
		logger.Info("gateway: model not allowed for key",
			"key_id", k.ID, "key_label", k.Label, "model", probe.Model)
		WriteError(w, 403, inFormat, "model '"+probe.Model+"' is not allowed for this API key", "permission_error")
		return
	}

	// 固定对外模型（别名）：对整个 model 字符串精确匹配，命中后按绑定优先级
	// 得到候选链，dispatch 内逐候选尝试并自动故障转移。别名优先于
	// 'name/model' 直连拆分（管理员显式配置即可遮蔽直连路由）。
	found, targets, err := model.ResolveAliasTargets(g.db, probe.Model)
	if err != nil {
		logger.Error("gateway: resolve model alias DB error", "model", probe.Model, "err", err)
		WriteError(w, 500, inFormat, "failed to resolve model alias: "+err.Error(), "internal_error")
		return
	}
	if found {
		if len(targets) == 0 {
			WriteError(w, 404, inFormat, "model alias '"+probe.Model+"' has no available bindings", "not_found_error")
			return
		}
		if g.writeLimitError(w, inFormat, k, "", g.checkKeyLimits(k)) {
			return
		}
		// 上游限额在 dispatch（流式会先 flush 200 头部）之前过滤：超限的候选
		// 直接跳过；全部超限则 429。
		usable := make([]model.AliasTarget, 0, len(targets))
		var firstLimit *limitError
		for i := range targets {
			err := g.checkUpstreamLimits(targets[i].Upstream)
			if err == nil {
				usable = append(usable, targets[i])
				continue
			}
			if le, ok := err.(*limitError); ok {
				logger.Info("alias candidate skipped: token limit exceeded",
					"alias", probe.Model, "key_id", k.ID,
					"upstream", targets[i].Upstream.Name, "scope", le.scope,
					"used", le.used, "limit", le.limit,
				)
				if firstLimit == nil {
					firstLimit = le
				}
				continue
			}
			g.writeLimitError(w, inFormat, k, targets[i].Upstream.Name, err)
			return
		}
		if len(usable) == 0 {
			WriteError(w, 429, inFormat, firstLimit.message, "rate_limit_error")
			return
		}
		g.dispatch(w, r, inFormat, k, usable, body)
		return
	}

	name, realModel, ok := splitModel(probe.Model)
	if !ok {
		WriteError(w, 400, inFormat, "model must be in 'name/model' format or match a configured alias", "invalid_request_error")
		return
	}

	u, err := model.GetUpstreamByName(g.db, name)
	if err != nil {
		WriteError(w, 404, inFormat, "upstream '"+name+"' not found", "not_found_error")
		return
	}
	if !u.Enabled {
		WriteError(w, 404, inFormat, "upstream '"+name+"' is disabled", "not_found_error")
		return
	}

	if g.writeLimitError(w, inFormat, k, u.Name, g.checkKeyLimits(k)) {
		return
	}
	if g.writeLimitError(w, inFormat, k, u.Name, g.checkUpstreamLimits(u)) {
		return
	}

	g.dispatch(w, r, inFormat, k, []model.AliasTarget{{Upstream: u, ModelName: realModel}}, body)
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

// writeLimitError 把限额检查错误写成 HTTP 响应；err 为 nil 时返回 false。
// 超限返回 429 并记日志，DB 错误返回 500。upstreamName 仅用于日志（key 级
// 限额没有对应上游时可传空串）。
func (g *Gateway) writeLimitError(w http.ResponseWriter, inFormat string, k *model.ExtKey, upstreamName string, err error) bool {
	if err == nil {
		return false
	}
	if le, ok := err.(*limitError); ok {
		WriteError(w, 429, inFormat, le.message, "rate_limit_error")
		logger.Info("token limit exceeded",
			"key_id", k.ID, "key_label", k.Label,
			"upstream", upstreamName, "scope", le.scope,
			"used", le.used, "limit", le.limit,
		)
		return true
	}
	logger.Error("gateway: check token limits DB error", "key_id", k.ID, "upstream", upstreamName, "err", err)
	WriteError(w, 500, inFormat, "failed to check token limits: "+err.Error(), "internal_error")
	return true
}

// checkKeyLimits verifies the ext key is within its daily and monthly token
// quotas. A limit of 0 means unbounded. Returns a *limitError when exceeded,
// or a wrapped error on DB failure.
func (g *Gateway) checkKeyLimits(k *model.ExtKey) error {
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
	return nil
}

// checkUpstreamLimits verifies the upstream is within its daily and monthly
// token quotas. A limit of 0 means unbounded. Returns a *limitError when
// exceeded, or a wrapped error on DB failure.
func (g *Gateway) checkUpstreamLimits(u *model.Upstream) error {
	now := time.Now()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	dayEnd := dayStart.Add(24 * time.Hour)
	monthEnd := monthStart.AddDate(0, 1, 0)

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
