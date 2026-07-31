package responses

import (
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
