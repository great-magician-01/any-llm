package webapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/great-magician-01/any-llm/internal/db"
	"github.com/great-magician-01/any-llm/internal/logger"
	"github.com/great-magician-01/any-llm/internal/model"
)

// handleConversations 对话归档的只读入口（仅 GET）。归档仅 PG 落库
// （网关层门控），SQLite 时列表返回 disabled 标记、详情返回 400，
// 前端据此提示「需要 PostgreSQL」而不是报未知错误。
func (a *API) handleConversations(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "method not allowed", 405)
		return
	}
	if r.URL.Path == "/api/admin/conversations" {
		a.listConversations(w, r)
		return
	}
	id := parseID(strings.TrimPrefix(r.URL.Path, "/api/admin/conversations/"))
	if id == 0 {
		http.NotFound(w, r)
		return
	}
	a.getConversation(w, r, id)
}

func (a *API) listConversations(w http.ResponseWriter, r *http.Request) {
	if db.DialectOf(a.db) != db.DialectPostgres {
		writeJSON(w, 200, map[string]any{"data": []any{}, "total": 0, "disabled": true})
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	size, _ := strconv.Atoi(r.URL.Query().Get("size"))
	records, total, err := model.ConversationRecordsList(a.db, page, size)
	if err != nil {
		logger.Error("admin: conversation list failed", "page", page, "size", size, "err", err)
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"data": records, "total": total})
}

func (a *API) getConversation(w http.ResponseWriter, r *http.Request, id int64) {
	if db.DialectOf(a.db) != db.DialectPostgres {
		writeJSON(w, 400, map[string]any{"error": "conversation archiving requires PostgreSQL"})
		return
	}
	rec, err := model.GetConversation(a.db, id)
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
