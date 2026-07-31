package gateway

import (
	"encoding/json"
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
	return NewSessionStore(d, ttl)
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
