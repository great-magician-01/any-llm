package openai

import (
	"strings"
	"testing"

	"github.com/great-magician-01/any-llm/internal/translate"
)

func TestStreamDecode_TextFlow(t *testing.T) {
	d := NewStreamDecoder()
	var got []*translate.StreamEvent

	evs, err := d.Decode([]byte(`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, evs...)
	evs, err = d.Decode([]byte(`{"id":"c1","choices":[{"index":0,"delta":{"content":" world"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, evs...)
	fr := "stop"
	evs, err = d.Decode([]byte(`{"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`))
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, evs...)
	evs, err = d.Decode([]byte("[DONE]"))
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, evs...)
	_ = fr

	// Expect: message_start, content_block_start(text,0), content_block_delta(text), content_block_delta(text),
	//         content_block_stop(0), message_delta(stop, usage), message_stop
	wantTypes := []string{
		"message_start", "content_block_start", "content_block_delta", "content_block_delta",
		"content_block_stop", "message_delta", "message_stop",
	}
	if len(got) != len(wantTypes) {
		t.Fatalf("event count=%d want %d", len(got), len(wantTypes))
	}
	for i, w := range wantTypes {
		if got[i].Type != w {
			t.Fatalf("event[%d]=%q want %q", i, got[i].Type, w)
		}
	}
	if got[0].Model != "gpt-4o" || got[0].MessageID != "c1" {
		t.Fatalf("message_start=%+v", got[0])
	}
	// text deltas
	if got[2].Delta.Text != "Hello" || got[3].Delta.Text != " world" {
		t.Fatalf("deltas=%q %q", got[2].Delta.Text, got[3].Delta.Text)
	}
	// message_delta usage
	if got[5].StopReason != "stop" || got[5].OutputTokens != 2 || got[5].InputTokens != 3 {
		t.Fatalf("message_delta=%+v", got[5])
	}
}

func TestStreamDecode_ToolUseFlow(t *testing.T) {
	d := NewStreamDecoder()
	var got []*translate.StreamEvent
	dec := func(s string) {
		evs, err := d.Decode([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, evs...)
	}
	dec(`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`)
	dec(`{"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":""}}]}}]}`)
	dec(`{"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":\"SF\"}"}}]}}]}`)
	dec(`{"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"completion_tokens":10,"total_tokens":15}}`)
	dec("[DONE]")

	// Find the tool_use content_block_start
	var toolStart *translate.StreamEvent
	for _, e := range got {
		if e.Type == "content_block_start" && e.Block != nil && e.Block.Type == "tool_use" {
			toolStart = e
		}
	}
	if toolStart == nil || toolStart.Block.ToolUse.ID != "call_1" || toolStart.Block.ToolUse.Name != "get_weather" {
		t.Fatalf("no tool_use start: %+v", toolStart)
	}
	// input_json_delta
	var jsonDelta *translate.StreamEvent
	for _, e := range got {
		if e.Type == "content_block_delta" && e.Delta != nil && e.Delta.Type == "input_json_delta" {
			jsonDelta = e
		}
	}
	if jsonDelta == nil || jsonDelta.Delta.PartialJSON != `{"city":"SF"}` {
		t.Fatalf("no json delta: %+v", jsonDelta)
	}
	if got[len(got)-2].StopReason != "tool_calls" {
		t.Fatalf("stop_reason=%+v", got[len(got)-2])
	}
}

func TestStreamEncode_TextFlow(t *testing.T) {
	e := NewStreamEncoder("gpt-4o")
	var lines []string
	enc := func(evt *translate.StreamEvent) {
		fs, err := e.Encode(evt)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range fs {
			lines = append(lines, string(f))
		}
	}
	enc(&translate.StreamEvent{Type: "message_start", MessageID: "m1", Model: "gpt-4o"})
	enc(&translate.StreamEvent{Type: "content_block_start", Index: 0, Block: &translate.ContentBlock{Type: "text"}})
	enc(&translate.StreamEvent{Type: "content_block_delta", Index: 0, Delta: &translate.Delta{Type: "text_delta", Text: "Hi"}})
	enc(&translate.StreamEvent{Type: "content_block_stop", Index: 0})
	enc(&translate.StreamEvent{Type: "message_delta", StopReason: "stop", InputTokens: 3, OutputTokens: 2})
	enc(&translate.StreamEvent{Type: "message_stop"})

	// first line: role assistant
	if !strings.Contains(lines[0], `"role":"assistant"`) {
		t.Fatalf("line0=%s", lines[0])
	}
	// second: content Hi
	if !strings.Contains(lines[1], `"content":"Hi"`) {
		t.Fatalf("line1=%s", lines[1])
	}
	// finish_reason stop
	found := false
	for _, l := range lines {
		if strings.Contains(l, `"finish_reason":"stop"`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("no finish_reason: %v", lines)
	}
	// [DONE]
	if !strings.HasSuffix(lines[len(lines)-1], "data: [DONE]\n\n") {
		t.Fatalf("last line=%q", lines[len(lines)-1])
	}
}

func TestStreamEncode_ToolUseFlow(t *testing.T) {
	e := NewStreamEncoder("gpt-4o")
	var lines []string
	enc := func(evt *translate.StreamEvent) {
		fs, err := e.Encode(evt)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range fs {
			lines = append(lines, string(f))
		}
	}
	enc(&translate.StreamEvent{Type: "message_start", MessageID: "m1"})
	enc(&translate.StreamEvent{Type: "content_block_start", Index: 1, Block: &translate.ContentBlock{Type: "tool_use", ToolUse: &translate.ToolUse{ID: "call_1", Name: "get_weather"}}})
	enc(&translate.StreamEvent{Type: "content_block_delta", Index: 1, Delta: &translate.Delta{Type: "input_json_delta", PartialJSON: `{"city":"SF"}`}})
	enc(&translate.StreamEvent{Type: "content_block_stop", Index: 1})
	enc(&translate.StreamEvent{Type: "message_delta", StopReason: "tool_calls"})
	enc(&translate.StreamEvent{Type: "message_stop"})

	joined := strings.Join(lines, "")
	if !strings.Contains(joined, `"name":"get_weather"`) || !strings.Contains(joined, `"id":"call_1"`) {
		t.Fatalf("tool start missing: %s", joined)
	}
	if !strings.Contains(joined, `"arguments":"{\"city\":\"SF\"}"`) {
		t.Fatalf("tool args missing: %s", joined)
	}
	if !strings.Contains(joined, `"finish_reason":"tool_calls"`) {
		t.Fatalf("no tool_calls finish: %s", joined)
	}
}
