package translate

import (
	"encoding/json"
	"testing"
)

// TestResponseStreamEvents_SafeguardResults verifies that a non-stream
// response carrying `safeguard_results` in Extra produces a synthesized
// message_delta with the field preserved in DeltaExtras.
func TestResponseStreamEvents_SafeguardResults(t *testing.T) {
	sr := json.RawMessage(`[{"type":"dangerous_tool_use","status":{"type":"available","tool_uses":{}}}]`)
	resp := &Response{
		ID:    "msg_1",
		Model: "m",
		Content: []ContentBlock{
			{Type: "text", Text: "ok"},
		},
		StopReason: "stop",
		Usage:      Usage{InputTokens: 1, OutputTokens: 1},
		Extra:      map[string]any{"safeguard_results": sr},
	}
	evs := ResponseStreamEvents(resp)
	var delta *StreamEvent
	for _, ev := range evs {
		if ev.Type == "message_delta" {
			delta = ev
			break
		}
	}
	if delta == nil {
		t.Fatal("no message_delta event produced")
	}
	raw, ok := delta.DeltaExtras["safeguard_results"]
	if !ok {
		t.Fatal("safeguard_results missing from message_delta DeltaExtras")
	}
	var arr []map[string]any
	if err := json.Unmarshal(raw, &arr); err != nil {
		t.Fatal(err)
	}
	if len(arr) != 1 || arr[0]["type"] != "dangerous_tool_use" {
		t.Fatalf("safeguard_results content = %v", arr)
	}
}
