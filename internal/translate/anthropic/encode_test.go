package anthropic

import (
	"encoding/json"
	"testing"

	"github.com/great-magician-01/any-llm/internal/translate"
)

func TestEncodeRequest_RoundTrip(t *testing.T) {
	temp := 0.5
	req := &translate.Request{
		Model:       "claude-3-5",
		System:      []translate.TextBlock{{Text: "You are helpful"}},
		Messages:    []translate.Message{{Role: "user", Content: []translate.ContentBlock{{Type: "text", Text: "Hello"}}}},
		MaxTokens:   100,
		Temperature: &temp,
		Stop:        []string{"END"},
	}
	out, err := EncodeRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	var got rawRequest
	_ = json.Unmarshal(out, &got)
	if got.Model != "claude-3-5" || got.MaxTokens != 100 {
		t.Fatalf("got=%+v", got)
	}
	// system as string
	var sys string
	_ = json.Unmarshal(got.System, &sys)
	if sys != "You are helpful" {
		t.Fatalf("system=%s", got.System)
	}
	// user content as array of text
	var parts []map[string]any
	_ = json.Unmarshal(got.Messages[0].Content, &parts)
	if parts[0]["type"] != "text" || parts[0]["text"] != "Hello" {
		t.Fatalf("content=%+v", parts)
	}
	// stop_sequences
	if len(got.StopSequences) != 1 || got.StopSequences[0] != "END" {
		t.Fatalf("stop=%v", got.StopSequences)
	}
}

func TestEncodeRequest_ImageAndToolUse(t *testing.T) {
	req := &translate.Request{
		Model: "claude-3-5",
		Messages: []translate.Message{
			{Role: "user", Content: []translate.ContentBlock{
				{Type: "image", Image: &translate.Image{Base64: "AAA", MediaType: "image/png"}},
			}},
			{Role: "assistant", Content: []translate.ContentBlock{
				{Type: "tool_use", ToolUse: &translate.ToolUse{ID: "toolu_1", Name: "get_weather", Input: json.RawMessage(`{"city":"SF"}`)}},
			}},
			{Role: "user", Content: []translate.ContentBlock{
				{Type: "tool_result", ToolResult: &translate.ToolResult{ToolUseID: "toolu_1", Content: []translate.ContentBlock{{Type: "text", Text: "sunny"}}}},
			}},
		},
		Tools:      []translate.Tool{{Name: "get_weather", Description: "w", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		ToolChoice: &translate.ToolChoice{Type: "auto"},
		MaxTokens:  10,
	}
	out, err := EncodeRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	var got rawRequest
	_ = json.Unmarshal(out, &got)
	// image
	var imgParts []map[string]any
	_ = json.Unmarshal(got.Messages[0].Content, &imgParts)
	src := imgParts[0]["source"].(map[string]any)
	if src["data"] != "AAA" || src["media_type"] != "image/png" {
		t.Fatalf("image=%+v", imgParts[0])
	}
	// tool_use
	var asstParts []map[string]any
	_ = json.Unmarshal(got.Messages[1].Content, &asstParts)
	if asstParts[0]["type"] != "tool_use" || asstParts[0]["id"] != "toolu_1" {
		t.Fatalf("tool_use=%+v", asstParts[0])
	}
	// tool_result
	var trParts []map[string]any
	_ = json.Unmarshal(got.Messages[2].Content, &trParts)
	if trParts[0]["type"] != "tool_result" || trParts[0]["tool_use_id"] != "toolu_1" {
		t.Fatalf("tool_result=%+v", trParts[0])
	}
	// tool_choice auto
	var tc map[string]any
	_ = json.Unmarshal(got.ToolChoice, &tc)
	if tc["type"] != "auto" {
		t.Fatalf("tool_choice=%+v", tc)
	}
}

func TestEncodeRequest_ExtraMerged(t *testing.T) {
	req := &translate.Request{
		Model: "c", Messages: []translate.Message{}, MaxTokens: 10,
		Extra: map[string]any{"top_k": 40},
	}
	out, err := EncodeRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal(out, &got)
	if got["top_k"] != float64(40) {
		t.Fatalf("extra=%v", got["top_k"])
	}
}

func TestEncodeResponse_TextAndToolUse(t *testing.T) {
	resp := &translate.Response{
		ID:    "msg_1",
		Model: "claude-3-5",
		Content: []translate.ContentBlock{
			{Type: "text", Text: "Hi"},
			{Type: "tool_use", ToolUse: &translate.ToolUse{ID: "toolu_1", Name: "get_weather", Input: json.RawMessage(`{"city":"SF"}`)}},
		},
		StopReason: "stop",
		Usage:      translate.Usage{InputTokens: 10, OutputTokens: 5},
	}
	out, err := EncodeResponse(resp)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal(out, &got)
	if got["id"] != "msg_1" || got["model"] != "claude-3-5" {
		t.Fatalf("id/model=%v %v", got["id"], got["model"])
	}
	if got["stop_reason"] != "end_turn" {
		t.Fatalf("stop_reason=%v", got["stop_reason"])
	}
	content := got["content"].([]any)
	first := content[0].(map[string]any)
	if first["type"] != "text" || first["text"] != "Hi" {
		t.Fatalf("text=%+v", first)
	}
	second := content[1].(map[string]any)
	if second["type"] != "tool_use" || second["name"] != "get_weather" {
		t.Fatalf("tool_use=%+v", second)
	}
	usage := got["usage"].(map[string]any)
	if usage["input_tokens"] != float64(10) || usage["output_tokens"] != float64(5) {
		t.Fatalf("usage=%+v", usage)
	}
}
