package anthropic

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/great-magician-01/any-llm/internal/translate"
)

// TestDecodeResponse_SafeguardResults verifies that the top-level
// `safeguard_results` field (Claude Code server-side classifier verdicts)
// is captured into Response.Extra so the gateway can pass it back to the
// client unchanged.
func TestDecodeResponse_SafeguardResults(t *testing.T) {
	body := []byte(`{
		"id":"msg_1","type":"message","role":"assistant","model":"m",
		"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn",
		"usage":{"input_tokens":1,"output_tokens":1},
		"safeguard_results":[{"type":"dangerous_tool_use","status":{"type":"available","tool_uses":{}}}]
	}`)
	resp, err := DecodeResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	raw, ok := resp.Extra["safeguard_results"]
	if !ok {
		t.Fatal("safeguard_results missing from Response.Extra")
	}
	rm, ok := raw.(json.RawMessage)
	if !ok {
		t.Fatalf("safeguard_results type = %T, want json.RawMessage", raw)
	}
	var arr []map[string]any
	if err := json.Unmarshal(rm, &arr); err != nil {
		t.Fatal(err)
	}
	if len(arr) != 1 || arr[0]["type"] != "dangerous_tool_use" {
		t.Fatalf("safeguard_results content = %v", arr)
	}
}

// TestEncodeResponse_SafeguardResults verifies that Extra fields are merged
// back into the encoded response top level.
func TestEncodeResponse_SafeguardResults(t *testing.T) {
	sr := json.RawMessage(`[{"type":"dangerous_tool_use","status":{"type":"available","tool_uses":{}}}]`)
	resp := &translate.Response{
		ID:    "msg_1",
		Model: "m",
		Content: []translate.ContentBlock{
			{Type: "text", Text: "ok"},
		},
		StopReason: "stop",
		Usage:      translate.Usage{InputTokens: 1, OutputTokens: 1},
		Extra:      map[string]any{"safeguard_results": sr},
	}
	out, err := EncodeResponse(resp)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, `"safeguard_results"`) {
		t.Fatalf("response missing safeguard_results: %s", s)
	}
	var probe map[string]any
	if err := json.Unmarshal(out, &probe); err != nil {
		t.Fatal(err)
	}
	arr, ok := probe["safeguard_results"].([]any)
	if !ok || len(arr) != 1 {
		t.Fatalf("safeguard_results shape wrong: %v", probe["safeguard_results"])
	}
}

// TestEncodeResponse_ExtraDoesNotOverwrite verifies that a malicious or
// conflicting Extra key cannot clobber the canonical response fields.
func TestEncodeResponse_ExtraDoesNotOverwrite(t *testing.T) {
	resp := &translate.Response{
		ID:         "msg_1",
		Model:      "m",
		Content:    []translate.ContentBlock{{Type: "text", Text: "ok"}},
		StopReason: "stop",
		Usage:      translate.Usage{InputTokens: 1, OutputTokens: 1},
		Extra:      map[string]any{"id": "evil", "usage": "evil"},
	}
	out, err := EncodeResponse(resp)
	if err != nil {
		t.Fatal(err)
	}
	var probe map[string]any
	if err := json.Unmarshal(out, &probe); err != nil {
		t.Fatal(err)
	}
	if probe["id"] != "msg_1" {
		t.Fatalf("id overwritten: %v", probe["id"])
	}
	if _, ok := probe["usage"].(map[string]any); !ok {
		t.Fatalf("usage overwritten: %v", probe["usage"])
	}
}

// TestDecodeStreamEvent_MessageDeltaSafeguardResults verifies that
// `safeguard_results` inside a `message_delta` delta object is preserved.
func TestDecodeStreamEvent_MessageDeltaSafeguardResults(t *testing.T) {
	evt, err := DecodeStreamEvent([]byte(`{
		"type":"message_delta",
		"delta":{"stop_reason":"end_turn","stop_sequence":null,
			"safeguard_results":[{"type":"dangerous_tool_use","status":{"type":"available","tool_uses":{}}}]},
		"usage":{"output_tokens":5}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if evt.Type != "message_delta" {
		t.Fatalf("type=%q want message_delta", evt.Type)
	}
	if evt.StopReason != "end_turn" {
		t.Fatalf("stop_reason=%q want end_turn", evt.StopReason)
	}
	raw, ok := evt.DeltaExtras["safeguard_results"]
	if !ok {
		t.Fatal("safeguard_results missing from DeltaExtras")
	}
	var arr []map[string]any
	if err := json.Unmarshal(raw, &arr); err != nil {
		t.Fatal(err)
	}
	if len(arr) != 1 || arr[0]["type"] != "dangerous_tool_use" {
		t.Fatalf("safeguard_results content = %v", arr)
	}
}

// TestEncodeStreamEvent_MessageDeltaSafeguardResults verifies that
// DeltaExtras are merged back into the encoded message_delta frame.
func TestEncodeStreamEvent_MessageDeltaSafeguardResults(t *testing.T) {
	sr := json.RawMessage(`[{"type":"dangerous_tool_use","status":{"type":"available","tool_uses":{}}}]`)
	evt := &translate.StreamEvent{
		Type:         "message_delta",
		StopReason:   "stop",
		OutputTokens: 5,
		DeltaExtras:  map[string]json.RawMessage{"safeguard_results": sr},
	}
	b, err := EncodeStreamEvent(evt)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `"safeguard_results"`) {
		t.Fatalf("frame missing safeguard_results: %s", s)
	}
	var probe struct {
		Delta map[string]any `json:"delta"`
	}
	// strip the leading "event: message_delta\ndata: " line
	data := string(b)
	data = data[strings.Index(data, "data: ")+6:]
	data = strings.TrimSuffix(data, "\n\n")
	if err := json.Unmarshal([]byte(data), &probe); err != nil {
		t.Fatal(err)
	}
	arr, ok := probe.Delta["safeguard_results"].([]any)
	if !ok || len(arr) != 1 {
		t.Fatalf("safeguard_results shape wrong: %v", probe.Delta)
	}
	if probe.Delta["stop_reason"] != "end_turn" {
		t.Fatalf("stop_reason wrong: %v", probe.Delta["stop_reason"])
	}
}
