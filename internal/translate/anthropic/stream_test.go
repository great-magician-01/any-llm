package anthropic

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/great-magician-01/any-llm/internal/translate"
)

func TestDecodeStreamEvent_MessageStart(t *testing.T) {
	evt, err := DecodeStreamEvent([]byte(`{"type":"message_start","message":{"id":"msg_1","model":"claude-3-5","usage":{"input_tokens":10,"output_tokens":1}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if evt == nil || evt.Type != "message_start" || evt.MessageID != "msg_1" || evt.Model != "claude-3-5" || evt.InputTokens != 10 {
		t.Fatalf("evt=%+v", evt)
	}
	if evt.OutputTokens != 1 {
		t.Fatalf("message_start output_tokens=%d want 1", evt.OutputTokens)
	}
	if len(evt.RawMessage) == 0 {
		t.Fatalf("RawMessage should be preserved for pass-through")
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
	evt, err := DecodeStreamEvent([]byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":12}}`))
	if err != nil {
		t.Fatal(err)
	}
	if evt.Type != "message_delta" || evt.StopReason != "end_turn" || evt.OutputTokens != 12 {
		t.Fatalf("evt=%+v", evt)
	}
	if len(evt.RawUsage) == 0 {
		t.Fatalf("RawUsage should be preserved for pass-through")
	}
}

// TestEncodeStreamEvent_MessageStartFromIR verifies that when no raw message
// is available (OpenAI->Anthropic cross-format), the encoder produces a
// complete, Anthropic-SDK-valid message object with all required fields.
func TestEncodeStreamEvent_MessageStartFromIR(t *testing.T) {
	out, err := EncodeStreamEvent(&translate.StreamEvent{
		Type:         "message_start",
		MessageID:    "msg_1",
		Model:        "claude-3-5",
		InputTokens:  25,
		OutputTokens: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.HasPrefix(s, "event: message_start\n") {
		t.Fatalf("no event line: %q", s)
	}
	for _, want := range []string{
		`"type":"message"`,
		`"role":"assistant"`,
		`"content":[]`,
		`"stop_reason":null`,
		`"stop_sequence":null`,
		`"input_tokens":25`,
		`"output_tokens":1`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("message_start missing %s: %q", want, s)
		}
	}
}

// TestEncodeStreamEvent_MessageStartRawPassThrough verifies that when raw
// message JSON is available (Anthropic->Anthropic), the encoder passes it
// through verbatim, preserving all fields (including cache tokens, etc.).
func TestEncodeStreamEvent_MessageStartRawPassThrough(t *testing.T) {
	rawMsg := json.RawMessage(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-3-5","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":25,"output_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}`)
	out, err := EncodeStreamEvent(&translate.StreamEvent{
		Type:        "message_start",
		RawMessage:  rawMsg,
		InputTokens: 25,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, `"cache_creation_input_tokens":0`) {
		t.Fatalf("raw pass-through should preserve cache tokens: %q", s)
	}
	if !strings.Contains(s, `"cache_read_input_tokens":0`) {
		t.Fatalf("raw pass-through should preserve cache tokens: %q", s)
	}
	if !strings.Contains(s, `"type":"message"`) {
		t.Fatalf("raw pass-through should preserve type field: %q", s)
	}
}

// TestEncodeStreamEvent_MessageDeltaRawUsagePassThrough verifies that raw
// usage JSON is passed through verbatim for Anthropic->Anthropic.
func TestEncodeStreamEvent_MessageDeltaRawUsagePassThrough(t *testing.T) {
	rawUsage := json.RawMessage(`{"output_tokens":15,"cache_creation_input_tokens":3}`)
	out, err := EncodeStreamEvent(&translate.StreamEvent{
		Type:       "message_delta",
		StopReason: "end_turn",
		RawUsage:   rawUsage,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, `"cache_creation_input_tokens":3`) {
		t.Fatalf("raw usage pass-through should preserve cache tokens: %q", s)
	}
	if !strings.Contains(s, `"output_tokens":15`) {
		t.Fatalf("raw usage pass-through should preserve output_tokens: %q", s)
	}
	if !strings.Contains(s, `"stop_sequence":null`) {
		t.Fatalf("message_delta should include stop_sequence:null: %q", s)
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

func TestEncodeStreamEvent_MessageDeltaWithInputTokens(t *testing.T) {
	out, err := EncodeStreamEvent(&translate.StreamEvent{
		Type:         "message_delta",
		StopReason:   "stop",
		InputTokens:  42,
		OutputTokens: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.HasPrefix(s, "event: message_delta\n") {
		t.Fatalf("no event line: %q", s)
	}
	if !strings.Contains(s, `"output_tokens":7`) {
		t.Fatalf("missing output_tokens: %q", s)
	}
	if !strings.Contains(s, `"input_tokens":42`) {
		t.Fatalf("missing input_tokens: %q", s)
	}
	if !strings.Contains(s, `"stop_reason":"end_turn"`) {
		t.Fatalf("stop_reason should be mapped to end_turn: %q", s)
	}
	if !strings.Contains(s, `"stop_sequence":null`) {
		t.Fatalf("missing stop_sequence:null: %q", s)
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

// TestEncodeStreamEvent_ToolUseBlockWithoutToolUse covers the synthesized
// content_block_start the gateway emits when the upstream omits it (e.g.
// DeepSeek's Anthropic API). The synthesized block has only Type set, so
// EncodeStreamEvent must not dereference a nil ToolUse and must emit a
// well-formed frame.
func TestEncodeStreamEvent_ToolUseBlockWithoutToolUse(t *testing.T) {
	f, err := EncodeStreamEvent(&translate.StreamEvent{
		Type:  "content_block_start",
		Index: 1,
		Block: &translate.ContentBlock{Type: "tool_use"},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(f)
	if !strings.Contains(s, `"type":"tool_use"`) {
		t.Fatalf("frame=%q want tool_use block", s)
	}
	if !strings.HasPrefix(s, "event: content_block_start") {
		t.Fatalf("frame=%q wrong event name", s)
	}
}

// TestDecodeStreamEvent_CacheUsage verifies cache fields are extracted from
// message_start and message_delta usage.
func TestDecodeStreamEvent_CacheUsage(t *testing.T) {
	start, err := DecodeStreamEvent([]byte(`{"type":"message_start","message":{"id":"msg_1","model":"m","usage":{"input_tokens":59,"output_tokens":0,"cache_creation_input_tokens":120,"cache_read_input_tokens":384}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if start.InputTokens != 59 || start.CacheReadTokens != 384 || start.CacheCreationTokens != 120 {
		t.Fatalf("start usage=%+v", start)
	}
	delta, err := DecodeStreamEvent([]byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"input_tokens":59,"output_tokens":71,"cache_creation_input_tokens":0,"cache_read_input_tokens":384}}`))
	if err != nil {
		t.Fatal(err)
	}
	if delta.OutputTokens != 71 || delta.CacheReadTokens != 384 || delta.CacheCreationTokens != 0 {
		t.Fatalf("delta usage=%+v", delta)
	}
}

// TestEncodeStreamEvent_CacheUsageFallback verifies the IR-built (cross-format)
// message_start and message_delta carry cache fields in their usage.
func TestEncodeStreamEvent_CacheUsageFallback(t *testing.T) {
	start, err := EncodeStreamEvent(&translate.StreamEvent{
		Type:                "message_start",
		MessageID:           "msg_1",
		Model:               "m",
		InputTokens:         59,
		CacheReadTokens:     384,
		CacheCreationTokens: 120,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(start), `"cache_read_input_tokens":384`) ||
		!strings.Contains(string(start), `"cache_creation_input_tokens":120`) {
		t.Fatalf("start missing cache fields: %s", start)
	}
	delta, err := EncodeStreamEvent(&translate.StreamEvent{
		Type:            "message_delta",
		StopReason:      "stop",
		InputTokens:     59,
		OutputTokens:    71,
		CacheReadTokens: 384,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(delta), `"cache_read_input_tokens":384`) {
		t.Fatalf("delta missing cache_read: %s", delta)
	}
	if !strings.Contains(string(delta), `"output_tokens":71`) {
		t.Fatalf("delta missing output_tokens: %s", delta)
	}
}

// TestStreamEncoder_ToolOnlyIndexRewritten verifies that a stream whose first
// content_block arrives at IR index 1 (the OpenAI StreamDecoder reserves
// index 0 for a text block and starts tool blocks at index 1, so a tool-only
// turn never opens index 0) is rewritten so the Anthropic client sees a
// 0-based, contiguous index sequence as the spec requires.
func TestStreamEncoder_ToolOnlyIndexRewritten(t *testing.T) {
	e := NewStreamEncoder()
	var lines []string
	enc := func(evt *translate.StreamEvent) {
		fs, err := e.Encode(evt)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range fs {
			lines = append(lines, string(f))
		}
	}
	enc(&translate.StreamEvent{Type: "message_start", MessageID: "m1", Model: "claude-3-5"})
	enc(&translate.StreamEvent{Type: "content_block_start", Index: 1, Block: &translate.ContentBlock{Type: "tool_use", ToolUse: &translate.ToolUse{ID: "call_1", Name: "get_weather"}}})
	enc(&translate.StreamEvent{Type: "content_block_delta", Index: 1, Delta: &translate.Delta{Type: "input_json_delta", PartialJSON: `{"city":"SF"}`}})
	enc(&translate.StreamEvent{Type: "content_block_stop", Index: 1})
	enc(&translate.StreamEvent{Type: "message_delta", StopReason: "tool_calls"})
	enc(&translate.StreamEvent{Type: "message_stop"})

	joined := strings.Join(lines, "")
	if !strings.Contains(joined, `"index":0`) {
		t.Fatalf("expected the rewritten tool block to be at index 0, got: %s", joined)
	}
	if strings.Contains(joined, `"index":1`) {
		t.Fatalf("original IR index 1 should have been rewritten to 0, got: %s", joined)
	}
	// sanity: the tool_use block still carries its id/name
	if !strings.Contains(joined, `"id":"call_1"`) || !strings.Contains(joined, `"name":"get_weather"`) {
		t.Fatalf("tool block payload lost: %s", joined)
	}
}

// TestStreamEncoder_TextAndToolIndicesPreserved verifies that a normal
// text-then-tool stream (text at index 0, tool at index 1) is emitted with
// the SAME indices — the remap must not reorder already-0-based sequences.
func TestStreamEncoder_TextAndToolIndicesPreserved(t *testing.T) {
	e := NewStreamEncoder()
	var lines []string
	enc := func(evt *translate.StreamEvent) {
		fs, err := e.Encode(evt)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range fs {
			lines = append(lines, string(f))
		}
	}
	enc(&translate.StreamEvent{Type: "message_start", MessageID: "m1"})
	enc(&translate.StreamEvent{Type: "content_block_start", Index: 0, Block: &translate.ContentBlock{Type: "text"}})
	enc(&translate.StreamEvent{Type: "content_block_delta", Index: 0, Delta: &translate.Delta{Type: "text_delta", Text: "Hi"}})
	enc(&translate.StreamEvent{Type: "content_block_stop", Index: 0})
	enc(&translate.StreamEvent{Type: "content_block_start", Index: 1, Block: &translate.ContentBlock{Type: "tool_use", ToolUse: &translate.ToolUse{ID: "call_1", Name: "get_weather"}}})
	enc(&translate.StreamEvent{Type: "content_block_delta", Index: 1, Delta: &translate.Delta{Type: "input_json_delta", PartialJSON: `{"city":"SF"}`}})
	enc(&translate.StreamEvent{Type: "content_block_stop", Index: 1})
	enc(&translate.StreamEvent{Type: "message_delta", StopReason: "tool_calls"})
	enc(&translate.StreamEvent{Type: "message_stop"})

	joined := strings.Join(lines, "")
	// both 0 and 1 must appear; no 2 should appear.
	if !strings.Contains(joined, `"index":0`) || !strings.Contains(joined, `"index":1`) {
		t.Fatalf("text(0) and tool(1) indices should be preserved, got: %s", joined)
	}
	if strings.Contains(joined, `"index":2`) {
		t.Fatalf("no index 2 expected (text+tool only), got: %s", joined)
	}
}

// TestServerBlockRoundTrip verifies hosted server blocks (server_tool_use and
// web_search_tool_result) survive the stream decode→encode round trip with
// their payload intact — the gateway must forward search results to the
// client instead of stripping them to a bare type.
func TestServerBlockRoundTrip(t *testing.T) {
	startRaw := []byte(`{"type":"web_search_tool_result","tool_use_id":"call_1","content":[{"type":"web_search_result","title":"T","url":"https://x","encrypted_content":"abc"}]}`)
	blk, err := decodeStreamContentBlock(startRaw)
	if err != nil {
		t.Fatal(err)
	}
	if blk.Type != "web_search_tool_result" {
		t.Fatalf("type=%q", blk.Type)
	}
	if blk.Extra == nil || blk.Extra["tool_use_id"] != "call_1" {
		t.Fatalf("extra lost: %v", blk.Extra)
	}
	evt := &translate.StreamEvent{Type: "content_block_start", Index: 2, Block: blk}
	f, err := EncodeStreamEvent(evt)
	if err != nil {
		t.Fatal(err)
	}
	s := string(f)
	for _, want := range []string{`"web_search_tool_result"`, `"tool_use_id":"call_1"`, `"encrypted_content":"abc"`, `"https://x"`} {
		if !strings.Contains(s, want) {
			t.Fatalf("round trip lost %s: %s", want, s)
		}
	}

	// server_tool_use 同样保留 id/name/caller。
	su, err := decodeStreamContentBlock([]byte(`{"type":"server_tool_use","id":"call_1","name":"web_search","input":{},"caller":{"type":"direct"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if su.Extra == nil || su.Extra["id"] != "call_1" || su.Extra["caller"] == nil {
		t.Fatalf("server_tool_use extra lost: %v", su.Extra)
	}
}
