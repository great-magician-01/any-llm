package openai

import (
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
