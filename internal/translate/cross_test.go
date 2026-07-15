package translate_test

import (
	"encoding/json"
	"testing"

	"github.com/great-magician-01/any-llm/internal/translate"
	"github.com/great-magician-01/any-llm/internal/translate/anthropic"
	"github.com/great-magician-01/any-llm/internal/translate/openai"
)

// OpenAI request -> IR -> Anthropic request -> IR -> should match first IR semantically
func TestCrossRequest_OpenAIToAnthropic(t *testing.T) {
	src := []byte(`{
		"model":"gpt-4o",
		"messages":[
			{"role":"system","content":"be good"},
			{"role":"user","content":[
				{"type":"text","text":"hi"},
				{"type":"image_url","image_url":{"url":"https://x/a.png"}}
			]},
			{"role":"assistant","tool_calls":[
				{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"SF\"}"}}
			]},
			{"role":"tool","tool_call_id":"call_1","content":"sunny"}
		],
		"tools":[{"type":"function","function":{"name":"get_weather","description":"w","parameters":{"type":"object"}}}],
		"tool_choice":"auto","max_tokens":50
	}`)
	ir1, err := openai.DecodeRequest(src)
	if err != nil {
		t.Fatal(err)
	}
	antBytes, err := anthropic.EncodeRequest(ir1)
	if err != nil {
		t.Fatal(err)
	}
	ir2, err := anthropic.DecodeRequest(antBytes)
	if err != nil {
		t.Fatal(err)
	}
	assertRequestsMatch(t, ir1, ir2)
}

func TestCrossRequest_AnthropicToOpenAI(t *testing.T) {
	src := []byte(`{
		"model":"claude-3-5","system":"be good",
		"messages":[
			{"role":"user","content":[
				{"type":"text","text":"hi"},
				{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAA"}}
			]},
			{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{"city":"SF"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"sunny"}]}
		],
		"tools":[{"name":"get_weather","description":"w","input_schema":{"type":"object"}}],
		"tool_choice":{"type":"auto"},"max_tokens":50
	}`)
	ir1, err := anthropic.DecodeRequest(src)
	if err != nil {
		t.Fatal(err)
	}
	oaiBytes, err := openai.EncodeRequest(ir1)
	if err != nil {
		t.Fatal(err)
	}
	ir2, err := openai.DecodeRequest(oaiBytes)
	if err != nil {
		t.Fatal(err)
	}
	assertRequestsMatch(t, ir1, ir2)
}

func TestCrossResponse_OpenAIToAnthropic(t *testing.T) {
	src := []byte(`{
		"id":"c1","model":"gpt-4o",
		"choices":[{"index":0,"message":{"role":"assistant","content":"Hi"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}
	}`)
	ir1, err := openai.DecodeResponse(src)
	if err != nil {
		t.Fatal(err)
	}
	antBytes, err := anthropic.EncodeResponse(ir1)
	if err != nil {
		t.Fatal(err)
	}
	ir2, err := anthropic.DecodeResponse(antBytes)
	if err != nil {
		t.Fatal(err)
	}
	if ir2.Content[0].Text != "Hi" {
		t.Fatalf("text=%q", ir2.Content[0].Text)
	}
	if ir2.Usage.InputTokens != 10 || ir2.Usage.OutputTokens != 5 {
		t.Fatalf("usage=%+v", ir2.Usage)
	}
}

func assertRequestsMatch(t *testing.T, a, b *translate.Request) {
	t.Helper()
	if a.Model != b.Model {
		t.Errorf("model %q != %q", a.Model, b.Model)
	}
	if len(a.System) != len(b.System) {
		t.Fatalf("system len %d != %d", len(a.System), len(b.System))
	}
	if len(a.Messages) != len(b.Messages) {
		t.Fatalf("messages len %d != %d", len(a.Messages), len(b.Messages))
	}
	for i := range a.Messages {
		if a.Messages[i].Role != b.Messages[i].Role {
			t.Errorf("msg[%d] role %q != %q", i, a.Messages[i].Role, b.Messages[i].Role)
		}
		if len(a.Messages[i].Content) != len(b.Messages[i].Content) {
			t.Fatalf("msg[%d] content len %d != %d", i, len(a.Messages[i].Content), len(b.Messages[i].Content))
		}
		for j := range a.Messages[i].Content {
			ca, cb := a.Messages[i].Content[j], b.Messages[i].Content[j]
			if ca.Type != cb.Type {
				t.Errorf("msg[%d].content[%d] type %q != %q", i, j, ca.Type, cb.Type)
			}
		}
	}
	if len(a.Tools) != len(b.Tools) {
		t.Errorf("tools len %d != %d", len(a.Tools), len(b.Tools))
	}
	if (a.ToolChoice == nil) != (b.ToolChoice == nil) {
		t.Errorf("tool_choice presence mismatch")
	}
	// Note: image URL vs base64 is not preserved across formats by design (different sources),
	// so we only assert block types match, not image payload. Assert text payload where present.
	for i := range a.Messages {
		for j := range a.Messages[i].Content {
			ca, cb := a.Messages[i].Content[j], b.Messages[i].Content[j]
			if ca.Type == "text" && ca.Text != cb.Text {
				t.Errorf("msg[%d].content[%d] text %q != %q", i, j, ca.Text, cb.Text)
			}
			if ca.Type == "tool_use" && ca.ToolUse.Name != cb.ToolUse.Name {
				t.Errorf("tool_use name %q != %q", ca.ToolUse.Name, cb.ToolUse.Name)
			}
		}
	}
	_ = json.RawMessage(nil)
}
