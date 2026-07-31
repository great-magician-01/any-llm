package translate_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/great-magician-01/any-llm/internal/translate"
	"github.com/great-magician-01/any-llm/internal/translate/anthropic"
	"github.com/great-magician-01/any-llm/internal/translate/openai"
	"github.com/great-magician-01/any-llm/internal/translate/responses"
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

// TestCrossUsage_OpenAIToAnthropic verifies the full token breakdown survives
// the OpenAI -> Anthropic response conversion: prompt cache hits and reasoning
// tokens from DeepSeek must appear as cache_read_input_tokens in the Anthropic
// response (reasoning has no Anthropic field and is dropped there).
func TestCrossUsage_OpenAIToAnthropic(t *testing.T) {
	src := []byte(`{
		"id":"c1","model":"deepseek-v4-flash",
		"choices":[{"index":0,"message":{"role":"assistant","content":"Hi","reasoning_content":"Hmm"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":437,"completion_tokens":82,"total_tokens":519,
			"prompt_tokens_details":{"cached_tokens":384},
			"completion_tokens_details":{"reasoning_tokens":26},
			"prompt_cache_hit_tokens":384,"prompt_cache_miss_tokens":53}
	}`)
	ir1, err := openai.DecodeResponse(src)
	if err != nil {
		t.Fatal(err)
	}
	if ir1.Usage.CacheReadTokens != 384 || ir1.Usage.ReasoningTokens != 26 {
		t.Fatalf("ir usage=%+v", ir1.Usage)
	}
	antBytes, err := anthropic.EncodeResponse(ir1)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(antBytes, &m); err != nil {
		t.Fatal(err)
	}
	usage, _ := m["usage"].(map[string]any)
	if usage["input_tokens"] != float64(437) || usage["output_tokens"] != float64(82) {
		t.Fatalf("usage=%v", usage)
	}
	if usage["cache_read_input_tokens"] != float64(384) {
		t.Fatalf("cache_read=%v want 384", usage["cache_read_input_tokens"])
	}
}

// TestCrossUsage_AnthropicToOpenAI verifies Anthropic cache fields survive the
// conversion to an OpenAI response (cache_read -> cached_tokens).
func TestCrossUsage_AnthropicToOpenAI(t *testing.T) {
	src := []byte(`{
		"id":"msg_1","model":"deepseek-v4-flash",
		"content":[{"type":"text","text":"Hi"}],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":59,"output_tokens":71,"cache_creation_input_tokens":120,"cache_read_input_tokens":384}
	}`)
	ir1, err := anthropic.DecodeResponse(src)
	if err != nil {
		t.Fatal(err)
	}
	oaiBytes, err := openai.EncodeResponse(ir1)
	if err != nil {
		t.Fatal(err)
	}
	s := string(oaiBytes)
	for _, want := range []string{
		`"prompt_tokens":59`,
		`"completion_tokens":71`,
		`"total_tokens":130`,
		`"cached_tokens":384`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %s in %s", want, s)
		}
	}
}

// Responses 请求 -> IR -> OpenAI chat completions -> IR，语义一致
func TestCrossRequest_ResponsesToOpenAI(t *testing.T) {
	src := []byte(`{
		"model":"gpt-4o",
		"instructions":"be good",
		"input":[
			{"role":"user","content":[{"type":"input_text","text":"hi"}]},
			{"role":"assistant","content":[{"type":"input_text","text":"sure"}]},
			{"type":"function_call","call_id":"call_1","name":"get_weather","arguments":"{\"city\":\"SF\"}"},
			{"type":"function_call_output","call_id":"call_1","output":"sunny"}
		],
		"tools":[{"type":"function","name":"get_weather","description":"w","parameters":{"type":"object"}}],
		"tool_choice":"auto","max_output_tokens":50
	}`)
	ir1, err := responses.DecodeRequest(src)
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

// OpenAI chat completions -> IR -> Responses -> IR，语义一致
func TestCrossRequest_OpenAIToResponses(t *testing.T) {
	src := []byte(`{
		"model":"gpt-4o",
		"messages":[
			{"role":"system","content":"be good"},
			{"role":"user","content":"hi"},
			{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"SF\"}"}}]},
			{"role":"tool","tool_call_id":"call_1","content":"sunny"}
		],
		"tools":[{"type":"function","function":{"name":"get_weather","description":"w","parameters":{"type":"object"}}}],
		"tool_choice":"auto","max_tokens":50
	}`)
	ir1, err := openai.DecodeRequest(src)
	if err != nil {
		t.Fatal(err)
	}
	rspBytes, err := responses.EncodeRequest(ir1)
	if err != nil {
		t.Fatal(err)
	}
	ir2, err := responses.DecodeRequest(rspBytes)
	if err != nil {
		t.Fatal(err)
	}
	assertRequestsMatch(t, ir1, ir2)
}

// Responses 请求 -> IR -> Anthropic -> IR，语义一致
func TestCrossRequest_ResponsesToAnthropic(t *testing.T) {
	src := []byte(`{
		"model":"claude-3-5",
		"instructions":"be good",
		"input":[
			{"role":"user","content":[{"type":"input_text","text":"hi"}]},
			{"role":"assistant","content":[{"type":"input_text","text":"sure"}]},
			{"type":"function_call","call_id":"call_1","name":"get_weather","arguments":"{\"city\":\"SF\"}"},
			{"type":"function_call_output","call_id":"call_1","output":"sunny"}
		],
		"tools":[{"type":"function","name":"get_weather","description":"w","parameters":{"type":"object"}}],
		"tool_choice":"auto","max_output_tokens":50
	}`)
	ir1, err := responses.DecodeRequest(src)
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

// Responses 响应 -> IR -> Anthropic -> IR（含 thinking 与 usage）
func TestCrossResponse_ResponsesToAnthropic(t *testing.T) {
	src := []byte(`{
		"id":"resp_1","object":"response","created_at":1,"status":"completed","model":"claude-3-5",
		"output":[
			{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"hmm"}],"content":[]},
			{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"Hi"}]},
			{"type":"function_call","id":"fc_1","call_id":"call_1","name":"get_weather","arguments":"{\"city\":\"SF\"}"}
		],
		"usage":{"input_tokens":10,"output_tokens":8,"total_tokens":18,
			"input_tokens_details":{"cached_tokens":5},
			"output_tokens_details":{"reasoning_tokens":2}}
	}`)
	ir1, err := responses.DecodeResponse(src)
	if err != nil {
		t.Fatal(err)
	}
	if ir1.StopReason != "tool_calls" || ir1.Usage.CacheReadTokens != 5 || ir1.Usage.ReasoningTokens != 2 {
		t.Fatalf("ir1=%+v usage=%+v", ir1, ir1.Usage)
	}
	antBytes, err := anthropic.EncodeResponse(ir1)
	if err != nil {
		t.Fatal(err)
	}
	ir2, err := anthropic.DecodeResponse(antBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(ir2.Content) != 3 {
		t.Fatalf("content len=%d", len(ir2.Content))
	}
	if ir2.Content[0].Type != "thinking" || ir2.Content[0].Thinking != "hmm" {
		t.Fatalf("c0=%+v", ir2.Content[0])
	}
	if ir2.Content[1].Type != "text" || ir2.Content[1].Text != "Hi" {
		t.Fatalf("c1=%+v", ir2.Content[1])
	}
	if ir2.Content[2].Type != "tool_use" || ir2.Content[2].ToolUse.Name != "get_weather" {
		t.Fatalf("c2=%+v", ir2.Content[2])
	}
	if ir2.Usage.InputTokens != 10 || ir2.Usage.OutputTokens != 8 || ir2.Usage.CacheReadTokens != 5 {
		t.Fatalf("usage=%+v", ir2.Usage)
	}
}

// Anthropic 响应 -> IR -> Responses，usage/thinking 透传（JSON 断言）
func TestCrossResponse_AnthropicToResponses(t *testing.T) {
	src := []byte(`{
		"id":"msg_1","model":"claude-3-5",
		"content":[{"type":"thinking","thinking":"hmm"},{"type":"text","text":"Hi"}],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":59,"output_tokens":71,"cache_read_input_tokens":384}
	}`)
	ir1, err := anthropic.DecodeResponse(src)
	if err != nil {
		t.Fatal(err)
	}
	rspBytes, err := responses.EncodeResponse(ir1)
	if err != nil {
		t.Fatal(err)
	}
	s := string(rspBytes)
	for _, want := range []string{
		`"input_tokens":59`, `"output_tokens":71`, `"total_tokens":130`,
		`"cached_tokens":384`, `"summary"`, `"hmm"`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %s in %s", want, s)
		}
	}
}
