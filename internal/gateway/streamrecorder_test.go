package gateway

import (
	"testing"

	"github.com/great-magician-01/any-llm/internal/translate"
)

// 完整流：text + thinking(含真签名) + tool_use，断言 Content 三块、思维链带真签名、
// 工具参数拼接、stopReason 捕获。
func TestStreamRecorderFull(t *testing.T) {
	s := newStreamRecorder()
	s.Add(&translate.StreamEvent{Type: "message_start", MessageID: "msg_1"})
	s.Add(&translate.StreamEvent{Type: "content_block_start", Index: 0, Block: &translate.ContentBlock{Type: "text"}})
	s.Add(&translate.StreamEvent{Type: "content_block_delta", Index: 0, Delta: &translate.Delta{Type: "text_delta", Text: "Hello"}})
	s.Add(&translate.StreamEvent{Type: "content_block_delta", Index: 0, Delta: &translate.Delta{Type: "text_delta", Text: " world"}})
	s.Add(&translate.StreamEvent{Type: "content_block_stop", Index: 0})
	s.Add(&translate.StreamEvent{Type: "content_block_start", Index: 1, Block: &translate.ContentBlock{Type: "thinking"}})
	s.Add(&translate.StreamEvent{Type: "content_block_delta", Index: 1, Delta: &translate.Delta{Type: "thinking_delta", Thinking: "let me think"}})
	s.Add(&translate.StreamEvent{Type: "content_block_delta", Index: 1, Delta: &translate.Delta{Type: "signature_delta", Signature: "sig_abc"}})
	s.Add(&translate.StreamEvent{Type: "content_block_stop", Index: 1})
	s.Add(&translate.StreamEvent{Type: "content_block_start", Index: 2, Block: &translate.ContentBlock{Type: "tool_use", ToolUse: &translate.ToolUse{ID: "call_1", Name: "get_weather"}}})
	s.Add(&translate.StreamEvent{Type: "content_block_delta", Index: 2, Delta: &translate.Delta{Type: "input_json_delta", PartialJSON: `{"city":`}})
	s.Add(&translate.StreamEvent{Type: "content_block_delta", Index: 2, Delta: &translate.Delta{Type: "input_json_delta", PartialJSON: `"Paris"}`}})
	s.Add(&translate.StreamEvent{Type: "content_block_stop", Index: 2})
	s.Add(&translate.StreamEvent{Type: "message_delta", StopReason: "tool_calls"})
	s.Add(&translate.StreamEvent{Type: "message_stop"})

	if s.msgID != "msg_1" {
		t.Errorf("msgID = %q, want msg_1", s.msgID)
	}
	if s.stopReason != "tool_calls" {
		t.Errorf("stopReason = %q, want tool_calls", s.stopReason)
	}
	c := s.Content()
	if len(c) != 3 {
		t.Fatalf("Content len = %d, want 3", len(c))
	}
	if c[0].Type != "text" || c[0].Text != "Hello world" {
		t.Errorf("block0 = %+v, want text 'Hello world'", c[0])
	}
	if c[1].Type != "thinking" || c[1].Thinking != "let me think" || c[1].Signature != "sig_abc" {
		t.Errorf("block1 = %+v, want thinking with real signature", c[1])
	}
	if c[2].Type != "tool_use" || c[2].ToolUse == nil {
		t.Fatalf("block2 = %+v, want tool_use", c[2])
	}
	if c[2].ToolUse.ID != "call_1" || c[2].ToolUse.Name != "get_weather" {
		t.Errorf("tool meta = %+v, want call_1/get_weather", c[2].ToolUse)
	}
	if string(c[2].ToolUse.Input) != `{"city":"Paris"}` {
		t.Errorf("tool input = %s, want concatenated JSON", c[2].ToolUse.Input)
	}
}

// DeepSeek 风格：delta 没有对应的 content_block_start，惰性开块推断 kind。
func TestStreamRecorderLazyStart(t *testing.T) {
	s := newStreamRecorder()
	s.Add(&translate.StreamEvent{Type: "content_block_delta", Index: 0, Delta: &translate.Delta{Type: "text_delta", Text: "hi"}})
	s.Add(&translate.StreamEvent{Type: "content_block_delta", Index: 1, Delta: &translate.Delta{Type: "input_json_delta", PartialJSON: `{"a":1}`}})
	c := s.Content()
	if len(c) != 2 {
		t.Fatalf("Content len = %d, want 2", len(c))
	}
	if c[0].Type != "text" || c[0].Text != "hi" {
		t.Errorf("block0 = %+v, want text", c[0])
	}
	if c[1].Type != "tool_use" || string(c[1].ToolUse.Input) != `{"a":1}` {
		t.Errorf("block1 = %+v, want tool_use", c[1])
	}
}

// 块 index 乱序到达时 Content 仍按 index 升序。
func TestStreamRecorderOutOfOrder(t *testing.T) {
	s := newStreamRecorder()
	s.Add(&translate.StreamEvent{Type: "content_block_start", Index: 2, Block: &translate.ContentBlock{Type: "text", Text: "c"}})
	s.Add(&translate.StreamEvent{Type: "content_block_start", Index: 0, Block: &translate.ContentBlock{Type: "text", Text: "a"}})
	s.Add(&translate.StreamEvent{Type: "content_block_start", Index: 1, Block: &translate.ContentBlock{Type: "text", Text: "b"}})
	c := s.Content()
	if len(c) != 3 || c[0].Text != "a" || c[1].Text != "b" || c[2].Text != "c" {
		t.Errorf("Content order = %v, want a,b,c", []string{c[0].Text, c[1].Text, c[2].Text})
	}
}

// 起始块自带完整 input 时预填（非 "{}"）。
func TestStreamRecorderToolInputSeeded(t *testing.T) {
	s := newStreamRecorder()
	s.Add(&translate.StreamEvent{Type: "content_block_start", Index: 0, Block: &translate.ContentBlock{Type: "tool_use", ToolUse: &translate.ToolUse{ID: "c", Name: "f", Input: []byte(`{"x":1}`)}}})
	c := s.Content()
	if string(c[0].ToolUse.Input) != `{"x":1}` {
		t.Errorf("seeded input = %s", c[0].ToolUse.Input)
	}
}
