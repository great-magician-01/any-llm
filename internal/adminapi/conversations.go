package adminapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/great-magician-01/any-llm/internal/db"
	"github.com/great-magician-01/any-llm/internal/logger"
	"github.com/great-magician-01/any-llm/internal/store"
)

// 对话归档的只读入口（仅 GET，路由层限定）。归档在 PG / MySQL 上落库
// （网关层门控），SQLite 时列表返回 disabled 标记、详情返回 400，
// 前端据此提示「需要 PostgreSQL 或 MySQL」而不是报未知错误。
func (a *API) listConversations(w http.ResponseWriter, r *http.Request) {
	if !db.DialectOf(a.db).SupportsConversationArchive() {
		writeJSON(w, 200, map[string]any{"data": []any{}, "total": 0, "disabled": true})
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	size, _ := strconv.Atoi(r.URL.Query().Get("size"))
	records, total, err := store.ConversationRecordsList(a.db, page, size)
	if err != nil {
		logger.Error("admin: conversation list failed", "page", page, "size", size, "err", err)
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"data": records, "total": total})
}

func (a *API) getConversation(w http.ResponseWriter, r *http.Request, id int64) {
	if !db.DialectOf(a.db).SupportsConversationArchive() {
		writeJSON(w, 400, map[string]any{"error": "conversation archiving requires PostgreSQL or MySQL"})
		return
	}
	rec, err := store.GetConversation(a.db, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, 404, map[string]any{"error": "conversation not found"})
		return
	}
	if err != nil {
		logger.Error("admin: get conversation failed", "id", id, "err", err)
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"data": rec})
}
