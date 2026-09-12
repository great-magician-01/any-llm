package webapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

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

	// 上游与模型
	mux.HandleFunc("GET /api/admin/upstreams", a.listUpstreams)
	mux.HandleFunc("POST /api/admin/upstreams", a.createUpstream)
	mux.HandleFunc("GET /api/admin/upstreams/{id}", withID(a.getUpstream))
	mux.HandleFunc("PUT /api/admin/upstreams/{id}", withID(a.updateUpstream))
	mux.HandleFunc("DELETE /api/admin/upstreams/{id}", withID(a.deleteUpstream))
	mux.HandleFunc("POST /api/admin/upstreams/{id}/fetch-models", withID(a.fetchModels))
	mux.HandleFunc("GET /api/admin/upstreams/{id}/models", withID(a.listModels))
	mux.HandleFunc("POST /api/admin/upstreams/{id}/models", withID(a.addModel))
	mux.HandleFunc("PUT /api/admin/upstreams/{id}/models/{mid}", withIDPair(a.updateModel))
	mux.HandleFunc("DELETE /api/admin/upstreams/{id}/models/{mid}", withIDPair(a.deleteModel))
	mux.HandleFunc("GET /api/admin/upstreams/{id}/balances", withID(a.listBalanceHistory))
	mux.HandleFunc("POST /api/admin/upstreams/{id}/balances/refresh", withID(a.refreshBalance))

	// 模型别名与外部 key
	mux.HandleFunc("GET /api/admin/aliases", a.listAliases)
	mux.HandleFunc("POST /api/admin/aliases", a.createAlias)
	mux.HandleFunc("GET /api/admin/aliases/{id}", withID(a.getAlias))
	mux.HandleFunc("PUT /api/admin/aliases/{id}", withID(a.updateAlias))
	mux.HandleFunc("DELETE /api/admin/aliases/{id}", withID(a.deleteAlias))
	mux.HandleFunc("GET /api/admin/keys", a.listKeys)
	mux.HandleFunc("POST /api/admin/keys", a.createKey)
	mux.HandleFunc("PUT /api/admin/keys/{id}", withID(a.updateKey))
	mux.HandleFunc("DELETE /api/admin/keys/{id}", withID(a.deleteKey))

	// 用量统计与余额快照
	mux.HandleFunc("GET /api/admin/usage/summary", a.usageSummary)
	mux.HandleFunc("GET /api/admin/usage/records", a.usageRecords)
	mux.HandleFunc("GET /api/admin/usage/daily", a.usageDaily)
	mux.HandleFunc("GET /api/admin/usage/key/{id}", withID(a.usageKeyTotals))
	mux.HandleFunc("GET /api/admin/usage/upstream/{id}", withID(a.usageUpstreamTotals))
	mux.HandleFunc("GET /api/admin/balances", a.listLatestBalances)
	mux.HandleFunc("POST /api/admin/balances", a.refreshAllBalances)

	// 对话归档（只读）与配置导入导出
	mux.HandleFunc("GET /api/admin/conversations", a.listConversations)
	mux.HandleFunc("GET /api/admin/conversations/{id}", withID(a.getConversation))
	mux.HandleFunc("GET /api/admin/config/export", a.handleConfigExport)
	mux.HandleFunc("POST /api/admin/config/import", a.handleConfigImport)

	return mux
}

// pathID 把 {name} 路径参数解析为正整数 ID。替代旧 parseID 的宽松
// fmt.Sscanf 解析（曾把 "123abc" 静默当作 123）：非法或非正数一律由
// 调用方转成 404。
func pathID(r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// withID 适配需要单个 {id} 路径参数的 handler。
func withID(h func(http.ResponseWriter, *http.Request, int64)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(r, "id")
		if !ok {
			http.NotFound(w, r)
			return
		}
		h(w, r, id)
	}
}

// withIDPair 适配需要 {id} + {mid} 两个路径参数的 handler（上游模型条目）。
func withIDPair(h func(http.ResponseWriter, *http.Request, int64, int64)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(r, "id")
		if !ok {
			http.NotFound(w, r)
			return
		}
		mid, ok := pathID(r, "mid")
		if !ok {
			http.NotFound(w, r)
			return
		}
		h(w, r, id, mid)
	}
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
