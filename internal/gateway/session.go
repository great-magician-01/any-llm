package gateway

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/great-magician-01/any-llm/internal/db"
	"github.com/great-magician-01/any-llm/internal/logger"
	"github.com/great-magician-01/any-llm/internal/translate"
)

// sessionTTL 是会话空闲过期时间：超过后 previous_response_id 返回 400。
const sessionTTL = 24 * time.Hour

// SessionStore 在 response_sessions 表里维护 Responses 有状态会话。
// 客户端用 store + previous_response_id 延续对话，历史由网关累积存储，
// 转发给上游的调用始终是无状态、带全量历史的。
type SessionStore struct {
	db  *sql.DB
	ttl time.Duration
}

func NewSessionStore(db *sql.DB, ttl time.Duration) *SessionStore {
	return &SessionStore{db: db, ttl: ttl}
}

// Get 返回累积会话消息。已过期（空闲超过 ttl）视为未命中并删除。
func (s *SessionStore) Get(id string) ([]translate.Message, bool, error) {
	var msgsJSON string
	var lastUsed time.Time
	err := s.db.QueryRow(
		db.Rebind(s.db, `SELECT messages, last_used_at FROM response_sessions WHERE id = ?`), id,
	).Scan(&msgsJSON, &lastUsed)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("session get: %w", err)
	}
	if time.Since(lastUsed) > s.ttl {
		_, _ = s.db.Exec(db.Rebind(s.db, `DELETE FROM response_sessions WHERE id = ?`), id)
		return nil, false, nil
	}
	if _, err := s.db.Exec(db.Rebind(s.db, `UPDATE response_sessions SET last_used_at = ? WHERE id = ?`), time.Now().UTC(), id); err != nil {
		logger.Warn("session touch failed", "id", id, "err", err)
	}
	var msgs []translate.Message
	if err := json.Unmarshal([]byte(msgsJSON), &msgs); err != nil {
		return nil, false, fmt.Errorf("session decode: %w", err)
	}
	return msgs, true, nil
}

// Put 保存（或覆盖）会话，并惰性清扫过期会话。
// 时间统一用 Go time.Time（UTC）绑定写入：SQLite CURRENT_TIMESTAMP 只有秒级
// 精度，亚秒间隔写入的两行会落在同一秒，`last_used_at < ?` 的字符串比较无法
// 区分；绑定参数带亚秒精度，写入与清扫共用同一驱动格式，比较才严格按时间序。
func (s *SessionStore) Put(id string, msgs []translate.Message) error {
	data, err := json.Marshal(msgs)
	if err != nil {
		return fmt.Errorf("session encode: %w", err)
	}
	now := time.Now().UTC()
	_, err = s.db.Exec(
		db.Rebind(s.db, `INSERT INTO response_sessions (id, messages, created_at, last_used_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET messages = excluded.messages, last_used_at = excluded.last_used_at`),
		id, string(data), now, now,
	)
	if err != nil {
		return fmt.Errorf("session put: %w", err)
	}
	if _, err := s.db.Exec(db.Rebind(s.db, `DELETE FROM response_sessions WHERE last_used_at < ?`),
		time.Now().UTC().Add(-s.ttl)); err != nil {
		logger.Warn("session sweep failed", "err", err)
	}
	return nil
}
