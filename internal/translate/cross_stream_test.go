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

// 本文件补 cross_test.go 缺失的「流式」方向矩阵：上游格式 SSE → IR →
// 客户端格式 SSE。cross_test.go 只覆盖非流式的请求/响应往返，而网关的热路径
// 恰恰是流式，历史上流式特有的事件语义（usage 归属、块索引纪律、收尾帧）
// 全都没有交叉测试守。
//
// 三条被钉死的硬规则：
//  1. usage 端到端保真。OpenAI/Responses 上游把提示 token 放在**最后一个
//     usage-only chunk / completed 事件**里，Anthropic 上游放在 message_start；
//     任何一环不转发，客户端看到的计费数据就是错的。
//  2. Anthropic 出站的 content_block_delta/stop 必须落在已经 start 过的
//     index 上（Anthropic SDK 遇到未 start 的 index 会直接 abort 连接），
//     且 start 的 index 必须从 0 起连续。OpenAI 上游的「纯工具调用」轮次会
//     让 IR 的首个块落在 index 1，重编码时必须重排。
//  3. 流必须有收尾帧（message_stop / response.completed / [DONE]），否则
//     客户端会挂到超时。

// upstreamStream 表示一段上游 SSE：format 决定用哪个解码器，payloads 是
// 每个事件的 data 载荷（不含 "data: " 前缀）。
type upstreamStream struct {
	format   string
	payloads []string
}

// decode 把上游 SSE 载荷喂给对应格式的流解码器，返回合并后的 IR 事件。
func (u upstreamStream) decode(t *testing.T) []*translate.StreamEvent {
	t.Helper()
	var evs []*translate.StreamEvent
	switch u.format {
	case "openai":
		d := openai.NewStreamDecoder()
		for _, p := range u.payloads {
			got, err := d.Decode([]byte(p))
			if err != nil {
				t.Fatalf("openai upstream decode %q: %v", p, err)
			}
			evs = append(evs, got...)
		}
	case "anthropic":
		for _, p := range u.payloads {
			ev, err := anthropic.DecodeStreamEvent([]byte(p))
			if err != nil {
				t.Fatalf("anthropic upstream decode %q: %v", p, err)
			}
			if ev != nil {
				evs = append(evs, ev)
			}
		}
	case "responses":
		d := responses.NewStreamDecoder()
		for _, p := range u.payloads {
			got, err := d.Decode([]byte(p))
			if err != nil {
				t.Fatalf("responses upstream decode %q: %v", p, err)
			}
			evs = append(evs, got...)
		}
	default:
		t.Fatalf("unknown upstream format %q", u.format)
	}
	return evs
}

// encodeTo 把 IR 事件渲染成客户端格式的线上帧，并像网关一样在事件循环结束后
// 调用 responses 编码器的 Flush（幂等）。
func encodeTo(t *testing.T, format string, evs []*translate.StreamEvent) []string {
	t.Helper()
	var frames []string
	switch format {
	case "openai":
		e := openai.NewStreamEncoder("gpt-4o")
		for _, ev := range evs {
			fs, err := e.Encode(ev)
			if err != nil {
				t.Fatalf("openai encode %s: %v", ev.Type, err)
			}
			for _, f := range fs {
				frames = append(frames, string(f))
			}
		}
	case "anthropic":
		e := anthropic.NewStreamEncoder()
		for _, ev := range evs {
			fs, err := e.Encode(ev)
			if err != nil {
				t.Fatalf("anthropic encode %s: %v", ev.Type, err)
			}
			for _, f := range fs {
				frames = append(frames, string(f))
			}
		}
	case "responses":
		e := responses.NewStreamEncoder("gpt-4o", "resp_test")
		for _, ev := range evs {
			fs, err := e.Encode(ev)
			if err != nil {
				t.Fatalf("responses encode %s: %v", ev.Type, err)
			}
			for _, f := range fs {
				frames = append(frames, string(f))
			}
		}
		for _, f := range e.Flush() {
			frames = append(frames, string(f))
		}
	default:
		t.Fatalf("unknown inbound format %q", format)
	}
	return frames
}

// ---- 帧解析工具 ----

// framePayloads 取出每帧的 data 载荷（[DONE] 原样返回）。
func framePayloads(frames []string) []string {
	var out []string
	for _, f := range frames {
		for _, line := range strings.Split(f, "\n") {
			if s, ok := strings.CutPrefix(line, "data: "); ok {
				out = append(out, s)
			} else if s, ok := strings.CutPrefix(line, "data:"); ok && s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

// jsonPayloads 把每帧载荷解析成对象；[DONE] 之类的非 JSON 载荷会被跳过，
// 需要它的测试用 hasDone 单独判断。
func jsonPayloads(t *testing.T, frames []string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, p := range framePayloads(frames) {
		if !strings.HasPrefix(p, "{") {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(p), &m); err != nil {
			t.Fatalf("frame payload is not valid JSON: %q: %v", p, err)
		}
		out = append(out, m)
	}
	return out
}

func hasDone(frames []string) bool {
	for _, p := range framePayloads(frames) {
		if p == "[DONE]" {
			return true
		}
	}
	return false
}

// allOfType 返回所有 type == typ 的帧对象（保持顺序）。
func allOfType(objs []map[string]any, typ string) []map[string]any {
	var out []map[string]any
	for _, o := range objs {
		if s, _ := o["type"].(string); s == typ {
			out = append(out, o)
		}
	}
	return out
}

// firstOfType 返回第一个 type == typ 的帧对象，找不到即 Fatal。
func firstOfType(t *testing.T, objs []map[string]any, typ string) map[string]any {
	t.Helper()
	for _, o := range objs {
		if s, _ := o["type"].(string); s == typ {
			return o
		}
	}
	t.Fatalf("no %q frame in stream (types: %v)", typ, typesOf(objs))
	return nil
}

func typesOf(objs []map[string]any) []string {
	var out []string
	for _, o := range objs {
		s, _ := o["type"].(string)
		out = append(out, s)
	}
	return out
}

func nested(t *testing.T, m map[string]any, path ...string) any {
	t.Helper()
	var cur any = m
	for _, p := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("path %v: %v is not an object at %q", path, cur, p)
		}
		cur, ok = mm[p]
		if !ok {
			t.Fatalf("path %v: key %q missing", path, p)
		}
	}
	return cur
}

func numField(t *testing.T, m map[string]any, path ...string) int {
	t.Helper()
	v := nested(t, m, path...)
	f, ok := v.(float64)
	if !ok {
		t.Fatalf("path %v: %v is not a number", path, v)
	}
	return int(f)
}

// arrElem 取数组字段的第 i 个对象元素（OpenAI 的 choices 就是数组）。
func arrElem(t *testing.T, m map[string]any, key string, i int) map[string]any {
	t.Helper()
	arr, ok := m[key].([]any)
	if !ok {
		t.Fatalf("%q is not an array (got %T)", key, m[key])
	}
	if i >= len(arr) {
		t.Fatalf("%q has %d elements, want index %d", key, len(arr), i)
	}
	el, ok := arr[i].(map[string]any)
	if !ok {
		t.Fatalf("%q[%d] is not an object", key, i)
	}
	return el
}

func strField(t *testing.T, m map[string]any, path ...string) string {
	t.Helper()
	v := nested(t, m, path...)
	s, ok := v.(string)
	if !ok {
		t.Fatalf("path %v: %v is not a string", path, v)
	}
	return s
}

// hasPath 报告嵌套键是否存在（用于断言「该省略的字段确实省略了」）。
func hasPath(m map[string]any, path ...string) bool {
	var cur any = m
	for _, p := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return false
		}
		cur, ok = mm[p]
		if !ok {
			return false
		}
	}
	return true
}

// ---- 断言工具 ----

// assertResponsesUsage 校验 response.completed 里的 usage 四项。
// cached/reasoning 为 0 时必须整块省略（OpenAI 的「零值不出现」约定）。
func assertResponsesUsage(t *testing.T, completed map[string]any, in, out, cached, reasoning int) {
	t.Helper()
	resp, ok := nested(t, completed, "response").(map[string]any)
	if !ok {
		t.Fatalf("response.completed.response is not an object")
	}
	if got := numField(t, resp, "usage", "input_tokens"); got != in {
		t.Errorf("usage.input_tokens=%d want %d (提示 token 在流式链路上丢失)", got, in)
	}
	if got := numField(t, resp, "usage", "output_tokens"); got != out {
		t.Errorf("usage.output_tokens=%d want %d", got, out)
	}
	if got := numField(t, resp, "usage", "total_tokens"); got != in+out {
		t.Errorf("usage.total_tokens=%d want %d", got, in+out)
	}
	if cached > 0 {
		if got := numField(t, resp, "usage", "input_tokens_details", "cached_tokens"); got != cached {
			t.Errorf("usage.input_tokens_details.cached_tokens=%d want %d", got, cached)
		}
	} else if hasPath(resp, "usage", "input_tokens_details") {
		t.Errorf("usage.input_tokens_details should be omitted when there is no cache hit")
	}
	if reasoning > 0 {
		if got := numField(t, resp, "usage", "output_tokens_details", "reasoning_tokens"); got != reasoning {
			t.Errorf("usage.output_tokens_details.reasoning_tokens=%d want %d", got, reasoning)
		}
	} else if hasPath(resp, "usage", "output_tokens_details") {
		t.Errorf("usage.output_tokens_details should be omitted when there is no reasoning")
	}
}

// assertAnthropicIndexDiscipline 校验 Anthropic 出站流的块索引纪律：
// 每个 delta/stop 的 index 都必须先出现过同 index 的 content_block_start，
// 且 start 的 index 从 0 起连续（否则严格客户端会 abort）。
func assertAnthropicIndexDiscipline(t *testing.T, objs []map[string]any) {
	t.Helper()
	started := map[int]bool{}
	var startOrder []int
	for _, o := range objs {
		switch strField(t, o, "type") {
		case "content_block_start":
			idx := numField(t, o, "index")
			if started[idx] {
				t.Errorf("content_block_start index %d emitted twice", idx)
			}
			started[idx] = true
			startOrder = append(startOrder, idx)
		case "content_block_delta", "content_block_stop":
			idx := numField(t, o, "index")
			if !started[idx] {
				t.Errorf("%s on index %d before its content_block_start (Anthropic SDK aborts on this)",
					strField(t, o, "type"), idx)
			}
		}
	}
	for i, idx := range startOrder {
		if idx != i {
			t.Errorf("content_block_start sequence %v is not 0-based contiguous", startOrder)
			break
		}
	}
}

// assertAnthropicEventLines 校验 event: 行与 data 里的 type 一致。
func assertAnthropicEventLines(t *testing.T, frames []string) {
	t.Helper()
	for _, f := range frames {
		var eventLine string
		for _, line := range strings.Split(f, "\n") {
			if s, ok := strings.CutPrefix(line, "event: "); ok {
				eventLine = s
			}
		}
		if eventLine == "" {
			t.Errorf("anthropic frame has no event: line: %q", f)
			continue
		}
		var payload struct {
			Type string `json:"type"`
		}
		for _, p := range framePayloads([]string{f}) {
			if err := json.Unmarshal([]byte(p), &payload); err != nil {
				t.Fatalf("anthropic payload %q: %v", p, err)
			}
		}
		if payload.Type != eventLine {
			t.Errorf("event: %s != data.type %s", eventLine, payload.Type)
		}
	}
}

// ---- 上游 payload 构造 ----

func openAIUpstreamText() []string {
	return []string{
		`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"Hi"}}]}`,
		`{"id":"c1","choices":[{"index":0,"delta":{"content":" there"}}]}`,
		`{"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`{"id":"c1","choices":[],"usage":{"prompt_tokens":11,"completion_tokens":4,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":3},"completion_tokens_details":{"reasoning_tokens":1}}}`,
		`[DONE]`,
	}
}

func anthropicUpstreamText() []string {
	return []string{
		`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-3-5","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":1,"cache_read_input_tokens":7,"cache_creation_input_tokens":2}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" there"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":2}}`,
		`{"type":"message_stop"}`,
	}
}

func responsesUpstreamText() []string {
	return []string{
		`{"type":"response.created","response":{"id":"resp_1","object":"response","created_at":1,"status":"in_progress","model":"m","output":[]}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg_1","type":"message","status":"in_progress","role":"assistant","content":[]}}`,
		`{"type":"response.content_part.added","item_id":"msg_1","output_index":0,"content_index":0,"part":{"type":"output_text","text":"","annotations":[]}}`,
		`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"Hi"}`,
		`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":" there"}`,
		`{"type":"response.output_text.done","item_id":"msg_1","output_index":0,"content_index":0,"text":"Hi there"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"Hi there","annotations":[]}]}}`,
		`{"type":"response.completed","response":{"id":"resp_1","object":"response","created_at":1,"status":"completed","model":"m","output":[{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"Hi there","annotations":[]}]}],"usage":{"input_tokens":10,"output_tokens":3,"total_tokens":13,"input_tokens_details":{"cached_tokens":7},"output_tokens_details":{"reasoning_tokens":1}}}}`,
	}
}

// openAIUpstreamToolOnly 是「纯工具调用」轮次：没有任何文本 delta，因此
// OpenAI 解码器保留的 IR index 0（文本块）从未打开，首个 tool_use 落在 index 1。
func openAIUpstreamToolOnly() []string {
	return []string{
		`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`,
		`{"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":""}}]}}]}`,
		`{"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":\"SF\"}"}}]}}]}`,
		`{"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`{"id":"c1","choices":[],"usage":{"prompt_tokens":9,"completion_tokens":5,"total_tokens":14}}`,
		`[DONE]`,
	}
}

// ---- OpenAI 上游 ----

// TestCrossStream_OpenAIUpstream_ResponsesInbound 覆盖 OpenAI 上游 →
// Responses 客户端。usage 只在最后的 usage-only chunk 里出现（经 message_delta
// 进入 IR），Responses 编码器必须把它读进 response.completed.usage。
func TestCrossStream_OpenAIUpstream_ResponsesInbound(t *testing.T) {
	up := upstreamStream{format: "openai", payloads: openAIUpstreamText()}
	frames := encodeTo(t, "responses", up.decode(t))
	objs := jsonPayloads(t, frames)

	var text string
	for _, o := range allOfType(objs, "response.output_text.delta") {
		text += strField(t, o, "delta")
	}
	if text != "Hi there" {
		t.Errorf("text=%q want %q", text, "Hi there")
	}

	if len(objs) == 0 {
		t.Fatal("no frames emitted")
	}
	last := objs[len(objs)-1]
	if typ := strField(t, last, "type"); typ != "response.completed" {
		t.Fatalf("last frame is %q, want response.completed (types: %v)", typ, typesOf(objs))
	}
	if st := strField(t, last, "response", "status"); st != "completed" {
		t.Errorf("status=%q want completed", st)
	}
	assertResponsesUsage(t, last, 11, 4, 3, 1)

	// 文本 item 必须先 added 后 done，且 delta 落在已 added 的 output_index 上。
	added := map[int]bool{}
	for _, o := range objs {
		switch strField(t, o, "type") {
		case "response.output_item.added":
			added[numField(t, o, "output_index")] = true
		case "response.output_text.delta":
			if idx := numField(t, o, "output_index"); !added[idx] {
				t.Errorf("output_text.delta on output_index %d before its output_item.added", idx)
			}
		}
	}
	if _, ok := added[0]; !ok {
		t.Errorf("no output_item.added for output_index 0: %v", typesOf(objs))
	}
}

// TestCrossStream_AnthropicUpstream_ResponsesInbound 覆盖 Anthropic 上游 →
// Responses 客户端。Anthropic 的 input token 在 message_start，output token 在
// message_delta，两者都必须出现在 completed.usage 里。
func TestCrossStream_AnthropicUpstream_ResponsesInbound(t *testing.T) {
	up := upstreamStream{format: "anthropic", payloads: anthropicUpstreamText()}
	frames := encodeTo(t, "responses", up.decode(t))
	objs := jsonPayloads(t, frames)

	var text string
	for _, o := range allOfType(objs, "response.output_text.delta") {
		text += strField(t, o, "delta")
	}
	if text != "Hi there" {
		t.Errorf("text=%q want %q", text, "Hi there")
	}
	completed := firstOfType(t, objs, "response.completed")
	assertResponsesUsage(t, completed, 10, 2, 7, 0)
}

// TestCrossStream_ResponsesUpstream_OpenAIInbound 覆盖 Responses 上游 →
// OpenAI 客户端：usage（含 cached/reasoning）必须落到最后一个 chunk 并补 [DONE]。
func TestCrossStream_ResponsesUpstream_OpenAIInbound(t *testing.T) {
	up := upstreamStream{format: "responses", payloads: responsesUpstreamText()}
	frames := encodeTo(t, "openai", up.decode(t))
	objs := jsonPayloads(t, frames)

	var text string
	var usageChunk map[string]any
	for _, o := range objs {
		if choices, ok := o["choices"].([]any); ok && len(choices) > 0 {
			if delta, ok := arrElem(t, o, "choices", 0)["delta"].(map[string]any); ok {
				if c, ok := delta["content"].(string); ok {
					text += c
				}
			}
		}
		if _, ok := o["usage"]; ok {
			usageChunk = o
		}
	}
	if text != "Hi there" {
		t.Errorf("text=%q want %q", text, "Hi there")
	}
	if usageChunk == nil {
		t.Fatalf("no chunk carried usage (types: %v)", typesOf(objs))
	}
	if got := numField(t, usageChunk, "usage", "prompt_tokens"); got != 10 {
		t.Errorf("prompt_tokens=%d want 10", got)
	}
	if got := numField(t, usageChunk, "usage", "completion_tokens"); got != 3 {
		t.Errorf("completion_tokens=%d want 3", got)
	}
	if got := numField(t, usageChunk, "usage", "total_tokens"); got != 13 {
		t.Errorf("total_tokens=%d want 13", got)
	}
	if got := numField(t, usageChunk, "usage", "prompt_tokens_details", "cached_tokens"); got != 7 {
		t.Errorf("cached_tokens=%d want 7", got)
	}
	if got := numField(t, usageChunk, "usage", "prompt_cache_miss_tokens"); got != 3 {
		t.Errorf("prompt_cache_miss_tokens=%d want 3 (10 input - 7 cached)", got)
	}
	if got := numField(t, usageChunk, "usage", "completion_tokens_details", "reasoning_tokens"); got != 1 {
		t.Errorf("reasoning_tokens=%d want 1", got)
	}
	if fr, ok := arrElem(t, usageChunk, "choices", 0)["finish_reason"].(string); !ok || fr != "stop" {
		t.Errorf("finish_reason=%v want stop", arrElem(t, usageChunk, "choices", 0)["finish_reason"])
	}
	if !hasDone(frames) {
		t.Errorf("missing [DONE] terminator: %v", framePayloads(frames))
	}
	if last := framePayloads(frames)[len(framePayloads(frames))-1]; last != "[DONE]" {
		t.Errorf("last payload=%q want [DONE]", last)
	}
}

// TestCrossStream_ResponsesUpstream_AnthropicInbound 覆盖 Responses 上游 →
// Anthropic 客户端：索引纪律、收尾帧、以及 message_delta 上的 usage。
// 注意 message_start 的 usage 只能是 0 —— Responses 上游要到 completed 才报
// 提示 token，IR 在 message_start 时无从得知；但 message_delta 必须带上
// input_tokens，否则 Anthropic 客户端永远看不到输入计费。
func TestCrossStream_ResponsesUpstream_AnthropicInbound(t *testing.T) {
	up := upstreamStream{format: "responses", payloads: responsesUpstreamText()}
	frames := encodeTo(t, "anthropic", up.decode(t))
	objs := jsonPayloads(t, frames)

	assertAnthropicEventLines(t, frames)
	assertAnthropicIndexDiscipline(t, objs)

	first := objs[0]
	if typ := strField(t, first, "type"); typ != "message_start" {
		t.Fatalf("first frame=%q want message_start", typ)
	}
	last := objs[len(objs)-1]
	if typ := strField(t, last, "type"); typ != "message_stop" {
		t.Fatalf("last frame=%q want message_stop (types: %v)", typ, typesOf(objs))
	}
	deltas := allOfType(objs, "message_delta")
	if len(deltas) != 1 {
		t.Fatalf("message_delta count=%d want 1", len(deltas))
	}
	if sr := strField(t, deltas[0], "delta", "stop_reason"); sr != "end_turn" {
		t.Errorf("stop_reason=%q want end_turn", sr)
	}
	if got := numField(t, deltas[0], "usage", "output_tokens"); got != 3 {
		t.Errorf("message_delta usage.output_tokens=%d want 3", got)
	}
	if got := numField(t, deltas[0], "usage", "input_tokens"); got != 10 {
		t.Errorf("message_delta usage.input_tokens=%d want 10", got)
	}
	if got := numField(t, deltas[0], "usage", "cache_read_input_tokens"); got != 7 {
		t.Errorf("message_delta usage.cache_read_input_tokens=%d want 7", got)
	}
}

// ---- Anthropic 上游 ----

// TestCrossStream_AnthropicUpstream_OpenAIInbound 覆盖 Anthropic 上游 →
// OpenAI 客户端：缓存命中/未命中拆分必须落到 usage chunk，且以 [DONE] 收尾。
func TestCrossStream_AnthropicUpstream_OpenAIInbound(t *testing.T) {
	up := upstreamStream{format: "anthropic", payloads: anthropicUpstreamText()}
	frames := encodeTo(t, "openai", up.decode(t))
	objs := jsonPayloads(t, frames)

	var usageChunk map[string]any
	for _, o := range objs {
		if _, ok := o["usage"]; ok {
			usageChunk = o
		}
	}
	if usageChunk == nil {
		t.Fatalf("no chunk carried usage (types: %v)", typesOf(objs))
	}
	// Anthropic 上游的 message_delta 只带 output_tokens；input / cache token 在
	// message_start，由 upstream.Client 在透传前搬到 message_delta 上
	// （internal/upstream/client.go）。所以这一层只断言 output token，完整
	// usage 链路由下面 hand-built IR 的编码器契约测试守住。
	if got := numField(t, usageChunk, "usage", "completion_tokens"); got != 2 {
		t.Errorf("completion_tokens=%d want 2", got)
	}
	if fr, ok := arrElem(t, usageChunk, "choices", 0)["finish_reason"].(string); !ok || fr != "stop" {
		t.Errorf("finish_reason=%v want stop", arrElem(t, usageChunk, "choices", 0)["finish_reason"])
	}
	if !hasDone(frames) {
		t.Errorf("missing [DONE] terminator")
	}
}

// TestCrossStream_AnthropicUpstream_ResponsesInbound_NoOutputTextWhenThinkingOnly
// 是「上游 thinking 块」的回归位：responses 编码器必须发 reasoning item 而不是
// 文本 item，且不能把 thinking 内容当成 output_text 泄漏出去。
func TestCrossStream_AnthropicUpstream_ResponsesInbound_ThinkingOnly(t *testing.T) {
	up := upstreamStream{format: "anthropic", payloads: []string{
		`{"type":"message_start","message":{"id":"msg_1","model":"claude-3-5","usage":{"input_tokens":4,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"let me think"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":6}}`,
		`{"type":"message_stop"}`,
	}}
	frames := encodeTo(t, "responses", up.decode(t))
	objs := jsonPayloads(t, frames)

	if len(allOfType(objs, "response.output_text.delta")) != 0 {
		t.Errorf("thinking content leaked as output_text: %v", objs)
	}
	completed := firstOfType(t, objs, "response.completed")
	out, ok := nested(t, completed, "response", "output").([]any)
	if !ok || len(out) != 1 {
		t.Fatalf("response.output=%v want exactly one item", nested(t, completed, "response", "output"))
	}
	item, _ := out[0].(map[string]any)
	if typ, _ := item["type"].(string); typ != "reasoning" {
		t.Errorf("output[0].type=%q want reasoning", typ)
	}
	assertResponsesUsage(t, completed, 4, 6, 0, 0)
}

// TestCrossStream_OpenAIUpstream_AnthropicInbound 覆盖 OpenAI 上游 →
// Anthropic 客户端（网关 heat path 之一）。
func TestCrossStream_OpenAIUpstream_AnthropicInbound(t *testing.T) {
	up := upstreamStream{format: "openai", payloads: openAIUpstreamText()}
	frames := encodeTo(t, "anthropic", up.decode(t))
	objs := jsonPayloads(t, frames)

	assertAnthropicEventLines(t, frames)
	assertAnthropicIndexDiscipline(t, objs)

	var text string
	for _, o := range allOfType(objs, "content_block_delta") {
		if d, ok := o["delta"].(map[string]any); ok {
			if s, ok := d["text"].(string); ok {
				text += s
			}
		}
	}
	if text != "Hi there" {
		t.Errorf("text=%q want %q", text, "Hi there")
	}
	deltas := allOfType(objs, "message_delta")
	if len(deltas) != 1 {
		t.Fatalf("message_delta count=%d want 1", len(deltas))
	}
	if got := numField(t, deltas[0], "usage", "input_tokens"); got != 11 {
		t.Errorf("usage.input_tokens=%d want 11", got)
	}
	if got := numField(t, deltas[0], "usage", "output_tokens"); got != 4 {
		t.Errorf("usage.output_tokens=%d want 4", got)
	}
	if got := numField(t, deltas[0], "usage", "cache_read_input_tokens"); got != 3 {
		t.Errorf("usage.cache_read_input_tokens=%d want 3", got)
	}
}

// ---- 块索引纪律（跨全部上游格式） ----

// assertOpenAIUsage 校验 OpenAI 出站 usage chunk 的四项 + 缓存命中/未命中拆分。
func assertOpenAIUsage(t *testing.T, chunk map[string]any, in, out, cached, reasoning int) {
	t.Helper()
	if got := numField(t, chunk, "usage", "prompt_tokens"); got != in {
		t.Errorf("usage.prompt_tokens=%d want %d", got, in)
	}
	if got := numField(t, chunk, "usage", "completion_tokens"); got != out {
		t.Errorf("usage.completion_tokens=%d want %d", got, out)
	}
	if got := numField(t, chunk, "usage", "total_tokens"); got != in+out {
		t.Errorf("usage.total_tokens=%d want %d", got, in+out)
	}
	if cached > 0 {
		if got := numField(t, chunk, "usage", "prompt_tokens_details", "cached_tokens"); got != cached {
			t.Errorf("usage.prompt_tokens_details.cached_tokens=%d want %d", got, cached)
		}
		if got := numField(t, chunk, "usage", "prompt_cache_hit_tokens"); got != cached {
			t.Errorf("usage.prompt_cache_hit_tokens=%d want %d", got, cached)
		}
		if miss := in - cached; miss > 0 {
			if got := numField(t, chunk, "usage", "prompt_cache_miss_tokens"); got != miss {
				t.Errorf("usage.prompt_cache_miss_tokens=%d want %d", got, miss)
			}
		}
	}
	if reasoning > 0 {
		if got := numField(t, chunk, "usage", "completion_tokens_details", "reasoning_tokens"); got != reasoning {
			t.Errorf("usage.completion_tokens_details.reasoning_tokens=%d want %d", got, reasoning)
		}
	}
}

// ---- 编码器 usage 契约（hand-built IR，绕开上游解码差异） ----

// messageDeltaWithUsage 构造真实链路上交给编码器的 message_delta：
// OpenAI 上游把三项 token 放在 usage-only chunk 上，Anthropic 上游由
// upstream.Client 从 message_start 搬到 message_delta 上。
func messageDeltaWithUsage() *translate.StreamEvent {
	return &translate.StreamEvent{
		Type: "message_delta", StopReason: "stop",
		InputTokens: 10, OutputTokens: 3, CacheReadTokens: 7, ReasoningTokens: 1,
	}
}

// TestCrossStream_MessageDeltaUsage_OpenAIInbound 钉住 OpenAI 出站的 usage 契约。
func TestCrossStream_MessageDeltaUsage_OpenAIInbound(t *testing.T) {
	evs := []*translate.StreamEvent{
		{Type: "message_start", MessageID: "c1", Model: "gpt-4o"},
		messageDeltaWithUsage(),
		{Type: "message_stop"},
	}
	frames := encodeTo(t, "openai", evs)
	objs := jsonPayloads(t, frames)
	var usageChunk map[string]any
	for _, o := range objs {
		if _, ok := o["usage"]; ok {
			usageChunk = o
		}
	}
	if usageChunk == nil {
		t.Fatalf("no chunk carried usage: %v", typesOf(objs))
	}
	assertOpenAIUsage(t, usageChunk, 10, 3, 7, 1)
	if !hasDone(frames) {
		t.Errorf("missing [DONE]")
	}
}

// TestCrossStream_MessageDeltaUsage_AnthropicInbound 钉住 Anthropic 出站的
// message_delta usage：输入 token 与缓存命中都必须出现，否则网关的
// Anthropic 客户端永远看不到输入计费。
func TestCrossStream_MessageDeltaUsage_AnthropicInbound(t *testing.T) {
	evs := []*translate.StreamEvent{
		{Type: "message_start", MessageID: "msg_1", Model: "claude-3-5"},
		messageDeltaWithUsage(),
		{Type: "message_stop"},
	}
	objs := jsonPayloads(t, encodeTo(t, "anthropic", evs))
	deltas := allOfType(objs, "message_delta")
	if len(deltas) != 1 {
		t.Fatalf("message_delta count=%d want 1", len(deltas))
	}
	if got := numField(t, deltas[0], "usage", "input_tokens"); got != 10 {
		t.Errorf("usage.input_tokens=%d want 10", got)
	}
	if got := numField(t, deltas[0], "usage", "output_tokens"); got != 3 {
		t.Errorf("usage.output_tokens=%d want 3", got)
	}
	if got := numField(t, deltas[0], "usage", "cache_read_input_tokens"); got != 7 {
		t.Errorf("usage.cache_read_input_tokens=%d want 7", got)
	}
}

// TestCrossStream_MessageDeltaUsage_ResponsesInbound 是回归位：Responses 编码器
// 曾经只在 message_start 读取 input token，而 OpenAI 上游的提示 token 只随
// usage-only chunk（→ message_delta）到达，导致 OpenAI→Responses 的流式
// response.completed.usage.input_tokens 恒为 0（客户端计费显示错误）。
func TestCrossStream_MessageDeltaUsage_ResponsesInbound(t *testing.T) {
	evs := []*translate.StreamEvent{
		{Type: "message_start", MessageID: "resp_1", Model: "gpt-4o"},
		{Type: "content_block_start", Index: 0, Block: &translate.ContentBlock{Type: "text"}},
		{Type: "content_block_delta", Index: 0, Delta: &translate.Delta{Type: "text_delta", Text: "Hi"}},
		{Type: "content_block_stop", Index: 0},
		messageDeltaWithUsage(),
		{Type: "message_stop"},
	}
	completed := firstOfType(t, jsonPayloads(t, encodeTo(t, "responses", evs)), "response.completed")
	assertResponsesUsage(t, completed, 10, 3, 7, 1)
}

// TestCrossStream_ToolOnly_AnthropicInbound_IndexFromZero 是历史上真实出现过
// 的 bug 的回归位：OpenAI 纯工具调用轮次让 IR 的首个块落在 index 1，Anthropic
// 出站必须重排为 0（否则客户端收到首个 content_block_start 就是 index 1）。
func TestCrossStream_ToolOnly_AnthropicInbound_IndexFromZero(t *testing.T) {
	up := upstreamStream{format: "openai", payloads: openAIUpstreamToolOnly()}
	frames := encodeTo(t, "anthropic", up.decode(t))
	objs := jsonPayloads(t, frames)

	assertAnthropicIndexDiscipline(t, objs)
	starts := allOfType(objs, "content_block_start")
	if len(starts) != 1 {
		t.Fatalf("content_block_start count=%d want 1 (types: %v)", len(starts), typesOf(objs))
	}
	if idx := numField(t, starts[0], "index"); idx != 0 {
		t.Errorf("first content_block_start index=%d want 0", idx)
	}
	if got := strField(t, starts[0], "content_block", "id"); got != "call_1" {
		t.Errorf("tool_use id=%q want call_1", got)
	}
	if got := strField(t, starts[0], "content_block", "name"); got != "get_weather" {
		t.Errorf("tool_use name=%q want get_weather", got)
	}
}

// TestCrossStream_ToolOnly_ResponsesInbound_IndexFromZero 是同一个 bug 在
// Responses 出站侧的对偶：Responses 协议里 output_index 是 output 数组的下标，
// 首个 item 必须是 0。Anthropic 编码器有 remapIndex，Responses 编码器也必须有，
// 否则客户端看到「数组里只有一个 item，但它的 output_index 是 1」。
func TestCrossStream_ToolOnly_ResponsesInbound_IndexFromZero(t *testing.T) {
	up := upstreamStream{format: "openai", payloads: openAIUpstreamToolOnly()}
	frames := encodeTo(t, "responses", up.decode(t))
	objs := jsonPayloads(t, frames)

	// 任何带 output_index 的帧都必须是 0：Responses 协议里 output_index 就是
	// response.output 数组的下标，只有一个 item 时它只能是 0。
	seen := map[int]bool{}
	for _, o := range objs {
		if _, ok := o["output_index"]; !ok {
			continue
		}
		idx := numField(t, o, "output_index")
		if idx != 0 {
			t.Errorf("%s output_index=%d want 0 (types: %v)", strField(t, o, "type"), idx, typesOf(objs))
		}
		seen[idx] = true
	}
	if !seen[0] {
		t.Errorf("no frame referenced output_index 0: %v", typesOf(objs))
	}
	completed := firstOfType(t, objs, "response.completed")
	out, _ := nested(t, completed, "response", "output").([]any)
	if len(out) != 1 {
		t.Fatalf("response.output len=%d want 1", len(out))
	}
	item, _ := out[0].(map[string]any)
	if typ, _ := item["type"].(string); typ != "function_call" {
		t.Errorf("output[0].type=%q want function_call", typ)
	}
	if got := numField(t, completed, "response", "usage", "input_tokens"); got != 9 {
		t.Errorf("usage.input_tokens=%d want 9", got)
	}
}

// ---- 收尾帧 ----

// TestCrossStream_TruncatedUpstream_ResponsesInboundStillCompletes 覆盖上游
// 中途断流（没有 message_stop）：gateway 在事件循环结束后调用 Flush，Responses
// 客户端必须仍然拿到 response.completed，否则 SDK 会挂到超时。
func TestCrossStream_TruncatedUpstream_ResponsesInboundStillCompletes(t *testing.T) {
	up := upstreamStream{format: "openai", payloads: []string{
		`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"Hi"}}]}`,
		`{"id":"c1","choices":[{"index":0,"delta":{"content":" there"}}]}`,
		// 上游在这里断开：没有 finish_reason、没有 usage、没有 [DONE]。
	}}
	frames := encodeTo(t, "responses", up.decode(t))
	objs := jsonPayloads(t, frames)

	completed := allOfType(objs, "response.completed")
	if len(completed) != 1 {
		t.Fatalf("response.completed count=%d want exactly 1 (types: %v)", len(completed), typesOf(objs))
	}
	if typ := strField(t, objs[len(objs)-1], "type"); typ != "response.completed" {
		t.Errorf("last frame=%q want response.completed", typ)
	}
	// 断流时上游没报 usage，不能凭空造出 usage 对象。
	if hasPath(completed[0], "response", "usage") {
		t.Errorf("truncated stream must not invent usage: %v", completed[0])
	}
}
