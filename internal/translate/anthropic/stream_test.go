package anthropic

import (
	"strings"
	"testing"

	"github.com/great-magician-01/any-llm/internal/translate"
)

func TestDecodeStreamEvent_MessageStart(t *testing.T) {
	evt, err := DecodeStreamEvent([]byte(`{"type":"message_start","message":{"id":"msg_1","model":"claude-3-5","usage":{"input_tokens":10}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if evt == nil || evt.Type != "message_start" || evt.MessageID != "msg_1" || evt.Model != "claude-3-5" || evt.InputTokens != 10 {
		t.Fatalf("evt=%+v", evt)
	}
}

func TestDecodeStreamEvent_ContentBlockDelta(t *testing.T) {
	evt, err := DecodeStreamEvent([]byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if evt.Type != "content_block_delta" || evt.Delta.Text != "Hi" || evt.Delta.Type != "text_delta" {
		t.Fatalf("evt=%+v", evt)
	}
}

func TestDecodeStreamEvent_Ping(t *testing.T) {
	evt, err := DecodeStreamEvent([]byte(`{"type":"ping"}`))
	if err != nil {
		t.Fatal(err)
	}
	if evt == nil || evt.Type != "ping" {
		t.Fatalf("ping should be forwarded as Type=ping, got %+v", evt)
	}
}

func TestDecodeStreamEvent_MessageDelta(t *testing.T) {
	evt, err := DecodeStreamEvent([]byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":12}}`))
	if err != nil {
		t.Fatal(err)
	}
	if evt.Type != "message_delta" || evt.StopReason != "end_turn" || evt.OutputTokens != 12 {
		t.Fatalf("evt=%+v", evt)
	}
}

func TestEncodeStreamEvent_TextDelta(t *testing.T) {
	out, err := EncodeStreamEvent(&translate.StreamEvent{
		Type: "content_block_delta", Index: 0,
		Delta: &translate.Delta{Type: "text_delta", Text: "Hi"},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.HasPrefix(s, "event: content_block_delta\n") {
		t.Fatalf("no event line: %q", s)
	}
	if !strings.Contains(s, `"text":"Hi"`) {
		t.Fatalf("no text: %q", s)
	}
	if !strings.HasSuffix(s, "\n\n") {
		t.Fatalf("no trailing newlines: %q", s)
	}
}

func TestEncodeStreamEvent_MessageStop(t *testing.T) {
	out, err := EncodeStreamEvent(&translate.StreamEvent{Type: "message_stop"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "event: message_stop\n") {
		t.Fatalf("msg stop: %q", out)
	}
}

func TestEncodeStreamEvent_Ping(t *testing.T) {
	out, err := EncodeStreamEvent(&translate.StreamEvent{Type: "ping"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.HasPrefix(s, "event: ping\n") {
		t.Fatalf("no event line: %q", s)
	}
	if !strings.Contains(s, `"type":"ping"`) {
		t.Fatalf("no ping payload: %q", s)
	}
	if !strings.HasSuffix(s, "\n\n") {
		t.Fatalf("no trailing newlines: %q", s)
	}
}

func TestDecodeStreamEvent_ThinkingBlockStart(t *testing.T) {
	evt, err := DecodeStreamEvent([]byte(`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`))
	if err != nil {
		t.Fatal(err)
	}
	if evt == nil || evt.Type != "content_block_start" || evt.Index != 0 {
		t.Fatalf("evt=%+v", evt)
	}
	if evt.Block == nil || evt.Block.Type != "thinking" {
		t.Fatalf("block=%+v", evt.Block)
	}
}

func TestDecodeStreamEvent_ThinkingDelta(t *testing.T) {
	evt, err := DecodeStreamEvent([]byte(`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"The"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if evt == nil || evt.Type != "content_block_delta" || evt.Index != 0 {
		t.Fatalf("evt=%+v", evt)
	}
	if evt.Delta == nil || evt.Delta.Type != "thinking_delta" || evt.Delta.Thinking != "The" {
		t.Fatalf("delta=%+v", evt.Delta)
	}
}

func TestEncodeStreamEvent_ThinkingDelta(t *testing.T) {
	out, err := EncodeStreamEvent(&translate.StreamEvent{
		Type:  "content_block_delta",
		Index: 0,
		Delta: &translate.Delta{Type: "thinking_delta", Thinking: "The"},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.HasPrefix(s, "event: content_block_delta\n") {
		t.Fatalf("no event line: %q", s)
	}
	if !strings.Contains(s, `"type":"thinking_delta"`) {
		t.Fatalf("no delta type: %q", s)
	}
	if !strings.Contains(s, `"thinking":"The"`) {
		t.Fatalf("no thinking field: %q", s)
	}
}

func TestEncodeStreamEvent_ThinkingBlockStart(t *testing.T) {
	out, err := EncodeStreamEvent(&translate.StreamEvent{
		Type:  "content_block_start",
		Index: 0,
		Block: &translate.ContentBlock{Type: "thinking", Thinking: "", Signature: "sig"},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.HasPrefix(s, "event: content_block_start\n") {
		t.Fatalf("no event line: %q", s)
	}
	if !strings.Contains(s, `"type":"thinking"`) {
		t.Fatalf("no block type: %q", s)
	}
	if !strings.Contains(s, `"signature":"sig"`) {
		t.Fatalf("no signature field: %q", s)
	}
}
