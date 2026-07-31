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
	if len(req.Messages) != 3 {
		t.Fatalf("messages len=%d", len(req.Messages))
	}
	u := req.Messages[0]
	if u.Role != "user" || len(u.Content) != 1 || u.Content[0].Type != "text" || u.Content[0].Text != "hi" {
		t.Fatalf("msg0=%+v", u)
	}
	a := req.Messages[1]
	if a.Role != "assistant" || len(a.Content) != 1 || a.Content[0].Type != "tool_use" {
		t.Fatalf("msg1=%+v", a)
	}
	if a.Content[0].ToolUse.ID != "call_1" || a.Content[0].ToolUse.Name != "get_weather" ||
		string(a.Content[0].ToolUse.Input) != `{"city":"SF"}` {
		t.Fatalf("tool_use=%+v", a.Content[0].ToolUse)
	}
	tr := req.Messages[2]
	if tr.Role != "user" || tr.Content[0].Type != "tool_result" || tr.Content[0].ToolResult.ToolUseID != "call_1" ||
		tr.Content[0].ToolResult.Content[0].Text != "sunny" {
		t.Fatalf("msg2=%+v", tr)
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
