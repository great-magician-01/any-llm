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

// 会话聚合视图的只读入口（仅 GET，路由层限定）。与对话归档同门控：
// 仅 PG / MySQL 落库；SQLite 时列表返回 disabled 标记、详情返回 400。
func (a *API) listConvSessions(w http.ResponseWriter, r *http.Request) {
	if !db.DialectOf(a.db).SupportsConversationArchive() {
		writeJSON(w, 200, map[string]any{"data": []any{}, "total": 0, "disabled": true})
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	size, _ := strconv.Atoi(r.URL.Query().Get("size"))
	sessions, total, err := store.ConversationSessionsList(a.db, page, size)
	if err != nil {
		logger.Error("admin: conv session list failed", "page", page, "size", size, "err", err)
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"data": sessions, "total": total})
}

func (a *API) getConvSession(w http.ResponseWriter, r *http.Request, id int64) {
	if !db.DialectOf(a.db).SupportsConversationArchive() {
		writeJSON(w, 400, map[string]any{"error": "conversation archiving requires PostgreSQL or MySQL"})
		return
	}
	sess, err := store.GetConversationSession(a.db, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, 404, map[string]any{"error": "conversation session not found"})
		return
	}
	if err != nil {
		logger.Error("admin: get conv session failed", "id", id, "err", err)
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"data": sess})
}
