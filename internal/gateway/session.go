package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
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
// 写路径全部并入 db.Writer 单写者通道（读保持直连，读不竞争写锁）：
// touch 与过期清扫是尽力而为的清理，走 DoAsync；Put 走 DoSync——响应 id
// 一旦发给客户端，下一轮 Get 就必须命中，不能像 usage 那样在缓冲满时
// 被静默丢弃。
type SessionStore struct {
	db     *sql.DB
	writer *db.Writer
	ttl    time.Duration
}

func NewSessionStore(sqlDB *sql.DB, writer *db.Writer, ttl time.Duration) *SessionStore {
	return &SessionStore{db: sqlDB, writer: writer, ttl: ttl}
}

// writeAsync 把清理类写排进单写者通道；错误包上 label/id 交回 writer 记日志，
// 否则日志里只剩一句裸的 write failed。writer 为 nil（测试直连）时退回直写，
// 与 conversation.go 的 writer==nil 兜底同款。
func (s *SessionStore) writeAsync(label, id string, fn func(d *sql.DB) error) {
	if s.writer == nil {
		if err := fn(s.db); err != nil {
			logger.Warn("session "+label+" failed", "id", id, "err", err)
		}
		return
	}
	s.writer.DoAsync(func(d *sql.DB) error {
		if err := fn(d); err != nil {
			return fmt.Errorf("session %s (id=%s): %w", label, id, err)
		}
		return nil
	})
}

// Get 返回累积会话消息。已过期（空闲超过 ttl）视为未命中并删除。
func (s *SessionStore) Get(id string) ([]translate.Message, bool, error) {
	var msgsJSON string
	var lastUsed time.Time
	err := s.db.QueryRow(
		db.Rebind(s.db, `SELECT messages, last_used_at FROM response_sessions WHERE id = ?`), id,
	).Scan(&msgsJSON, &lastUsed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("session get: %w", err)
	}
	if time.Since(lastUsed) > s.ttl {
		s.writeAsync("expired delete", id, func(d *sql.DB) error {
			_, err := d.Exec(db.Rebind(d, `DELETE FROM response_sessions WHERE id = ?`), id)
			return err
		})
		return nil, false, nil
	}
	s.writeAsync("touch", id, func(d *sql.DB) error {
		_, err := d.Exec(db.Rebind(d, `UPDATE response_sessions SET last_used_at = ? WHERE id = ?`), time.Now(), id)
		return err
	})
	var msgs []translate.Message
	if err := json.Unmarshal([]byte(msgsJSON), &msgs); err != nil {
		return nil, false, fmt.Errorf("session decode: %w", err)
	}
	return msgs, true, nil
}

// Put 保存（或覆盖）会话，并惰性清扫过期会话。
// 时间统一用 Go time.Time（服务器本地时区）绑定写入，与全库其它表一致：
// PG 的 timestamp 列由驱动codec 存取均为本地墙钟（见 internal/db/pgtime.go）。
// 不用 SQLite CURRENT_TIMESTAMP 默认值：它只有秒级精度，亚秒间隔写入的两行
// 会落在同一秒，`last_used_at < ?` 的字符串比较无法区分；绑定参数带亚秒精度，
// 写入与清扫共用同一驱动格式，比较才严格按时间序。
func (s *SessionStore) Put(id string, msgs []translate.Message) error {
	data, err := json.Marshal(msgs)
	if err != nil {
		return fmt.Errorf("session encode: %w", err)
	}
	now := time.Now()
	q := `INSERT INTO response_sessions (id, messages, created_at, last_used_at)
		 VALUES (?, ?, ?, ?)` + db.UpsertSuffix(s.db, "id", "messages", "last_used_at")
	// VALUES(col) 是 SQL 里的函数引用（指向同一条语句 VALUES 子句中该列的值），
	// 由服务端解析，不占绑定参数位 —— 所以三种方言都只传 4 个参数，无需按方言
	// 分流。PG/SQLite 的 excluded.<col> 同理。
	put := func(d *sql.DB) error {
		_, err := d.Exec(db.Rebind(d, q), id, string(data), now, now)
		return err
	}
	if s.writer == nil {
		err = put(s.db)
	} else {
		err = s.writer.DoSync(put)
	}
	if err != nil {
		return fmt.Errorf("session put: %w", err)
	}
	s.writeAsync("sweep", "", func(d *sql.DB) error {
		_, err := d.Exec(db.Rebind(d, `DELETE FROM response_sessions WHERE last_used_at < ?`),
			time.Now().Add(-s.ttl))
		return err
	})
	return nil
}
