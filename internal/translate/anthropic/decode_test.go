package anthropic

import (
	"encoding/json"
	"testing"

	"github.com/great-magician-01/any-llm/internal/translate"
)

func TestDecodeRequest_TextSystem(t *testing.T) {
	body := []byte(`{
		"model":"claude-3-5",
		"system":"You are helpful",
		"messages":[{"role":"user","content":"Hello"}],
		"max_tokens":100,
		"temperature":0.5,
		"stop_sequences":["END"]
	}`)
	req, err := DecodeRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if req.Model != "claude-3-5" {
		t.Fatalf("model=%q", req.Model)
	}
	if len(req.System) != 1 || req.System[0].Text != "You are helpful" {
		t.Fatalf("system=%+v", req.System)
	}
	if len(req.Messages) != 1 || req.Messages[0].Content[0].Type != "text" || req.Messages[0].Content[0].Text != "Hello" {
		t.Fatalf("messages=%+v", req.Messages)
	}
	if req.MaxTokens != 100 || req.Stop[0] != "END" {
		t.Fatalf("max/stop=%d %v", req.MaxTokens, req.Stop)
	}
}

func TestDecodeRequest_ImageToolUseToolResult(t *testing.T) {
	body := []byte(`{
		"model":"claude-3-5",
		"messages":[
			{"role":"user","content":[
				{"type":"text","text":"look"},
				{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAA"}}
			]},
			{"role":"assistant","content":[
				{"type":"text","text":"ok"},
				{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{"city":"SF"}}
			]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"toolu_1","content":"sunny"}
			]}
		],
		"tools":[{"name":"get_weather","description":"w","input_schema":{"type":"object"}}],
		"tool_choice":{"type":"tool","name":"get_weather"}
	}`)
	req, err := DecodeRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	u := req.Messages[0].Content
	if u[0].Type != "text" || u[1].Type != "image" || u[1].Image.Base64 != "AAA" || u[1].Image.MediaType != "image/png" {
		t.Fatalf("user=%+v %+v", u[0], u[1])
	}
	a := req.Messages[1].Content
	if a[1].Type != "tool_use" || a[1].ToolUse.ID != "toolu_1" || a[1].ToolUse.Name != "get_weather" {
		t.Fatalf("asst tool_use=%+v", a[1])
	}
	var inp map[string]string
	_ = json.Unmarshal(a[1].ToolUse.Input, &inp)
	if inp["city"] != "SF" {
		t.Fatalf("input=%s", a[1].ToolUse.Input)
	}
	tr := req.Messages[2].Content[0]
	if tr.Type != "tool_result" || tr.ToolResult.ToolUseID != "toolu_1" || tr.ToolResult.Content[0].Text != "sunny" {
		t.Fatalf("tool_result=%+v", tr.ToolResult)
	}
	if req.ToolChoice == nil || req.ToolChoice.Type != "tool" || req.ToolChoice.Name != "get_weather" {
		t.Fatalf("tool_choice=%+v", req.ToolChoice)
	}
}

func TestDecodeRequest_ExtraFields(t *testing.T) {
	body := []byte(`{"model":"c","messages":[],"max_tokens":10,"top_k":40}`)
	req, err := DecodeRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if req.Extra["top_k"] != float64(40) {
		t.Fatalf("extra=%v", req.Extra["top_k"])
	}
}

func _useT() { _ = translate.Request{} }
