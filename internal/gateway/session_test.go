package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/great-magician-01/any-llm/internal/db"
	"github.com/great-magician-01/any-llm/internal/translate"
)

func newSessionStore(t *testing.T, ttl time.Duration) *SessionStore {
	t.Helper()
	d, err := db.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	// nil writer = 直写（生产路径走 Gateway 装配的 writer，另测）
	return NewSessionStore(d, nil, ttl)
}

func newWriterSessionStore(t *testing.T, ttl time.Duration) (*SessionStore, *db.Writer, *sql.DB) {
	t.Helper()
	// 不用 :memory:：writer 是独立 goroutine，池化多连接下各连接拿到的是不同
	// 的空库（router_test/writer_test 同款做法——文件型临时库所有连接共享）。
	d, err := db.OpenSQLite(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	w := db.NewWriter(d, 16)
	w.Start()
	t.Cleanup(w.Stop)
	return NewSessionStore(d, w, ttl), w, d
}

func TestSessionStore_PutGet(t *testing.T) {
	s := newSessionStore(t, time.Hour)
	msgs := []translate.Message{{Role: "user", Content: []translate.ContentBlock{{Type: "text", Text: "hi"}}}}
	if err := s.Put("resp_1", msgs); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Get("resp_1")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if len(got) != 1 || got[0].Content[0].Text != "hi" {
		t.Fatalf("got=%+v", got)
	}
	// 覆盖更新
	msgs2 := append(msgs, translate.Message{Role: "assistant", Content: []translate.ContentBlock{{Type: "text", Text: "yo"}}})
	if err := s.Put("resp_1", msgs2); err != nil {
		t.Fatal(err)
	}
	got2, ok, _ := s.Get("resp_1")
	if !ok || len(got2) != 2 {
		t.Fatalf("got2=%+v ok=%v", got2, ok)
	}
}

func TestSessionStore_Miss(t *testing.T) {
	s := newSessionStore(t, time.Hour)
	if _, ok, err := s.Get("resp_unknown"); err != nil || ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestSessionStore_Expiry(t *testing.T) {
	s := newSessionStore(t, time.Millisecond)
	if err := s.Put("resp_1", []translate.Message{{Role: "user", Content: []translate.ContentBlock{{Type: "text", Text: "hi"}}}}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, ok, err := s.Get("resp_1"); err != nil || ok {
		t.Fatalf("expired session still found: ok=%v err=%v", ok, err)
	}
}

func TestSessionStore_JSONRoundTrip(t *testing.T) {
	s := newSessionStore(t, time.Hour)
	msgs := []translate.Message{
		{Role: "user", Content: []translate.ContentBlock{
			{Type: "tool_result", ToolResult: &translate.ToolResult{ToolUseID: "call_1", Content: []translate.ContentBlock{{Type: "text", Text: "sunny"}}}},
		}},
		{Role: "assistant", Content: []translate.ContentBlock{
			{Type: "tool_use", ToolUse: &translate.ToolUse{ID: "call_1", Name: "get_weather", Input: json.RawMessage(`{"city":"SF"}`)}},
		}},
	}
	if err := s.Put("resp_1", msgs); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Get("resp_1")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if got[0].Content[0].ToolResult.ToolUseID != "call_1" || got[1].Content[0].ToolUse.Input[0] != '{' {
		t.Fatalf("round trip broken: %+v", got)
	}
}

func TestSessionStore_SweepOnPut(t *testing.T) {
	s := newSessionStore(t, time.Millisecond)
	_ = s.Put("resp_old", []translate.Message{})
	time.Sleep(5 * time.Millisecond)
	// 第二次 Put 触发清扫，过期行应被删除
	_ = s.Put("resp_new", []translate.Message{{Role: "user", Content: []translate.ContentBlock{{Type: "text", Text: "x"}}}})
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM response_sessions`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("sweep failed: %d rows remain", n)
	}
}

func TestSessionStore_WriterPath(t *testing.T) {
	s, w, d := newWriterSessionStore(t, time.Hour)

	msgs := []translate.Message{{Role: "user", Content: []translate.ContentBlock{{Type: "text", Text: "hi"}}}}
	if err := s.Put("resp_1", msgs); err != nil {
		t.Fatal(err)
	}
	// Put 走 DoSync：返回即已落库，Get（直读）必须立刻命中
	got, ok, err := s.Get("resp_1")
	if err != nil || !ok {
		t.Fatalf("get after sync put: ok=%v err=%v", ok, err)
	}
	if len(got) != 1 || got[0].Content[0].Text != "hi" {
		t.Fatalf("got=%+v", got)
	}

	// 直接塞一行已过期的会话（绕过 Store，模拟历史遗留），再 Put 触发异步清扫
	old := time.Now().Add(-2 * time.Hour)
	if _, err := d.Exec(`INSERT INTO response_sessions (id, messages, created_at, last_used_at) VALUES (?, ?, ?, ?)`,
		"resp_old", "[]", old, old); err != nil {
		t.Fatal(err)
	}
	if err := s.Put("resp_2", msgs); err != nil {
		t.Fatal(err)
	}
	w.Stop() // 排空 DoAsync 队列，清扫必须已执行
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM response_sessions`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("sweep through writer failed: %d rows remain", n)
	}
}

func TestSessionStore_WriterStopped(t *testing.T) {
	s, w, _ := newWriterSessionStore(t, time.Hour)
	w.Stop()
	// 关停后 DoSync 必须立刻报错返回，而不是阻塞或静默丢
	err := s.Put("resp_x", nil)
	if !errors.Is(err, db.ErrWriterStopped) {
		t.Fatalf("err=%v", err)
	}
}
