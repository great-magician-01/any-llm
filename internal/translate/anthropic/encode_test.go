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

func TestEncodeRequest_MaxTokensDefaultWhenUnset(t *testing.T) {
	req := &translate.Request{
		Model:     "claude-3-5",
		Messages:  []translate.Message{},
		MaxTokens: 0,
	}
	out, err := EncodeRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal(out, &got)
	if got["max_tokens"] != float64(4096) {
		t.Fatalf("max_tokens=%v want 4096", got["max_tokens"])
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

func TestThinkingRoundTrip_Request(t *testing.T) {
	body := []byte(`{
		"model":"claude-3-5",
		"messages":[
			{"role":"user","content":"What is 2+2?"},
			{"role":"assistant","content":[
				{"type":"thinking","thinking":"Let me compute...","signature":"sig_abc"},
				{"type":"text","text":"4"}
			]},
			{"role":"user","content":"And 3+3?"}
		],
		"max_tokens":100,
		"thinking":{"type":"enabled","budget_tokens":1024}
	}`)
	req, err := DecodeRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	asst := req.Messages[1].Content
	if len(asst) != 2 || asst[0].Type != "thinking" || asst[0].Thinking != "Let me compute..." || asst[0].Signature != "sig_abc" {
		t.Fatalf("thinking block not preserved: %+v", asst)
	}
	if asst[1].Type != "text" || asst[1].Text != "4" {
		t.Fatalf("text block not preserved: %+v", asst[1])
	}
	if req.Extra["thinking"] == nil {
		t.Fatal("thinking config not preserved in Extra")
	}
	out, err := EncodeRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	var got rawRequest
	_ = json.Unmarshal(out, &got)
	var parts []map[string]any
	_ = json.Unmarshal(got.Messages[1].Content, &parts)
	if parts[0]["type"] != "thinking" || parts[0]["thinking"] != "Let me compute..." || parts[0]["signature"] != "sig_abc" {
		t.Fatalf("thinking block not encoded: %+v", parts[0])
	}
	if parts[1]["type"] != "text" || parts[1]["text"] != "4" {
		t.Fatalf("text block not encoded: %+v", parts[1])
	}
	var outMap map[string]any
	_ = json.Unmarshal(out, &outMap)
	if outMap["thinking"] == nil {
		t.Fatal("thinking config not forwarded")
	}
}

func TestRedactedThinkingRoundTrip_Request(t *testing.T) {
	body := []byte(`{
		"model":"claude-3-5",
		"messages":[
			{"role":"user","content":"secret"},
			{"role":"assistant","content":[
				{"type":"redacted_thinking","data":"EncYBCkQY..."},
				{"type":"text","text":"ok"}
			]}
		],
		"max_tokens":100
	}`)
	req, err := DecodeRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	asst := req.Messages[1].Content
	if len(asst) != 2 || asst[0].Type != "redacted_thinking" || asst[0].Data != "EncYBCkQY..." {
		t.Fatalf("redacted_thinking block not preserved: %+v", asst)
	}
	out, err := EncodeRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	var got rawRequest
	_ = json.Unmarshal(out, &got)
	var parts []map[string]any
	_ = json.Unmarshal(got.Messages[1].Content, &parts)
	if parts[0]["type"] != "redacted_thinking" || parts[0]["data"] != "EncYBCkQY..." {
		t.Fatalf("redacted_thinking block not encoded: %+v", parts[0])
	}
}

func TestThinkingRoundTrip_Response(t *testing.T) {
	body := []byte(`{
		"id":"msg_1","model":"claude-3-5",
		"content":[
			{"type":"thinking","thinking":"reasoning...","signature":"sig_xyz"},
			{"type":"redacted_thinking","data":"EncABC..."},
			{"type":"text","text":"answer"}
		],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":10,"output_tokens":20}
	}`)
	resp, err := DecodeResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Content) != 3 {
		t.Fatalf("content len=%d", len(resp.Content))
	}
	if resp.Content[0].Type != "thinking" || resp.Content[0].Thinking != "reasoning..." || resp.Content[0].Signature != "sig_xyz" {
		t.Fatalf("thinking not decoded: %+v", resp.Content[0])
	}
	if resp.Content[1].Type != "redacted_thinking" || resp.Content[1].Data != "EncABC..." {
		t.Fatalf("redacted_thinking not decoded: %+v", resp.Content[1])
	}
	out, err := EncodeResponse(resp)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal(out, &got)
	content := got["content"].([]any)
	thinkingBlock := content[0].(map[string]any)
	if thinkingBlock["type"] != "thinking" || thinkingBlock["thinking"] != "reasoning..." || thinkingBlock["signature"] != "sig_xyz" {
		t.Fatalf("thinking not encoded: %+v", thinkingBlock)
	}
	redactedBlock := content[1].(map[string]any)
	if redactedBlock["type"] != "redacted_thinking" || redactedBlock["data"] != "EncABC..." {
		t.Fatalf("redacted_thinking not encoded: %+v", redactedBlock)
	}
}
