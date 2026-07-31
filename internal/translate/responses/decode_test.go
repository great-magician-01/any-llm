package responses

import (
	"testing"
)

func TestDecodeRequest_Basic(t *testing.T) {
	body := []byte(`{
		"model": "gpt-4o",
		"instructions": "be good",
		"input": [
			{"role": "user", "content": [{"type": "input_text", "text": "hi"}]},
			{"role": "assistant", "content": [{"type": "input_text", "text": "hello"}]},
			{"type": "function_call", "call_id": "call_1", "name": "get_weather", "arguments": "{\"city\":\"SF\"}"},
			{"type": "function_call_output", "call_id": "call_1", "output": "sunny"}
		],
		"tools": [{"type": "function", "name": "get_weather", "description": "w", "parameters": {"type": "object"}}],
		"tool_choice": "auto",
		"max_output_tokens": 50
	}`)
	req, err := DecodeRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if req.Model != "gpt-4o" || req.MaxTokens != 50 {
		t.Fatalf("model/max_tokens: %+v", req)
	}
	if len(req.System) != 1 || req.System[0].Text != "be good" {
		t.Fatalf("system=%+v", req.System)
	}
	// 注意：assistant 文本历史消息必须保留（忠实转换，不丢对话历史）
	if len(req.Messages) != 4 {
		t.Fatalf("messages len=%d", len(req.Messages))
	}
	u := req.Messages[0]
	if u.Role != "user" || len(u.Content) != 1 || u.Content[0].Type != "text" || u.Content[0].Text != "hi" {
		t.Fatalf("msg0=%+v", u)
	}
	at := req.Messages[1]
	if at.Role != "assistant" || len(at.Content) != 1 || at.Content[0].Type != "text" || at.Content[0].Text != "hello" {
		t.Fatalf("msg1(assistant text)=%+v", at)
	}
	a := req.Messages[2]
	if a.Role != "assistant" || len(a.Content) != 1 || a.Content[0].Type != "tool_use" {
		t.Fatalf("msg2=%+v", a)
	}
	if a.Content[0].ToolUse.ID != "call_1" || a.Content[0].ToolUse.Name != "get_weather" ||
		string(a.Content[0].ToolUse.Input) != `{"city":"SF"}` {
		t.Fatalf("tool_use=%+v", a.Content[0].ToolUse)
	}
	tr := req.Messages[3]
	if tr.Role != "user" || tr.Content[0].Type != "tool_result" || tr.Content[0].ToolResult.ToolUseID != "call_1" ||
		tr.Content[0].ToolResult.Content[0].Text != "sunny" {
		t.Fatalf("msg3=%+v", tr)
	}
	if len(req.Tools) != 1 || req.Tools[0].Name != "get_weather" {
		t.Fatalf("tools=%+v", req.Tools)
	}
	if req.ToolChoice == nil || req.ToolChoice.Type != "auto" {
		t.Fatalf("tool_choice=%+v", req.ToolChoice)
	}
}

func TestDecodeRequest_StatefulFields(t *testing.T) {
	body := []byte(`{"model":"m","input":[{"role":"user","content":[{"type":"input_text","text":"hi"}]}],
		"previous_response_id":"resp_abc","store":true,"stream":true}`)
	req, err := DecodeRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if !req.Stream {
		t.Fatal("stream not decoded")
	}
	if req.Extra["previous_response_id"] != "resp_abc" {
		t.Fatalf("extra=%+v", req.Extra)
	}
	if req.Extra["store"] != true {
		t.Fatalf("extra store=%v", req.Extra["store"])
	}
}

func TestDecodeRequest_InstructionsArray(t *testing.T) {
	body := []byte(`{"model":"m","instructions":[{"type":"input_text","text":"a"},{"type":"input_text","text":"b"}],
		"input":[]}`)
	req, err := DecodeRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.System) != 2 || req.System[0].Text != "a" || req.System[1].Text != "b" {
		t.Fatalf("system=%+v", req.System)
	}
}

func TestDecodeRequest_SystemRoleMessage(t *testing.T) {
	body := []byte(`{"model":"m","input":[{"role":"system","content":[{"type":"input_text","text":"sys"}]},
		{"role":"user","content":[{"type":"input_text","text":"hi"}]}]}`)
	req, err := DecodeRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.System) != 1 || req.System[0].Text != "sys" {
		t.Fatalf("system=%+v", req.System)
	}
}

func TestDecodeRequest_Image(t *testing.T) {
	body := []byte(`{"model":"m","input":[{"role":"user","content":[
		{"type":"input_text","text":"what is this"},
		{"type":"input_image","image_url":"https://x/a.png"}]}]}`)
	req, err := DecodeRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	b := req.Messages[0].Content
	if len(b) != 2 || b[0].Type != "text" || b[1].Type != "image" || b[1].Image.URL != "https://x/a.png" {
		t.Fatalf("blocks=%+v", b)
	}
}

func TestDecodeRequest_UnknownPartType(t *testing.T) {
	body := []byte(`{"model":"m","input":[{"role":"user","content":[{"type":"input_file","file_id":"f1"}]}]}`)
	if _, err := DecodeRequest(body); err == nil {
		t.Fatal("expected error for input_file")
	}
}

func TestDecodeRequest_ToolChoiceRequired(t *testing.T) {
	body := []byte(`{"model":"m","input":[],"tool_choice":"required"}`)
	req, err := DecodeRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if req.ToolChoice == nil || req.ToolChoice.Type != "required" {
		t.Fatalf("tool_choice=%+v", req.ToolChoice)
	}
}

func TestDecodeResponse_Full(t *testing.T) {
	body := []byte(`{
		"id": "resp_1", "object": "response", "created_at": 123, "status": "completed", "model": "gpt-4o",
		"output": [
			{"type": "message", "id": "msg_1", "status": "completed", "role": "assistant",
			 "content": [{"type": "output_text", "text": "Hi", "annotations": []}]},
			{"type": "function_call", "id": "fc_1", "call_id": "call_1", "name": "get_weather", "arguments": "{\"city\":\"SF\"}"},
			{"type": "reasoning", "id": "rs_1", "summary": [{"type": "summary_text", "text": "think it through"}], "content": []}
		],
		"usage": {"input_tokens": 437, "output_tokens": 82, "total_tokens": 519,
			"input_tokens_details": {"cached_tokens": 384},
			"output_tokens_details": {"reasoning_tokens": 26}}
	}`)
	resp, err := DecodeResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.ID != "resp_1" || resp.Model != "gpt-4o" || resp.StopReason != "tool_calls" {
		t.Fatalf("resp=%+v", resp)
	}
	if len(resp.Content) != 3 {
		t.Fatalf("content len=%d", len(resp.Content))
	}
	if resp.Content[0].Type != "text" || resp.Content[0].Text != "Hi" {
		t.Fatalf("content0=%+v", resp.Content[0])
	}
	tu := resp.Content[1]
	if tu.Type != "tool_use" || tu.ToolUse.ID != "call_1" || tu.ToolUse.Name != "get_weather" ||
		string(tu.ToolUse.Input) != `{"city":"SF"}` {
		t.Fatalf("content1=%+v", tu)
	}
	th := resp.Content[2]
	if th.Type != "thinking" || th.Thinking != "think it through" || th.Signature != "rs_1" {
		t.Fatalf("content2=%+v", th)
	}
	u := resp.Usage
	if u.InputTokens != 437 || u.OutputTokens != 82 || u.CacheReadTokens != 384 || u.ReasoningTokens != 26 {
		t.Fatalf("usage=%+v", u)
	}
}

func TestDecodeResponse_StatusIncomplete(t *testing.T) {
	body := []byte(`{"id":"r","status":"incomplete","model":"m",
		"incomplete_details":{"reason":"max_output_tokens"},
		"output":[{"type":"message","id":"m1","role":"assistant","content":[{"type":"output_text","text":"part"}]}]}`)
	resp, err := DecodeResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != "max_tokens" {
		t.Fatalf("stop=%q", resp.StopReason)
	}
	if len(resp.Content) != 1 || resp.Content[0].Text != "part" {
		t.Fatalf("content=%+v", resp.Content)
	}
}

func TestDecodeResponse_EmptyOutput(t *testing.T) {
	body := []byte(`{"id":"r","status":"completed","model":"m","output":[]}`)
	resp, err := DecodeResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != "stop" || len(resp.Content) != 0 {
		t.Fatalf("resp=%+v", resp)
	}
}
