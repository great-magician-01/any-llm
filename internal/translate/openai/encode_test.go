package openai

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/great-magician-01/any-llm/internal/translate"
)

func TestEncodeRequest_RoundTripText(t *testing.T) {
	temp := 0.5
	req := &translate.Request{
		Model:       "gpt-4o",
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
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got.Model != "gpt-4o" || got.MaxTokens != 100 {
		t.Fatalf("got=%+v", got)
	}
	// system becomes messages[0]
	if got.Messages[0].Role != "system" {
		t.Fatalf("messages=%+v", got.Messages)
	}
	if decodeString(got.Messages[0].Content) != "You are helpful" {
		t.Fatalf("system content=%s", got.Messages[0].Content)
	}
	if got.Messages[1].Role != "user" || decodeString(got.Messages[1].Content) != "Hello" {
		t.Fatalf("user content=%s", got.Messages[1].Content)
	}
	// stop as array
	var arr []string
	_ = json.Unmarshal(got.Stop, &arr)
	if len(arr) != 1 || arr[0] != "END" {
		t.Fatalf("stop=%s", got.Stop)
	}
}

func TestEncodeRequest_ToolUseAndResult(t *testing.T) {
	req := &translate.Request{
		Model: "gpt-4o",
		Messages: []translate.Message{
			{Role: "assistant", Content: []translate.ContentBlock{
				{Type: "text", Text: "Sure"},
				{Type: "tool_use", ToolUse: &translate.ToolUse{ID: "call_1", Name: "get_weather", Input: json.RawMessage(`{"city":"SF"}`)}},
			}},
			{Role: "user", Content: []translate.ContentBlock{
				{Type: "tool_result", ToolResult: &translate.ToolResult{ToolUseID: "call_1", Content: []translate.ContentBlock{{Type: "text", Text: "sunny"}}}},
			}},
		},
		Tools:      []translate.Tool{{Name: "get_weather", Description: "w", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		ToolChoice: &translate.ToolChoice{Type: "tool", Name: "get_weather"},
	}
	out, err := EncodeRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	var got rawRequest
	_ = json.Unmarshal(out, &got)
	// assistant: content + tool_calls
	asst := got.Messages[0]
	if asst.Role != "assistant" || decodeString(asst.Content) != "Sure" {
		t.Fatalf("asst=%+v", asst)
	}
	if len(asst.ToolCalls) != 1 || asst.ToolCalls[0].ID != "call_1" || asst.ToolCalls[0].Function.Name != "get_weather" {
		t.Fatalf("tool_calls=%+v", asst.ToolCalls)
	}
	// tool_result user message -> role:tool
	tool := got.Messages[1]
	if tool.Role != "tool" || tool.ToolCallID != "call_1" || decodeString(tool.Content) != "sunny" {
		t.Fatalf("tool msg=%+v", tool)
	}
	// tools
	if len(got.Tools) != 1 || got.Tools[0].Function.Name != "get_weather" {
		t.Fatalf("tools=%+v", got.Tools)
	}
	// tool_choice object form
	var tc struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	_ = json.Unmarshal(got.ToolChoice, &tc)
	if tc.Type != "function" || tc.Function.Name != "get_weather" {
		t.Fatalf("tool_choice=%s", got.ToolChoice)
	}
}

func TestEncodeRequest_ExtraMerged(t *testing.T) {
	req := &translate.Request{
		Model:    "gpt-4o",
		Messages: []translate.Message{},
		Extra:    map[string]any{"top_k": 40},
	}
	out, err := EncodeRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal(out, &got)
	if got["top_k"] != float64(40) {
		t.Fatalf("extra not merged: %v", got["top_k"])
	}
}

func TestEncodeResponse_TextAndToolUse(t *testing.T) {
	resp := &translate.Response{
		ID:    "c1",
		Model: "gpt-4o",
		Content: []translate.ContentBlock{
			{Type: "text", Text: "Hi"},
			{Type: "tool_use", ToolUse: &translate.ToolUse{ID: "call_1", Name: "get_weather", Input: json.RawMessage(`{"city":"SF"}`)}},
		},
		StopReason: "tool_calls",
		Usage:      translate.Usage{InputTokens: 10, OutputTokens: 5},
	}
	out, err := EncodeResponse(resp)
	if err != nil {
		t.Fatal(err)
	}
	var got rawResponse
	_ = json.Unmarshal(out, &got)
	if got.ID != "c1" || got.Model != "gpt-4o" {
		t.Fatalf("id/model=%+v", got)
	}
	if got.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("finish=%q", got.Choices[0].FinishReason)
	}
	if got.Choices[0].Message.Content != "Hi" {
		t.Fatalf("content=%s", got.Choices[0].Message.Content)
	}
	if len(got.Choices[0].Message.ToolCalls) != 1 || got.Choices[0].Message.ToolCalls[0].Function.Name != "get_weather" {
		t.Fatalf("tool_calls=%+v", got.Choices[0].Message.ToolCalls)
	}
	if got.Usage == nil || got.Usage.PromptTokens != 10 || got.Usage.CompletionTokens != 5 {
		t.Fatalf("usage=%+v", got.Usage)
	}
}

// TestEncodeResponse_ThinkingToOpenAI verifies Anthropic thinking blocks map
// to reasoning_content for OpenAI-format clients (DeepSeek-style).
func TestEncodeResponse_ThinkingToOpenAI(t *testing.T) {
	out, err := EncodeResponse(&translate.Response{
		ID:    "msg_1",
		Model: "m",
		Content: []translate.ContentBlock{
			{Type: "thinking", Thinking: "Hmm, let me think"},
			{Type: "text", Text: "Hi"},
		},
		StopReason: "stop",
	})
	if err != nil {
		t.Fatal(err)
	}
	var rr rawResponse
	if err := json.Unmarshal(out, &rr); err != nil {
		t.Fatal(err)
	}
	if rr.Choices[0].Message.ReasoningContent != "Hmm, let me think" {
		t.Fatalf("reasoning_content=%q", rr.Choices[0].Message.ReasoningContent)
	}
	if rr.Choices[0].Message.Content != "Hi" {
		t.Fatalf("content=%q", rr.Choices[0].Message.Content)
	}
}

// TestEncodeResponse_FullUsage verifies the OpenAI usage object carries the
// full breakdown (cache hit + reasoning) when present in the IR.
func TestEncodeResponse_FullUsage(t *testing.T) {
	out, err := EncodeResponse(&translate.Response{
		ID: "c1", Model: "m",
		Content:    []translate.ContentBlock{{Type: "text", Text: "Hi"}},
		StopReason: "stop",
		Usage: translate.Usage{
			InputTokens:     437,
			OutputTokens:    82,
			CacheReadTokens: 384,
			ReasoningTokens: 26,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var rr rawResponse
	if err := json.Unmarshal(out, &rr); err != nil {
		t.Fatal(err)
	}
	if rr.Usage == nil {
		t.Fatal("no usage")
	}
	if rr.Usage.PromptTokens != 437 || rr.Usage.CompletionTokens != 82 || rr.Usage.TotalTokens != 519 {
		t.Fatalf("usage=%+v", rr.Usage)
	}
	if rr.Usage.PromptTokensDetails == nil || rr.Usage.PromptTokensDetails.CachedTokens != 384 {
		t.Fatalf("cached_tokens=%+v", rr.Usage.PromptTokensDetails)
	}
	if rr.Usage.CompletionTokensDetails == nil || rr.Usage.CompletionTokensDetails.ReasoningTokens != 26 {
		t.Fatalf("reasoning_tokens=%+v", rr.Usage.CompletionTokensDetails)
	}
	if rr.Usage.PromptCacheHitTokens != 384 || rr.Usage.PromptCacheMissTokens != 53 {
		t.Fatalf("hit/miss=%d/%d", rr.Usage.PromptCacheHitTokens, rr.Usage.PromptCacheMissTokens)
	}
}

// TestEncodeResponse_NoCacheNoDetails verifies the details fields are omitted
// entirely when there is no cache/reasoning data.
func TestEncodeResponse_NoCacheNoDetails(t *testing.T) {
	out, err := EncodeResponse(&translate.Response{
		ID: "c1", Model: "m",
		Content:    []translate.ContentBlock{{Type: "text", Text: "Hi"}},
		StopReason: "stop",
		Usage:      translate.Usage{InputTokens: 10, OutputTokens: 5},
	})
	if err != nil {
		t.Fatal(err)
	}
	var rr rawResponse
	if err := json.Unmarshal(out, &rr); err != nil {
		t.Fatal(err)
	}
	if rr.Usage.PromptTokensDetails != nil || rr.Usage.CompletionTokensDetails != nil {
		t.Fatalf("details should be omitted: %+v", rr.Usage)
	}
	if rr.Usage.PromptCacheHitTokens != 0 {
		t.Fatalf("hit should be 0: %+v", rr.Usage)
	}
}

// TestEncodeRequest_NoParametersForSchemaLessTool verifies a tool without an
// InputSchema is emitted without a parameters key — a nil json.RawMessage
// would otherwise serialize as "parameters": null, which upstreams reject.
func TestEncodeRequest_NoParametersForSchemaLessTool(t *testing.T) {
	req := &translate.Request{
		Model:     "gpt-4o",
		Messages:  []translate.Message{{Role: "user", Content: []translate.ContentBlock{{Type: "text", Text: "hi"}}}},
		Tools:     []translate.Tool{{Name: "web_search", Type: "web_search_20250305"}},
		MaxTokens: 10,
	}
	out, err := EncodeRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), `"parameters"`) {
		t.Fatalf("parameters should be omitted for schema-less tools: %s", out)
	}
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	tools, _ := m["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools=%v", m["tools"])
	}
	fn, _ := tools[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "web_search" {
		t.Fatalf("tool lost: %s", out)
	}
}
