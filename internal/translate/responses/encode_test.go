package responses

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/great-magician-01/any-llm/internal/translate"
)

func TestEncodeRequest_Full(t *testing.T) {
	req := &translate.Request{
		Model:     "gpt-4o",
		System:    []translate.TextBlock{{Text: "be good"}},
		MaxTokens: 50,
		Messages: []translate.Message{
			{Role: "user", Content: []translate.ContentBlock{
				{Type: "text", Text: "hi"},
				{Type: "image", Image: &translate.Image{URL: "https://x/a.png"}},
			}},
			{Role: "assistant", Content: []translate.ContentBlock{
				{Type: "text", Text: "hello"},
				{Type: "tool_use", ToolUse: &translate.ToolUse{ID: "call_1", Name: "get_weather", Input: json.RawMessage(`{"city":"SF"}`)}},
			}},
			{Role: "user", Content: []translate.ContentBlock{
				{Type: "tool_result", ToolResult: &translate.ToolResult{ToolUseID: "call_1", Content: []translate.ContentBlock{{Type: "text", Text: "sunny"}}}},
			}},
		},
		Tools:      []translate.Tool{{Name: "get_weather", Description: "w", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		ToolChoice: &translate.ToolChoice{Type: "auto"},
		Extra: map[string]any{
			"previous_response_id": "resp_old", // 必须被跳过
			"store":                true,       // 必须被跳过
			"metadata":             "x",        // 透传
		},
	}
	body, err := EncodeRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	if m["instructions"] != "be good" {
		t.Fatalf("instructions=%v", m["instructions"])
	}
	if m["max_output_tokens"] != float64(50) {
		t.Fatalf("max_output_tokens=%v", m["max_output_tokens"])
	}
	if _, ok := m["previous_response_id"]; ok {
		t.Fatalf("previous_response_id must not be forwarded: %v", m)
	}
	if _, ok := m["store"]; ok {
		t.Fatalf("store must not be forwarded: %v", m)
	}
	if m["metadata"] != "x" {
		t.Fatalf("metadata passthrough failed: %v", m)
	}
	in, _ := m["input"].([]any)
	if len(in) != 4 {
		t.Fatalf("input len=%d: %v", len(in), in)
	}
	// 0: user 消息（text+image 两个 part）
	u := in[0].(map[string]any)
	if u["role"] != "user" {
		t.Fatalf("in[0]=%v", u)
	}
	parts, _ := u["content"].([]any)
	if len(parts) != 2 {
		t.Fatalf("user parts len=%d", len(parts))
	}
	p0 := parts[0].(map[string]any)
	if p0["type"] != "input_text" || p0["text"] != "hi" {
		t.Fatalf("part0=%v", p0)
	}
	p1 := parts[1].(map[string]any)
	if p1["type"] != "input_image" || p1["image_url"] != "https://x/a.png" {
		t.Fatalf("part1=%v", p1)
	}
	// 1: assistant 消息（text）
	a := in[1].(map[string]any)
	if a["role"] != "assistant" {
		t.Fatalf("in[1]=%v", a)
	}
	// 2: function_call 顶层 item
	fc := in[2].(map[string]any)
	if fc["type"] != "function_call" || fc["call_id"] != "call_1" || fc["name"] != "get_weather" ||
		fc["arguments"] != `{"city":"SF"}` {
		t.Fatalf("in[2]=%v", fc)
	}
	// 3: function_call_output 顶层 item
	fo := in[3].(map[string]any)
	if fo["type"] != "function_call_output" || fo["call_id"] != "call_1" || fo["output"] != "sunny" {
		t.Fatalf("in[3]=%v", fo)
	}
	tools, _ := m["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools=%v", m["tools"])
	}
	to := tools[0].(map[string]any)
	if to["type"] != "function" || to["name"] != "get_weather" {
		t.Fatalf("tool=%v", to)
	}
}

func TestEncodeRequest_ToolChoiceFunction(t *testing.T) {
	req := &translate.Request{Model: "m", ToolChoice: &translate.ToolChoice{Type: "tool", Name: "get_weather"}}
	body, _ := EncodeRequest(req)
	if !strings.Contains(string(body), `"name":"get_weather"`) {
		t.Fatalf("tool_choice missing name: %s", body)
	}
}

func TestEncodeRequest_ThinkingDropped(t *testing.T) {
	req := &translate.Request{Model: "m", Messages: []translate.Message{
		{Role: "assistant", Content: []translate.ContentBlock{
			{Type: "thinking", Thinking: "hmm"},
			{Type: "text", Text: "hi"},
		}},
	}}
	body, err := EncodeRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "hmm") {
		t.Fatalf("thinking must be dropped: %s", body)
	}
}
