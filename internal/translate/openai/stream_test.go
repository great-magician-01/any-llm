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

// TestStreamDecode_ToolUseArgsInFirstChunk covers DeepSeek's real streaming
// pattern: the first tool_calls chunk carries the id, the name AND the first
// arguments fragment together. That fragment must not be dropped, or the
// accumulated tool input JSON is corrupted and the client fails to parse it.
func TestStreamDecode_ToolUseArgsInFirstChunk(t *testing.T) {
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
	// id + name + first arguments fragment in the SAME chunk (DeepSeek style)
	dec(`{"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":"}}]}}]}`)
	dec(`{"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"SF\"}"}}]}}]}`)
	dec(`{"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"completion_tokens":10,"total_tokens":15}}`)
	dec("[DONE]")

	var json string
	var start *translate.StreamEvent
	for _, e := range got {
		if e.Type == "content_block_start" && e.Block != nil && e.Block.Type == "tool_use" {
			start = e
		}
		if e.Type == "content_block_delta" && e.Delta != nil && e.Delta.Type == "input_json_delta" {
			json += e.Delta.PartialJSON
		}
	}
	if start == nil || start.Block.ToolUse.ID != "call_1" || start.Block.ToolUse.Name != "get_weather" {
		t.Fatalf("no tool_use start: %+v", start)
	}
	if json != `{"city":"SF"}` {
		t.Fatalf("accumulated partial_json=%q want %q", json, `{"city":"SF"}`)
	}
}

func TestStreamDecode_ParallelToolCalls(t *testing.T) {
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
	dec(`{"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_2","type":"function","function":{"name":"get_time","arguments":""}}]}}]}`)
	dec(`{"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":\"SF\"}"}}]}}]}`)
	dec(`{"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"{\"tz\":\"PST\"}"}}]}}]}`)
	dec(`{"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"completion_tokens":10,"total_tokens":15}}`)
	dec("[DONE]")

	blockStarts := map[int]string{}
	type deltaInfo struct {
		Index int
		JSON  string
	}
	var deltas []deltaInfo
	for _, e := range got {
		if e.Type == "content_block_start" && e.Block != nil && e.Block.Type == "tool_use" {
			blockStarts[e.Index] = e.Block.ToolUse.ID
		}
		if e.Type == "content_block_delta" && e.Delta != nil && e.Delta.Type == "input_json_delta" {
			deltas = append(deltas, deltaInfo{Index: e.Index, JSON: e.Delta.PartialJSON})
		}
	}
	if blockStarts[1] != "call_1" || blockStarts[2] != "call_2" {
		t.Fatalf("block starts=%+v", blockStarts)
	}
	if len(deltas) != 2 {
		t.Fatalf("deltas=%+v", deltas)
	}
	if deltas[0].Index != 1 || deltas[0].JSON != `{"city":"SF"}` {
		t.Fatalf("delta[0]=%+v want block 1 {\"city\":\"SF\"}", deltas[0])
	}
	if deltas[1].Index != 2 || deltas[1].JSON != `{"tz":"PST"}` {
		t.Fatalf("delta[1]=%+v want block 2 {\"tz\":\"PST\"}", deltas[1])
	}
}

// TestStreamDecode_SeparatedUsageChunk covers the standard OpenAI streaming
// pattern with stream_options.include_usage=true: usage arrives in a
// SEPARATE chunk (empty choices) AFTER the finish_reason chunk. The decoder
// must buffer the stop_reason and emit a single message_delta carrying both
// stop_reason and usage when the usage chunk arrives.
func TestStreamDecode_SeparatedUsageChunk(t *testing.T) {
	d := NewStreamDecoder()
	var got []*translate.StreamEvent
	dec := func(s string) {
		evs, err := d.Decode([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, evs...)
	}
	dec(`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"Hi"}}]}`)
	// finish_reason WITHOUT usage (standard pattern)
	dec(`{"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
	// usage-only chunk (empty choices) arrives separately after finish_reason
	dec(`{"id":"c1","choices":[],"usage":{"prompt_tokens":42,"completion_tokens":7,"total_tokens":49}}`)
	dec("[DONE]")

	wantTypes := []string{
		"message_start", "content_block_start", "content_block_delta",
		"content_block_stop", "message_delta", "message_stop",
	}
	if len(got) != len(wantTypes) {
		t.Fatalf("event count=%d want %d (events=%+v)", len(got), len(wantTypes), got)
	}
	for i, w := range wantTypes {
		if got[i].Type != w {
			t.Fatalf("event[%d]=%q want %q", i, got[i].Type, w)
		}
	}
	md := got[4]
	if md.StopReason != "stop" {
		t.Fatalf("message_delta stop_reason=%q want stop", md.StopReason)
	}
	if md.InputTokens != 42 {
		t.Fatalf("message_delta input_tokens=%d want 42", md.InputTokens)
	}
	if md.OutputTokens != 7 {
		t.Fatalf("message_delta output_tokens=%d want 7", md.OutputTokens)
	}
}

// TestStreamDecode_NoUsage covers the case where the upstream never sends a
// usage chunk (e.g. stream_options not supported). The decoder should still
// emit a message_delta with just the stop_reason at [DONE].
func TestStreamDecode_NoUsage(t *testing.T) {
	d := NewStreamDecoder()
	var got []*translate.StreamEvent
	dec := func(s string) {
		evs, err := d.Decode([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, evs...)
	}
	dec(`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"Hi"}}]}`)
	dec(`{"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
	dec("[DONE]")

	wantTypes := []string{
		"message_start", "content_block_start", "content_block_delta",
		"content_block_stop", "message_delta", "message_stop",
	}
	if len(got) != len(wantTypes) {
		t.Fatalf("event count=%d want %d (events=%+v)", len(got), len(wantTypes), got)
	}
	for i, w := range wantTypes {
		if got[i].Type != w {
			t.Fatalf("event[%d]=%q want %q", i, got[i].Type, w)
		}
	}
	md := got[4]
	if md.StopReason != "stop" {
		t.Fatalf("message_delta stop_reason=%q want stop", md.StopReason)
	}
	if md.InputTokens != 0 || md.OutputTokens != 0 {
		t.Fatalf("message_delta usage should be zero, got in=%d out=%d", md.InputTokens, md.OutputTokens)
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

// TestStreamDecode_ReasoningThenToolUse covers DeepSeek reasoning models:
// reasoning_content streams BEFORE content and tool_calls. The decoder must
// expose it as a thinking block at index 0, close it when content starts, and
// emit a synthesized signature delta (message id) before the stop.
func TestStreamDecode_ReasoningThenToolUse(t *testing.T) {
	d := NewStreamDecoder()
	var got []*translate.StreamEvent
	dec := func(s string) {
		evs, err := d.Decode([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, evs...)
	}
	dec(`{"id":"c1","model":"deepseek-v4-flash","choices":[{"index":0,"delta":{"role":"assistant","content":null,"reasoning_content":""}}]}`)
	dec(`{"id":"c1","choices":[{"index":0,"delta":{"content":null,"reasoning_content":"The"}}]}`)
	dec(`{"id":"c1","choices":[{"index":0,"delta":{"content":null,"reasoning_content":" user"}}]}`)
	dec(`{"id":"c1","choices":[{"index":0,"delta":{"content":"I","reasoning_content":null}}]}`)
	dec(`{"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":""}}]}}]}`)
	dec(`{"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":\"SF\"}"}}]}}]}`)
	dec(`{"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"completion_tokens":10,"total_tokens":15}}`)
	dec("[DONE]")

	wantTypes := []string{
		"message_start",
		"content_block_start", "content_block_delta", "content_block_delta", // thinking start + deltas
		"content_block_delta", "content_block_stop", // signature delta + thinking stop
		"content_block_start", "content_block_delta", // text
		"content_block_stop",                         // text stop
		"content_block_start", "content_block_delta", // tool start + args
		"content_block_stop", // tool stop
		"message_delta", "message_stop",
	}
	if len(got) != len(wantTypes) {
		t.Fatalf("event count=%d want %d (events=%+v)", len(got), len(wantTypes), got)
	}
	for i, w := range wantTypes {
		if got[i].Type != w {
			t.Fatalf("event[%d]=%q want %q", i, got[i].Type, w)
		}
	}
	// thinking block at index 0 (first block)
	if got[1].Index != 0 || got[1].Block == nil || got[1].Block.Type != "thinking" {
		t.Fatalf("thinking start=%+v", got[1])
	}
	// thinking deltas accumulate reasoning
	think := got[2].Delta.Thinking + got[3].Delta.Thinking
	if think != "The user" {
		t.Fatalf("thinking=%q want %q", think, "The user")
	}
	// synthesized signature from message id, then thinking stop
	if got[4].Delta == nil || got[4].Delta.Type != "signature_delta" || got[4].Delta.Signature != "c1" {
		t.Fatalf("signature delta=%+v", got[4])
	}
	// text and tool blocks ascend after thinking
	if got[6].Index != 1 || got[6].Block.Type != "text" {
		t.Fatalf("text start=%+v", got[6])
	}
	if got[9].Index != 2 || got[9].Block.ToolUse.ID != "call_1" {
		t.Fatalf("tool start=%+v", got[9])
	}
	if got[12].StopReason != "tool_calls" || got[12].InputTokens != 5 || got[12].OutputTokens != 10 {
		t.Fatalf("message_delta=%+v", got[12])
	}
}

// TestStreamDecode_ReasoningOnly covers a response where the model reasons and
// then stops without content or tool calls: the thinking block must be closed
// at finish_reason.
func TestStreamDecode_ReasoningOnly(t *testing.T) {
	d := NewStreamDecoder()
	var got []*translate.StreamEvent
	dec := func(s string) {
		evs, err := d.Decode([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, evs...)
	}
	dec(`{"id":"c1","model":"deepseek-v4-flash","choices":[{"index":0,"delta":{"role":"assistant","content":null,"reasoning_content":""}}]}`)
	dec(`{"id":"c1","choices":[{"index":0,"delta":{"content":null,"reasoning_content":"Hmm"}}]}`)
	dec(`{"id":"c1","choices":[{"index":0,"delta":{"content":"","reasoning_content":null},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`)
	dec("[DONE]")

	wantTypes := []string{
		"message_start",
		"content_block_start", "content_block_delta",
		"content_block_delta", "content_block_stop", // signature delta + stop
		"message_delta", "message_stop",
	}
	if len(got) != len(wantTypes) {
		t.Fatalf("event count=%d want %d (events=%+v)", len(got), len(wantTypes), got)
	}
	if got[1].Index != 0 || got[1].Block.Type != "thinking" {
		t.Fatalf("thinking start=%+v", got[1])
	}
	if got[4].Type != "content_block_stop" || got[4].Index != 0 {
		t.Fatalf("thinking stop=%+v", got[4])
	}
	if got[5].StopReason != "stop" {
		t.Fatalf("message_delta=%+v", got[5])
	}
}

// TestStreamDecode_FullUsageChunk verifies the usage-only chunk carries the
// full breakdown (cache hits + reasoning) onto the deferred message_delta.
func TestStreamDecode_FullUsageChunk(t *testing.T) {
	d := NewStreamDecoder()
	var got []*translate.StreamEvent
	dec := func(s string) {
		evs, err := d.Decode([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, evs...)
	}
	dec(`{"id":"c1","model":"deepseek-v4-flash","choices":[{"index":0,"delta":{"role":"assistant","content":"Hi"}}]}`)
	dec(`{"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
	dec(`{"id":"c1","choices":[],"usage":{"prompt_tokens":437,"completion_tokens":82,"total_tokens":519,"prompt_tokens_details":{"cached_tokens":384},"completion_tokens_details":{"reasoning_tokens":26},"prompt_cache_hit_tokens":384,"prompt_cache_miss_tokens":53}}`)
	dec("[DONE]")

	var md *translate.StreamEvent
	for _, e := range got {
		if e.Type == "message_delta" {
			md = e
		}
	}
	if md == nil {
		t.Fatalf("no message_delta: %+v", got)
	}
	if md.InputTokens != 437 || md.OutputTokens != 82 {
		t.Fatalf("in/out=%d/%d", md.InputTokens, md.OutputTokens)
	}
	if md.CacheReadTokens != 384 {
		t.Fatalf("cache_read=%d want 384", md.CacheReadTokens)
	}
	if md.ReasoningTokens != 26 {
		t.Fatalf("reasoning=%d want 26", md.ReasoningTokens)
	}
}

// TestStreamEncode_FullUsage verifies the OpenAI stream chunk carries the full
// usage breakdown for OpenAI-format clients.
func TestStreamEncode_FullUsage(t *testing.T) {
	e := NewStreamEncoder("gpt-4o")
	fs, err := e.Encode(&translate.StreamEvent{
		Type:            "message_delta",
		StopReason:      "stop",
		InputTokens:     437,
		OutputTokens:    82,
		CacheReadTokens: 384,
		ReasoningTokens: 26,
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, f := range fs {
		joined += string(f)
	}
	for _, want := range []string{
		`"prompt_tokens":437`,
		`"completion_tokens":82`,
		`"total_tokens":519`,
		`"cached_tokens":384`,
		`"reasoning_tokens":26`,
		`"prompt_cache_hit_tokens":384`,
		`"prompt_cache_miss_tokens":53`,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %s in %s", want, joined)
		}
	}
}
