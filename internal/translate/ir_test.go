package translate

import (
	"encoding/json"
	"testing"
)

func TestContentBlockToolUseRoundTrip(t *testing.T) {
	b := ContentBlock{
		Type: "tool_use",
		ToolUse: &ToolUse{
			ID:    "call_1",
			Name:  "get_weather",
			Input: json.RawMessage(`{"city":"SF"}`),
		},
	}
	raw, err := json.Marshal(b.ToolUse.Input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal input: %v", err)
	}
	if got["city"] != "SF" {
		t.Fatalf("city = %q, want SF", got["city"])
	}
}

func TestRequestZeroValue(t *testing.T) {
	var r Request
	if r.Stream != false || r.Model != "" || r.Extra != nil {
		t.Fatalf("zero value not clean: %+v", r)
	}
}
