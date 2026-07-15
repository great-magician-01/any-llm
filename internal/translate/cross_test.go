package translate_test

import (
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

func TestCrossResponse_AnthropicToOpenAI(t *testing.T) {
	src := []byte(`{
		"id":"msg_1","model":"claude-3-5",
		"content":[
			{"type":"text","text":"Hi"},
			{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{"city":"SF"}}
		],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":10,"output_tokens":8}
	}`)
	ir1, err := anthropic.DecodeResponse(src)
	if err != nil {
		t.Fatal(err)
	}
	if ir1.StopReason != "stop" {
		t.Fatalf("ir1 stop=%q want stop (end_turn normalized)", ir1.StopReason)
	}
	oaiBytes, err := openai.EncodeResponse(ir1)
	if err != nil {
		t.Fatal(err)
	}
	ir2, err := openai.DecodeResponse(oaiBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(ir2.Content) != 2 {
		t.Fatalf("content len=%d", len(ir2.Content))
	}
	if ir2.Content[0].Type != "text" || ir2.Content[0].Text != "Hi" {
		t.Fatalf("text=%+v", ir2.Content[0])
	}
	if ir2.Content[1].Type != "tool_use" || ir2.Content[1].ToolUse.Name != "get_weather" {
		t.Fatalf("tool_use=%+v", ir2.Content[1])
	}
	if ir2.Usage.InputTokens != 10 || ir2.Usage.OutputTokens != 8 {
		t.Fatalf("usage=%+v", ir2.Usage)
	}
	if ir2.StopReason != "stop" {
		t.Fatalf("ir2 stop=%q want stop (round-trip end_turn->stop)", ir2.StopReason)
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
	for i := range a.System {
		if a.System[i].Text != b.System[i].Text {
			t.Errorf("system[%d] text %q != %q", i, a.System[i].Text, b.System[i].Text)
		}
	}
	if len(a.Messages) != len(b.Messages) {
		t.Fatalf("messages len %d != %d", len(a.Messages), len(b.Messages))
	}
	for i := range a.Messages {
		am, bm := a.Messages[i], b.Messages[i]
		if am.Role != bm.Role {
			t.Errorf("msg[%d] role %q != %q", i, am.Role, bm.Role)
		}
		if len(am.Content) != len(bm.Content) {
			t.Fatalf("msg[%d] content len %d != %d", i, len(am.Content), len(bm.Content))
		}
		for j := range am.Content {
			ca, cb := am.Content[j], bm.Content[j]
			if ca.Type != cb.Type {
				t.Errorf("msg[%d].content[%d] type %q != %q", i, j, ca.Type, cb.Type)
				continue
			}
			switch ca.Type {
			case "text":
				if ca.Text != cb.Text {
					t.Errorf("msg[%d].content[%d] text %q != %q", i, j, ca.Text, cb.Text)
				}
			case "image":
				// Image payload (URL vs base64) is not preserved across formats by design;
				// only the block type is asserted (checked above).
			case "tool_use":
				if ca.ToolUse == nil || cb.ToolUse == nil {
					t.Errorf("msg[%d].content[%d] tool_use nil", i, j)
					continue
				}
				if ca.ToolUse.ID != cb.ToolUse.ID {
					t.Errorf("tool_use id %q != %q", ca.ToolUse.ID, cb.ToolUse.ID)
				}
				if ca.ToolUse.Name != cb.ToolUse.Name {
					t.Errorf("tool_use name %q != %q", ca.ToolUse.Name, cb.ToolUse.Name)
				}
				if string(ca.ToolUse.Input) != string(cb.ToolUse.Input) {
					t.Errorf("tool_use input %s != %s", ca.ToolUse.Input, cb.ToolUse.Input)
				}
			case "tool_result":
				if ca.ToolResult == nil || cb.ToolResult == nil {
					t.Errorf("msg[%d].content[%d] tool_result nil", i, j)
					continue
				}
				if ca.ToolResult.ToolUseID != cb.ToolResult.ToolUseID {
					t.Errorf("tool_result id %q != %q", ca.ToolResult.ToolUseID, cb.ToolResult.ToolUseID)
				}
				if len(ca.ToolResult.Content) != len(cb.ToolResult.Content) {
					t.Fatalf("tool_result content len %d != %d", len(ca.ToolResult.Content), len(cb.ToolResult.Content))
				}
				for k := range ca.ToolResult.Content {
					ra, rb := ca.ToolResult.Content[k], cb.ToolResult.Content[k]
					if ra.Type != rb.Type {
						t.Errorf("tool_result.content[%d] type %q != %q", k, ra.Type, rb.Type)
						continue
					}
					if ra.Type == "text" && ra.Text != rb.Text {
						t.Errorf("tool_result.content[%d] text %q != %q", k, ra.Text, rb.Text)
					}
				}
			}
		}
	}
	if len(a.Tools) != len(b.Tools) {
		t.Fatalf("tools len %d != %d", len(a.Tools), len(b.Tools))
	}
	for i := range a.Tools {
		ta, tb := a.Tools[i], b.Tools[i]
		if ta.Name != tb.Name {
			t.Errorf("tool[%d] name %q != %q", i, ta.Name, tb.Name)
		}
		if string(ta.InputSchema) != string(tb.InputSchema) {
			t.Errorf("tool[%d] input_schema %s != %s", i, ta.InputSchema, tb.InputSchema)
		}
	}
	if (a.ToolChoice == nil) != (b.ToolChoice == nil) {
		t.Fatalf("tool_choice presence mismatch")
	}
	if a.ToolChoice != nil {
		if a.ToolChoice.Type != b.ToolChoice.Type {
			t.Errorf("tool_choice type %q != %q", a.ToolChoice.Type, b.ToolChoice.Type)
		}
		if a.ToolChoice.Name != b.ToolChoice.Name {
			t.Errorf("tool_choice name %q != %q", a.ToolChoice.Name, b.ToolChoice.Name)
		}
	}
}
