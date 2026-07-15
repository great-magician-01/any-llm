package openai

import (
	"encoding/json"
	"testing"

	"github.com/great-magician-01/any-llm/internal/translate"
)

func TestDecodeRequest_TextOnly(t *testing.T) {
	body := []byte(`{
		"model":"gpt-4o",
		"messages":[
			{"role":"system","content":"You are helpful"},
			{"role":"user","content":"Hello"}
		],
		"max_tokens":100,
		"temperature":0.5
	}`)
	req, err := DecodeRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if req.Model != "gpt-4o" {
		t.Fatalf("model=%q", req.Model)
	}
	if len(req.System) != 1 || req.System[0].Text != "You are helpful" {
		t.Fatalf("system=%+v", req.System)
	}
	if len(req.Messages) != 1 || req.Messages[0].Role != "user" {
		t.Fatalf("messages=%+v", req.Messages)
	}
	if req.Messages[0].Content[0].Type != "text" || req.Messages[0].Content[0].Text != "Hello" {
		t.Fatalf("content=%+v", req.Messages[0].Content)
	}
	if req.MaxTokens != 100 {
		t.Fatalf("max_tokens=%d", req.MaxTokens)
	}
	if req.Temperature == nil || *req.Temperature != 0.5 {
		t.Fatalf("temperature=%v", req.Temperature)
	}
}

func TestDecodeRequest_ImageAndToolCallAndToolResult(t *testing.T) {
	body := []byte(`{
		"model":"gpt-4o",
		"messages":[
			{"role":"user","content":[
				{"type":"text","text":"What is this?"},
				{"type":"image_url","image_url":{"url":"https://x/a.png"}}
			]},
			{"role":"assistant","content":"Sure","tool_calls":[
				{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"SF\"}"}}
			]},
			{"role":"tool","tool_call_id":"call_1","content":"sunny"}
		],
		"tools":[{"type":"function","function":{"name":"get_weather","description":"w","parameters":{"type":"object"}}}],
		"tool_choice":"auto",
		"stop":["END"]
	}`)
	req, err := DecodeRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	// user with text + image
	u := req.Messages[0]
	if u.Content[0].Type != "text" || u.Content[0].Text != "What is this?" {
		t.Fatalf("user[0]=%+v", u.Content[0])
	}
	if u.Content[1].Type != "image" || u.Content[1].Image.URL != "https://x/a.png" {
		t.Fatalf("user[1]=%+v", u.Content[1])
	}
	// assistant with text + tool_use
	a := req.Messages[1]
	if a.Role != "assistant" || a.Content[0].Type != "text" || a.Content[0].Text != "Sure" {
		t.Fatalf("asst text=%+v", a.Content[0])
	}
	if a.Content[1].Type != "tool_use" || a.Content[1].ToolUse.ID != "call_1" || a.Content[1].ToolUse.Name != "get_weather" {
		t.Fatalf("asst tool_use=%+v", a.Content[1])
	}
	var inp map[string]string
	_ = json.Unmarshal(a.Content[1].ToolUse.Input, &inp)
	if inp["city"] != "SF" {
		t.Fatalf("tool input=%s", a.Content[1].ToolUse.Input)
	}
	// tool result mapped to user message with tool_result block
	tr := req.Messages[2]
	if tr.Role != "user" || tr.Content[0].Type != "tool_result" {
		t.Fatalf("tool role=%+v", tr)
	}
	if tr.Content[0].ToolResult.ToolUseID != "call_1" || tr.Content[0].ToolResult.Content[0].Text != "sunny" {
		t.Fatalf("tool_result=%+v", tr.Content[0].ToolResult)
	}
	// tools
	if len(req.Tools) != 1 || req.Tools[0].Name != "get_weather" {
		t.Fatalf("tools=%+v", req.Tools)
	}
	// tool_choice
	if req.ToolChoice == nil || req.ToolChoice.Type != "auto" {
		t.Fatalf("tool_choice=%+v", req.ToolChoice)
	}
	// stop
	if len(req.Stop) != 1 || req.Stop[0] != "END" {
		t.Fatalf("stop=%+v", req.Stop)
	}
}

func TestDecodeRequest_ExtraFields(t *testing.T) {
	body := []byte(`{"model":"gpt-4o","messages":[],"top_k":40,"logprobs":true}`)
	req, err := DecodeRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if req.Extra["top_k"] != float64(40) {
		t.Fatalf("extra top_k=%v", req.Extra["top_k"])
	}
	if req.Extra["logprobs"] != true {
		t.Fatalf("extra logprobs=%v", req.Extra["logprobs"])
	}
}

func _useTranslate() { _ = translate.Request{} }

func TestDecodeResponse_TextAndUsage(t *testing.T) {
	body := []byte(`{
		"id":"chatcmpl-1","model":"gpt-4o",
		"choices":[{"index":0,"message":{"role":"assistant","content":"Hi"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}
	}`)
	resp, err := DecodeResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.ID != "chatcmpl-1" || resp.Model != "gpt-4o" {
		t.Fatalf("id/model=%+v", resp)
	}
	if len(resp.Content) != 1 || resp.Content[0].Type != "text" || resp.Content[0].Text != "Hi" {
		t.Fatalf("content=%+v", resp.Content)
	}
	if resp.StopReason != "stop" {
		t.Fatalf("stop_reason=%q", resp.StopReason)
	}
	if resp.Usage.InputTokens != 10 || resp.Usage.OutputTokens != 5 {
		t.Fatalf("usage=%+v", resp.Usage)
	}
}

func TestDecodeResponse_ToolCalls(t *testing.T) {
	body := []byte(`{
		"id":"chatcmpl-2","model":"gpt-4o",
		"choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[
			{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"SF\"}"}}
		]},"finish_reason":"tool_calls"}],
		"usage":{"prompt_tokens":8,"completion_tokens":12,"total_tokens":20}
	}`)
	resp, err := DecodeResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != "tool_calls" {
		t.Fatalf("stop_reason=%q", resp.StopReason)
	}
	if len(resp.Content) != 1 || resp.Content[0].Type != "tool_use" || resp.Content[0].ToolUse.Name != "get_weather" {
		t.Fatalf("content=%+v", resp.Content)
	}
	if resp.Usage.OutputTokens != 12 {
		t.Fatalf("usage=%+v", resp.Usage)
	}
}
