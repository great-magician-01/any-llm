package translate_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/great-magician-01/any-llm/internal/translate"
)

// TestResponseStreamEvents_Shape 覆盖「把完整响应展开成流事件」的形状：类型顺序、
// 每个块的 delta 形态、以及 usage/stop_reason 落在 message_delta 上。
// 这条路径用于「上游无视 stream:true、直接回非流式 JSON」的兜底：网关必须把完整
// 响应重新包装成**合法 SSE 流**，而不是把裸 JSON 塞进 SSE 响应里。
func TestResponseStreamEvents_Shape(t *testing.T) {
	resp := &translate.Response{
		ID: "resp_1", Model: "m", StopReason: "tool_calls",
		Content: []translate.ContentBlock{
			{Type: "thinking", Thinking: "hmm", Signature: "sig_1"},
			{Type: "text", Text: "Hi there"},
			{Type: "tool_use", ToolUse: &translate.ToolUse{ID: "call_1", Name: "f", Input: json.RawMessage(`{"a":1}`)}},
		},
		Usage: translate.Usage{InputTokens: 10, OutputTokens: 4, CacheReadTokens: 7, ReasoningTokens: 2},
	}

	evs := translate.ResponseStreamEvents(resp)
	wantTypes := []string{
		"message_start",
		"content_block_start", "content_block_delta", "content_block_delta", "content_block_stop", // thinking + signature
		"content_block_start", "content_block_delta", "content_block_stop", // text
		"content_block_start", "content_block_delta", "content_block_stop", // tool_use
		"message_delta", "message_stop",
	}
	if len(evs) != len(wantTypes) {
		t.Fatalf("event count=%d want %d: %+v", len(evs), len(wantTypes), evs)
	}
	for i, want := range wantTypes {
		if evs[i].Type != want {
			t.Fatalf("event[%d]=%q want %q", i, evs[i].Type, want)
		}
	}

	if evs[0].MessageID != "resp_1" || evs[0].Model != "m" {
		t.Errorf("message_start=%+v", evs[0])
	}
	// 块索引必须 0/1/2 且 delta 与 start 同 index。
	for _, idx := range []int{0, 1, 2} {
		var start, stop *translate.StreamEvent
		var deltas []*translate.StreamEvent
		for _, ev := range evs {
			if ev.Index != idx {
				continue
			}
			switch ev.Type {
			case "content_block_start":
				start = ev
			case "content_block_stop":
				stop = ev
			case "content_block_delta":
				deltas = append(deltas, ev)
			}
		}
		if start == nil || stop == nil || len(deltas) == 0 {
			t.Fatalf("index %d: start=%v stop=%v deltas=%d", idx, start, stop, len(deltas))
		}
	}
	// thinking 块：thinking_delta + signature_delta；文本块：text_delta；工具块：input_json_delta。
	if d := evs[2].Delta; d == nil || d.Type != "thinking_delta" || d.Thinking != "hmm" {
		t.Errorf("thinking delta=%+v", d)
	}
	if d := evs[3].Delta; d == nil || d.Type != "signature_delta" || d.Signature != "sig_1" {
		t.Errorf("signature delta=%+v", d)
	}
	if d := evs[6].Delta; d == nil || d.Type != "text_delta" || d.Text != "Hi there" {
		t.Errorf("text delta=%+v", d)
	}
	if d := evs[9].Delta; d == nil || d.Type != "input_json_delta" || d.PartialJSON != `{"a":1}` {
		t.Errorf("tool delta=%+v", d)
	}

	md := evs[len(evs)-2]
	if md.Type != "message_delta" || md.StopReason != "tool_calls" {
		t.Errorf("message_delta=%+v", md)
	}
	if md.InputTokens != 10 || md.OutputTokens != 4 || md.CacheReadTokens != 7 || md.ReasoningTokens != 2 {
		t.Errorf("message_delta usage=%+v want 10/4/7/2", md)
	}
}

// TestResponseStreamEvents_EncodesToAllFormats：展开后交给三种出站流编码器，
// 内容与 usage 都必须落到客户端帧里（这正是「裸 JSON 会被 SDK 丢掉」要修的点）。
func TestResponseStreamEvents_EncodesToAllFormats(t *testing.T) {
	resp := &translate.Response{
		ID: "resp_1", Model: "m", StopReason: "stop",
		Content: []translate.ContentBlock{
			{Type: "text", Text: "Hi there"},
			{Type: "tool_use", ToolUse: &translate.ToolUse{ID: "call_1", Name: "f", Input: json.RawMessage(`{"a":1}`)}},
		},
		Usage: translate.Usage{InputTokens: 10, OutputTokens: 4},
	}
	evs := translate.ResponseStreamEvents(resp)

	for _, format := range []string{"openai", "anthropic", "responses"} {
		t.Run(format, func(t *testing.T) {
			joined := strings.Join(encodeTo(t, format, evs), "")
			if !strings.Contains(joined, "Hi there") {
				t.Errorf("文本丢失：%s", joined)
			}
			if !strings.Contains(joined, "call_1") {
				t.Errorf("工具调用丢失：%s", joined)
			}
			// usage：openai 报 prompt_tokens，anthropic/responses 报 input_tokens。
			if !strings.Contains(joined, `"prompt_tokens":10`) && !strings.Contains(joined, `"input_tokens":10`) {
				t.Errorf("输入 token 丢失：%s", joined)
			}
			// 收尾帧必须存在。
			switch format {
			case "openai":
				if !strings.Contains(joined, "[DONE]") {
					t.Errorf("缺少 [DONE]：%s", joined)
				}
			case "anthropic":
				if !strings.Contains(joined, "event: message_stop") {
					t.Errorf("缺少 message_stop：%s", joined)
				}
			case "responses":
				if !strings.Contains(joined, "response.completed") {
					t.Errorf("缺少 response.completed：%s", joined)
				}
			}
		})
	}
}

// TestResponseStreamEvents_NoDuplicateContent 钉住一个真实线上 bug 的回归：
// start 帧若带全量内容而 delta 再发一遍，累积式客户端会把内容收两遍——
// Anthropic SDK 以 start 帧的 text/input 为初值再追加 delta（文本翻倍），
// Responses 编码器把 start 的 input 与 input_json_delta 都当参数片段
// （工具参数被拼成非法 JSON）。正文只能经 delta 到达。
func TestResponseStreamEvents_NoDuplicateContent(t *testing.T) {
	resp := &translate.Response{
		ID: "resp_1", Model: "m", StopReason: "tool_calls",
		Content: []translate.ContentBlock{
			{Type: "text", Text: "hello"},
			{Type: "thinking", Thinking: "hmm", Signature: "sig_1"},
			{Type: "tool_use", ToolUse: &translate.ToolUse{ID: "call_1", Name: "f", Input: json.RawMessage(`{"city":"SF"}`)}},
		},
		Usage: translate.Usage{InputTokens: 3, OutputTokens: 5},
	}
	evs := translate.ResponseStreamEvents(resp)

	// IR 层：start 帧本身不得携带正文。
	for _, ev := range evs {
		if ev.Type != "content_block_start" || ev.Block == nil {
			continue
		}
		switch ev.Block.Type {
		case "text":
			if ev.Block.Text != "" {
				t.Errorf("text start 帧带正文 %q", ev.Block.Text)
			}
		case "thinking":
			if ev.Block.Thinking != "" || ev.Block.Signature != "" {
				t.Errorf("thinking start 帧带正文 %+v", ev.Block)
			}
		case "tool_use":
			if in := string(ev.Block.ToolUse.Input); in != "" && in != "{}" && in != "null" {
				t.Errorf("tool_use start 帧带参数 %q", in)
			}
		}
	}

	// 出站编码层：正文在帧流里恰好出现一次（Responses 的 done/completed 帧
	// 携带全量是协议设计，只看增量帧）。
	t.Run("anthropic", func(t *testing.T) {
		joined := strings.Join(encodeTo(t, "anthropic", evs), "")
		if n := strings.Count(joined, `"hello"`); n != 1 {
			t.Errorf("text 出现 %d 次，want 1:\n%s", n, joined)
		}
		if n := strings.Count(joined, `"hmm"`); n != 1 {
			t.Errorf("thinking 出现 %d 次，want 1:\n%s", n, joined)
		}
		if !strings.Contains(joined, `"input":{}`) {
			t.Errorf("tool_use start 帧 input 应为空对象:\n%s", joined)
		}
		// 参数在 delta 里以 partial_json 字符串形式携带，delta 事件恰好一次。
		if n := strings.Count(joined, "input_json_delta"); n != 1 {
			t.Errorf("input_json_delta 出现 %d 次，want 1:\n%s", n, joined)
		}
	})

	t.Run("responses", func(t *testing.T) {
		joined := strings.Join(encodeTo(t, "responses", evs), "")
		// 帧的 event 行与 data 里的 type 字段同名，按带引号的 type 字段计数。
		if n := strings.Count(joined, `"type":"response.function_call_arguments.delta"`); n != 1 {
			t.Errorf("arguments delta 出现 %d 次，want 1:\n%s", n, joined)
		}
		if strings.Contains(joined, `{\"city\":\"SF\"}{\"city\":\"SF\"}`) {
			t.Errorf("最终 arguments 是重复拼接的非法 JSON:\n%s", joined)
		}
	})

	t.Run("openai", func(t *testing.T) {
		joined := strings.Join(encodeTo(t, "openai", evs), "")
		if n := strings.Count(joined, `"content":"hello"`); n != 1 {
			t.Errorf("content 出现 %d 次，want 1:\n%s", n, joined)
		}
		// arguments 以字符串形式内嵌（JSON 转义），恰好一次。
		if n := strings.Count(joined, `{\"city\":\"SF\"}`); n != 1 {
			t.Errorf("工具参数出现 %d 次，want 1:\n%s", n, joined)
		}
	})
}

// TestResponseStreamEvents_ImageBlock：图片没有 delta 形态，完整 source 必须
// 留在 start 帧里，且三种出站编码器都不能把它弄丢。
func TestResponseStreamEvents_ImageBlock(t *testing.T) {
	resp := &translate.Response{
		ID: "resp_1", Model: "m", StopReason: "stop",
		Content: []translate.ContentBlock{
			{Type: "image", Image: translate.NewImage("https://example.com/cat.png")},
		},
		Usage: translate.Usage{InputTokens: 3, OutputTokens: 5},
	}
	evs := translate.ResponseStreamEvents(resp)
	start := evs[1]
	if start.Type != "content_block_start" || start.Block == nil || start.Block.Image == nil {
		t.Fatalf("image start 事件=%+v", start)
	}
	if got := start.Block.Image.SourceURL(); got != "https://example.com/cat.png" {
		t.Fatalf("image url=%q", got)
	}
	// Anthropic 出站：source 必须出现在 start 帧里（曾经的回归：编成空壳
	// {"type":"image"}）。
	joined := strings.Join(encodeTo(t, "anthropic", evs), "")
	if !strings.Contains(joined, `"url":"https://example.com/cat.png"`) {
		t.Errorf("anthropic start 帧丢失 image source:\n%s", joined)
	}
}
