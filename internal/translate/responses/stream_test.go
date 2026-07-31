package responses

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/great-magician-01/any-llm/internal/translate"
)

// 文本回复完整序列
func TestStreamDecode_TextSequence(t *testing.T) {
	d := NewStreamDecoder()
	frames := []string{
		`{"type":"response.created","response":{"id":"resp_1","object":"response","created_at":1,"status":"in_progress","model":"m","output":[]}}`,
		`{"type":"response.in_progress","response":{"id":"resp_1"}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg_1","type":"message","status":"in_progress","role":"assistant","content":[]}}`,
		`{"type":"response.content_part.added","item_id":"msg_1","output_index":0,"content_index":0,"part":{"type":"output_text","text":"","annotations":[]}}`,
		`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"Hi"}`,
		`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":" there"}`,
		`{"type":"response.output_text.done","item_id":"msg_1","output_index":0,"content_index":0,"text":"Hi there"}`,
		`{"type":"response.content_part.done","item_id":"msg_1","output_index":0,"content_index":0,"part":{"type":"output_text","text":"Hi there","annotations":[]}}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"Hi there","annotations":[]}]}}`,
		`{"type":"response.completed","response":{"id":"resp_1","object":"response","created_at":1,"status":"completed","model":"m","output":[{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"Hi there","annotations":[]}]}],"usage":{"input_tokens":10,"output_tokens":3,"total_tokens":13,"input_tokens_details":{"cached_tokens":7},"output_tokens_details":{"reasoning_tokens":1}}}}`,
	}
	var evs []*translate.StreamEvent
	for _, f := range frames {
		got, err := d.Decode([]byte(f))
		if err != nil {
			t.Fatal(err)
		}
		evs = append(evs, got...)
	}
	if len(evs) < 3 {
		t.Fatalf("too few events: %d", len(evs))
	}
	if evs[0].Type != "message_start" || evs[0].MessageID != "resp_1" || evs[0].Model != "m" {
		t.Fatalf("ev0=%+v", evs[0])
	}
	if evs[1].Type != "content_block_start" || evs[1].Index != 0 || evs[1].Block.Type != "text" {
		t.Fatalf("ev1=%+v", evs[1])
	}
	var text string
	for _, ev := range evs {
		if ev.Type == "content_block_delta" && ev.Delta != nil && ev.Delta.Type == "text_delta" {
			text += ev.Delta.Text
		}
	}
	if text != "Hi there" {
		t.Fatalf("text=%q", text)
	}
	var stop *translate.StreamEvent
	var last *translate.StreamEvent
	for _, ev := range evs {
		if ev.Type == "message_delta" {
			stop = ev
		}
		last = ev
	}
	if stop == nil || stop.StopReason != "stop" || stop.InputTokens != 10 || stop.OutputTokens != 3 ||
		stop.CacheReadTokens != 7 || stop.ReasoningTokens != 1 {
		t.Fatalf("message_delta=%+v", stop)
	}
	if last.Type != "message_stop" {
		t.Fatalf("last=%+v", last)
	}
}

// 工具调用 + arguments 跨 chunk 分段
func TestStreamDecode_ToolUse(t *testing.T) {
	d := NewStreamDecoder()
	frames := []string{
		`{"type":"response.created","response":{"id":"resp_1","status":"in_progress","model":"m","output":[]}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"get_weather","arguments":""}}`,
		`{"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":0,"delta":"{\"city\":"}`,
		`{"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":0,"delta":"\"SF\"}"}`,
		`{"type":"response.function_call_arguments.done","item_id":"fc_1","output_index":0,"arguments":"{\"city\":\"SF\"}"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"get_weather","arguments":"{\"city\":\"SF\"}"}}`,
		`{"type":"response.completed","response":{"id":"resp_1","status":"completed","model":"m","output":[],"usage":{"input_tokens":5,"output_tokens":1,"total_tokens":6}}}`,
	}
	var evs []*translate.StreamEvent
	for _, f := range frames {
		got, err := d.Decode([]byte(f))
		if err != nil {
			t.Fatal(err)
		}
		evs = append(evs, got...)
	}
	var start, json1, json2, stop *translate.StreamEvent
	for _, ev := range evs {
		switch {
		case ev.Type == "content_block_start":
			start = ev
		case ev.Type == "content_block_delta" && ev.Delta != nil && ev.Delta.Type == "input_json_delta":
			if json1 == nil {
				json1 = ev
			} else {
				json2 = ev
			}
		case ev.Type == "content_block_stop":
			stop = ev
		}
	}
	if start == nil || start.Block.Type != "tool_use" || start.Block.ToolUse.ID != "call_1" ||
		start.Block.ToolUse.Name != "get_weather" {
		t.Fatalf("start=%+v", start)
	}
	if json1 == nil || json1.Delta.PartialJSON != `{"city":` || json2 == nil || json2.Delta.PartialJSON != `"SF"}` {
		t.Fatalf("deltas=%+v %+v", json1, json2)
	}
	if stop == nil || stop.Index != start.Index {
		t.Fatalf("stop=%+v", stop)
	}
}

// arguments 只在 output_item.done 出现（从未发 delta）——补发完整 input_json_delta
func TestStreamDecode_ToolUseArgsOnlyInDone(t *testing.T) {
	d := NewStreamDecoder()
	frames := []string{
		`{"type":"response.created","response":{"id":"resp_1","status":"in_progress","model":"m","output":[]}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"get_weather","arguments":""}}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"get_weather","arguments":"{\"city\":\"SF\"}"}}`,
	}
	var evs []*translate.StreamEvent
	for _, f := range frames {
		got, _ := d.Decode([]byte(f))
		evs = append(evs, got...)
	}
	// 期望：start、stop 前有补发的完整 arguments delta
	var jsonDelta, stop *translate.StreamEvent
	for _, ev := range evs {
		if ev.Type == "content_block_delta" && ev.Delta != nil && ev.Delta.Type == "input_json_delta" {
			jsonDelta = ev
		}
		if ev.Type == "content_block_stop" {
			stop = ev
		}
	}
	if jsonDelta == nil || jsonDelta.Delta.PartialJSON != `{"city":"SF"}` {
		t.Fatalf("jsonDelta=%+v", jsonDelta)
	}
	if stop == nil {
		t.Fatal("no content_block_stop")
	}
}

// 推理 + 文本混合（summary delta 转 thinking_delta，reasoning_text 忽略）
func TestStreamDecode_ReasoningThenText(t *testing.T) {
	d := NewStreamDecoder()
	frames := []string{
		`{"type":"response.created","response":{"id":"resp_1","status":"in_progress","model":"m","output":[]}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"rs_1","type":"reasoning","summary":[],"content":[]}}`,
		`{"type":"response.reasoning_summary_text.delta","item_id":"rs_1","output_index":0,"delta":"think "}`,
		`{"type":"response.reasoning_text.delta","item_id":"rs_1","output_index":0,"delta":"long reasoning, ignored"}`,
		`{"type":"response.reasoning_summary_text.delta","item_id":"rs_1","output_index":0,"delta":"it through"}`,
		`{"type":"response.reasoning_summary_text.done","item_id":"rs_1","output_index":0,"summary":[{"type":"summary_text","text":"think it through"}]}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"rs_1","type":"reasoning","summary":[{"type":"summary_text","text":"think it through"}],"content":[]}}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"id":"msg_1","type":"message","status":"in_progress","role":"assistant","content":[]}}`,
		`{"type":"response.output_text.delta","item_id":"msg_1","output_index":1,"content_index":0,"delta":"Hi"}`,
		`{"type":"response.output_item.done","output_index":1,"item":{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"Hi","annotations":[]}]}}`,
		`{"type":"response.completed","response":{"id":"resp_1","status":"completed","model":"m","output":[]}}`,
	}
	var evs []*translate.StreamEvent
	for _, f := range frames {
		got, _ := d.Decode([]byte(f))
		evs = append(evs, got...)
	}
	// 第一个块是 thinking（index 0），第二个是 text（index 1）
	var starts []*translate.StreamEvent
	var thinkDelta string
	for _, ev := range evs {
		if ev.Type == "content_block_start" {
			starts = append(starts, ev)
		}
		if ev.Type == "content_block_delta" && ev.Delta != nil && ev.Delta.Type == "thinking_delta" {
			thinkDelta += ev.Delta.Thinking
		}
	}
	if len(starts) != 2 || starts[0].Block.Type != "thinking" || starts[1].Block.Type != "text" {
		t.Fatalf("starts=%+v", starts)
	}
	if starts[0].Index != 0 || starts[1].Index != 1 {
		t.Fatalf("indices=%d,%d", starts[0].Index, starts[1].Index)
	}
	if thinkDelta != "think it through" {
		t.Fatalf("thinkDelta=%q", thinkDelta)
	}
}

// 缺失 response.created（防御）：首个事件也发出 message_start
func TestStreamDecode_NoCreated(t *testing.T) {
	d := NewStreamDecoder()
	got, err := d.Decode([]byte(`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","content":[]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Type != "message_start" || got[1].Type != "content_block_start" {
		t.Fatalf("evs=%+v", got)
	}
}

func TestStreamDecode_ErrorEvent(t *testing.T) {
	d := NewStreamDecoder()
	got, err := d.Decode([]byte(`{"type":"response.failed","response":{"id":"r","status":"failed","error":{"code":"x","message":"boom"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Type != "error" {
		t.Fatalf("evs=%+v", got)
	}
}

// 文本回复：完整事件序列断言
func TestStreamEncode_TextSequence(t *testing.T) {
	e := NewStreamEncoder("gpt-4o", "resp_9")
	events := []*translate.StreamEvent{
		{Type: "message_start", MessageID: "resp_9", Model: "gpt-4o", InputTokens: 10, CacheReadTokens: 7},
		{Type: "content_block_start", Index: 0, Block: &translate.ContentBlock{Type: "text"}},
		{Type: "content_block_delta", Index: 0, Delta: &translate.Delta{Type: "text_delta", Text: "Hi"}},
		{Type: "content_block_delta", Index: 0, Delta: &translate.Delta{Type: "text_delta", Text: " there"}},
		{Type: "content_block_stop", Index: 0},
		{Type: "message_delta", StopReason: "stop", OutputTokens: 3, ReasoningTokens: 1},
		{Type: "message_stop"},
	}
	var frames [][]byte
	for _, ev := range events {
		got, err := e.Encode(ev)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range got {
			frames = append(frames, f)
		}
	}
	s := joinFrames(frames)
	// 开头必须有 response.created + response.in_progress
	if !strings.Contains(s, `"type":"response.created"`) || !strings.Contains(s, `"type":"response.in_progress"`) {
		t.Fatalf("missing created/in_progress: %s", s)
	}
	// output_item.added 是 message，part 是 output_text
	// （map 序列化按键排序，id 不一定在 item 里第一个键，用宽松子串断言）
	if !strings.Contains(s, `"id":"msg_`) || !strings.Contains(s, `"type":"message"`) {
		t.Fatalf("missing output_item.added: %s", s)
	}
	// 两个 text delta
	if strings.Count(s, `"type":"response.output_text.delta"`) != 2 {
		t.Fatalf("delta count: %s", s)
	}
	// stop 时发出 done 三连
	for _, want := range []string{"response.output_text.done", "response.content_part.done", "response.output_item.done"} {
		if !strings.Contains(s, `"type":"`+want+`"`) {
			t.Fatalf("missing %s: %s", want, s)
		}
	}
	// completed 带 usage（input 来自 message_start，output 来自 message_delta）
	if !strings.Contains(s, `"type":"response.completed"`) {
		t.Fatalf("missing completed: %s", s)
	}
	if !strings.Contains(s, `"input_tokens":10`) || !strings.Contains(s, `"output_tokens":3`) {
		t.Fatalf("completed usage: %s", s)
	}
	if !strings.Contains(s, `"cached_tokens":7`) || !strings.Contains(s, `"reasoning_tokens":1`) {
		t.Fatalf("completed details: %s", s)
	}
}

// 工具调用：start 块自带 arguments 片段时先补发 delta
func TestStreamEncode_ToolUseWithInitialArgs(t *testing.T) {
	e := NewStreamEncoder("m", "resp_9")
	events := []*translate.StreamEvent{
		{Type: "message_start", MessageID: "resp_9", Model: "m"},
		{Type: "content_block_start", Index: 0, Block: &translate.ContentBlock{
			Type: "tool_use",
			ToolUse: &translate.ToolUse{ID: "call_1", Name: "get_weather", Input: json.RawMessage(`{"city":`)},
		}},
		{Type: "content_block_delta", Index: 0, Delta: &translate.Delta{Type: "input_json_delta", PartialJSON: `"SF"}`}},
		{Type: "content_block_stop", Index: 0},
		{Type: "message_delta", StopReason: "tool_calls"},
		{Type: "message_stop"},
	}
	var frames [][]byte
	for _, ev := range events {
		got, _ := e.Encode(ev)
		for _, f := range got {
			frames = append(frames, f)
		}
	}
	s := joinFrames(frames)
	if !strings.Contains(s, `"type":"response.output_item.added"`) ||
		!strings.Contains(s, `"type":"function_call"`) ||
		!strings.Contains(s, `"call_id":"call_1"`) {
		t.Fatalf("missing function_call item: %s", s)
	}
	// start 自带完整 arguments -> 补发第一段 delta
	if !strings.Contains(s, `"type":"response.function_call_arguments.delta"`) {
		t.Fatalf("missing arguments delta: %s", s)
	}
	if strings.Count(s, `"type":"response.function_call_arguments.delta"`) != 2 {
		t.Fatalf("expected 2 arguments deltas: %s", s)
	}
	if !strings.Contains(s, `"arguments":"{\"city\":\"SF\"}"`) {
		t.Fatalf("missing final arguments in done: %s", s)
	}
}

// 缺失 content_block_start：delta 直接来时自动补合成
func TestStreamEncode_SynthesizeMissingStart(t *testing.T) {
	e := NewStreamEncoder("m", "resp_9")
	var frames [][]byte
	for _, ev := range []*translate.StreamEvent{
		{Type: "message_start", Model: "m"},
		{Type: "content_block_delta", Index: 0, Delta: &translate.Delta{Type: "text_delta", Text: "Hi"}},
		{Type: "content_block_stop", Index: 0},
		{Type: "message_delta", StopReason: "stop", OutputTokens: 1},
		{Type: "message_stop"},
	} {
		got, _ := e.Encode(ev)
		for _, f := range got {
			frames = append(frames, f)
		}
	}
	s := joinFrames(frames)
	if !strings.Contains(s, `"type":"response.output_item.added"`) || !strings.Contains(s, `"type":"response.output_text.delta"`) {
		t.Fatalf("missing synthesized start: %s", s)
	}
	// 合成的 added 帧必须先于触发它的 delta 帧（否则接收方解码器会丢弃首个 delta）
	if strings.Index(s, `"type":"response.output_item.added"`) > strings.Index(s, `"type":"response.output_text.delta"`) {
		t.Fatalf("output_item.added must precede output_text.delta: %s", s)
	}
}

// thinking 块 -> reasoning item（summary 流）
func TestStreamEncode_Thinking(t *testing.T) {
	e := NewStreamEncoder("m", "resp_9")
	var frames [][]byte
	for _, ev := range []*translate.StreamEvent{
		{Type: "message_start", Model: "m"},
		{Type: "content_block_start", Index: 0, Block: &translate.ContentBlock{Type: "thinking"}},
		{Type: "content_block_delta", Index: 0, Delta: &translate.Delta{Type: "thinking_delta", Thinking: "hmm"}},
		{Type: "content_block_stop", Index: 0},
		{Type: "content_block_start", Index: 1, Block: &translate.ContentBlock{Type: "text"}},
		{Type: "content_block_delta", Index: 1, Delta: &translate.Delta{Type: "text_delta", Text: "Hi"}},
		{Type: "content_block_stop", Index: 1},
		{Type: "message_delta", StopReason: "stop", OutputTokens: 2},
		{Type: "message_stop"},
	} {
		got, _ := e.Encode(ev)
		for _, f := range got {
			frames = append(frames, f)
		}
	}
	s := joinFrames(frames)
	if !strings.Contains(s, `"type":"response.reasoning_summary_text.delta"`) ||
		!strings.Contains(s, `"type":"response.reasoning_summary_text.done"`) {
		t.Fatalf("missing reasoning summary events: %s", s)
	}
	if strings.Contains(s, `"type":"response.reasoning_text.delta"`) {
		t.Fatalf("must not emit reasoning_text events: %s", s)
	}
}

// Flush：上游没发 message_stop 时补发 completed；从未 start 时补发 created+completed
func TestStreamEncode_Flush(t *testing.T) {
	e := NewStreamEncoder("m", "resp_9")
	_, _ = e.Encode(&translate.StreamEvent{Type: "message_start", Model: "m"})
	_, _ = e.Encode(&translate.StreamEvent{Type: "content_block_start", Index: 0, Block: &translate.ContentBlock{Type: "text"}})
	_, _ = e.Encode(&translate.StreamEvent{Type: "content_block_delta", Index: 0, Delta: &translate.Delta{Type: "text_delta", Text: "Hi"}})
	_, _ = e.Encode(&translate.StreamEvent{Type: "content_block_stop", Index: 0})
	_, _ = e.Encode(&translate.StreamEvent{Type: "message_delta", StopReason: "stop", OutputTokens: 1})
	frames := e.Flush()
	s := joinFrames(frames)
	if !strings.Contains(s, `"type":"response.completed"`) || !strings.Contains(s, `"output_tokens":1`) {
		t.Fatalf("flush: %s", s)
	}
	// 再次 Flush 应为空（幂等）
	if len(e.Flush()) != 0 {
		t.Fatal("Flush not idempotent")
	}

	e2 := NewStreamEncoder("m", "resp_2")
	frames2 := e2.Flush()
	s2 := joinFrames(frames2)
	if !strings.Contains(s2, `"type":"response.created"`) || !strings.Contains(s2, `"type":"response.completed"`) {
		t.Fatalf("flush empty stream: %s", s2)
	}
}

// Content：累积的模型输出（text/tool_use/thinking）
func TestStreamEncode_Content(t *testing.T) {
	e := NewStreamEncoder("m", "resp_9")
	for _, ev := range []*translate.StreamEvent{
		{Type: "message_start", Model: "m"},
		{Type: "content_block_start", Index: 0, Block: &translate.ContentBlock{Type: "thinking"}},
		{Type: "content_block_delta", Index: 0, Delta: &translate.Delta{Type: "thinking_delta", Thinking: "hmm"}},
		{Type: "content_block_stop", Index: 0},
		{Type: "content_block_start", Index: 1, Block: &translate.ContentBlock{Type: "text"}},
		{Type: "content_block_delta", Index: 1, Delta: &translate.Delta{Type: "text_delta", Text: "Hi"}},
		{Type: "content_block_stop", Index: 1},
		{Type: "content_block_start", Index: 2, Block: &translate.ContentBlock{
			Type: "tool_use", ToolUse: &translate.ToolUse{ID: "call_1", Name: "get_weather", Input: json.RawMessage(`{"city":"SF"}`)},
		}},
		{Type: "content_block_stop", Index: 2},
		{Type: "message_stop"},
	} {
		_, _ = e.Encode(ev)
	}
	c := e.Content()
	if len(c) != 3 {
		t.Fatalf("content len=%d: %+v", len(c), c)
	}
	if c[0].Type != "thinking" || c[0].Thinking != "hmm" {
		t.Fatalf("c0=%+v", c[0])
	}
	if c[1].Type != "text" || c[1].Text != "Hi" {
		t.Fatalf("c1=%+v", c[1])
	}
	if c[2].Type != "tool_use" || c[2].ToolUse.ID != "call_1" || string(c[2].ToolUse.Input) != `{"city":"SF"}` {
		t.Fatalf("c2=%+v", c[2])
	}
}

func joinFrames(frames [][]byte) string {
	var sb strings.Builder
	for _, f := range frames {
		sb.Write(f)
		sb.WriteString("\n")
	}
	return sb.String()
}
