package responses

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/great-magician-01/any-llm/internal/translate"
)

// 单个 SSE data 载荷（不含 "data: " 前缀）
type rawStreamEvent struct {
	Type        string         `json:"type"`
	Response    *rawResponse   `json:"response,omitempty"`
	Item        *rawOutputItem `json:"item,omitempty"`
	ItemID      string         `json:"item_id,omitempty"`
	OutputIndex int            `json:"output_index,omitempty"`
	Delta       string         `json:"delta,omitempty"`
	Arguments   string         `json:"arguments,omitempty"`
}

type StreamDecoder struct {
	started      bool
	msgID        string
	model        string
	nextBlock    int
	slot0Taken   bool
	openItems    map[string]*openItem // item id -> 打开的 IR 块
	inputTokens  int
	outputTokens int
	cacheRead    int
	reasoning    int
	stopReason   string
}

type openItem struct {
	index    int
	kind     string // text | thinking | tool_use
	argsSent bool   // function_call 已收到过 arguments delta
}

func NewStreamDecoder() *StreamDecoder {
	return &StreamDecoder{openItems: map[string]*openItem{}, nextBlock: 1}
}

// Decode 消费一个 SSE data 载荷，产出 0..n 个 IR 事件。
func (d *StreamDecoder) Decode(data []byte) ([]*translate.StreamEvent, error) {
	var ev rawStreamEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		return nil, fmt.Errorf("responses stream decode: %w", err)
	}
	switch ev.Type {
	case "response.created", "response.in_progress":
		return d.ensureStarted(ev.Response), nil

	case "response.output_item.added":
		evs := d.ensureStarted(nil)
		if ev.Item == nil {
			return evs, nil
		}
		item := ev.Item
		switch item.Type {
		case "message":
			idx := d.nextBlockIndex()
			d.openItems[item.ID] = &openItem{index: idx, kind: "text"}
			evs = append(evs, &translate.StreamEvent{Type: "content_block_start", Index: idx, Block: &translate.ContentBlock{Type: "text"}})
		case "reasoning":
			idx := d.nextBlockIndex()
			d.openItems[item.ID] = &openItem{index: idx, kind: "thinking"}
			evs = append(evs, &translate.StreamEvent{Type: "content_block_start", Index: idx, Block: &translate.ContentBlock{Type: "thinking"}})
		case "function_call":
			idx := d.nextBlockIndex()
			st := &openItem{index: idx, kind: "tool_use"}
			d.openItems[item.ID] = st
			evs = append(evs, &translate.StreamEvent{
				Type:  "content_block_start",
				Index: idx,
				Block: &translate.ContentBlock{
					Type: "tool_use",
					ToolUse: &translate.ToolUse{ID: item.CallID, Name: item.Name, Input: json.RawMessage("{}")},
				},
			})
			// 上游把 arguments 直接放在 added 事件里（如 DeepSeek 风格）时立即转发
			if item.Arguments != "" {
				st.argsSent = true
				evs = append(evs, &translate.StreamEvent{
					Type: "content_block_delta", Index: idx,
					Delta: &translate.Delta{Type: "input_json_delta", PartialJSON: item.Arguments},
				})
			}
		}
		return evs, nil

	case "response.output_text.delta":
		st := d.openItems[ev.ItemID]
		if st == nil || st.kind != "text" {
			return nil, nil
		}
		return []*translate.StreamEvent{{
			Type: "content_block_delta", Index: st.index,
			Delta: &translate.Delta{Type: "text_delta", Text: ev.Delta},
		}}, nil

	case "response.function_call_arguments.delta":
		st := d.openItems[ev.ItemID]
		if st == nil || st.kind != "tool_use" {
			return nil, nil
		}
		st.argsSent = true
		return []*translate.StreamEvent{{
			Type: "content_block_delta", Index: st.index,
			Delta: &translate.Delta{Type: "input_json_delta", PartialJSON: ev.Delta},
		}}, nil

	case "response.reasoning_summary_text.delta":
		st := d.openItems[ev.ItemID]
		if st == nil || st.kind != "thinking" {
			return nil, nil
		}
		return []*translate.StreamEvent{{
			Type: "content_block_delta", Index: st.index,
			Delta: &translate.Delta{Type: "thinking_delta", Thinking: ev.Delta},
		}}, nil

	case "response.reasoning_text.delta":
		// 只保留 summary，完整思考文本忽略
		return nil, nil

	case "response.output_item.done":
		// 上游（含 OpenAI 官方）此事件不带顶层 item_id，id 在 item 对象里。
		id := ev.ItemID
		if id == "" && ev.Item != nil {
			id = ev.Item.ID
		}
		st := d.openItems[id]
		if st == nil {
			return nil, nil
		}
		var evs []*translate.StreamEvent
		if st.kind == "tool_use" && !st.argsSent && ev.Item != nil && ev.Item.Arguments != "" {
			evs = append(evs, &translate.StreamEvent{
				Type: "content_block_delta", Index: st.index,
				Delta: &translate.Delta{Type: "input_json_delta", PartialJSON: ev.Item.Arguments},
			})
		}
		evs = append(evs, &translate.StreamEvent{Type: "content_block_stop", Index: st.index})
		delete(d.openItems, id)
		return evs, nil

	case "response.content_part.added", "response.content_part.done",
		"response.output_text.done", "response.function_call_arguments.done",
		"response.reasoning_summary_text.done":
		return nil, nil

	case "response.completed":
		if ev.Response != nil {
			if u := ev.Response.Usage; u != nil {
				d.inputTokens = u.InputTokens
				d.outputTokens = u.OutputTokens
				if u.InputTokensDetails != nil {
					d.cacheRead = u.InputTokensDetails.CachedTokens
				}
				if u.OutputTokensDetails != nil {
					d.reasoning = u.OutputTokensDetails.ReasoningTokens
				}
			}
			d.stopReason = mapStopFromStatus(ev.Response.Status, hasFunctionCall(ev.Response))
		}
		if d.stopReason == "" {
			d.stopReason = "stop"
		}
		return []*translate.StreamEvent{
			{
				Type: "message_delta", StopReason: d.stopReason,
				InputTokens: d.inputTokens, OutputTokens: d.outputTokens,
				CacheReadTokens: d.cacheRead, ReasoningTokens: d.reasoning,
			},
			{Type: "message_stop"},
		}, nil

	case "response.failed", "response.errored", "error":
		return []*translate.StreamEvent{{Type: "error"}}, nil
	}
	return nil, nil
}

func (d *StreamDecoder) ensureStarted(resp *rawResponse) []*translate.StreamEvent {
	if d.started {
		return nil
	}
	d.started = true
	if resp != nil {
		d.msgID = resp.ID
		d.model = resp.Model
	}
	return []*translate.StreamEvent{{Type: "message_start", MessageID: d.msgID, Model: d.model}}
}

// nextBlockIndex 与 openai 解码器一致：第一个块占 index 0，后续递增。
func (d *StreamDecoder) nextBlockIndex() int {
	if !d.slot0Taken {
		d.slot0Taken = true
		return 0
	}
	idx := d.nextBlock
	d.nextBlock++
	return idx
}

// hasFunctionCall 检查响应 output 里是否含 function_call（用于推 tool_calls）。
func hasFunctionCall(r *rawResponse) bool {
	if r == nil {
		return false
	}
	for _, item := range r.Output {
		if item.Type == "function_call" {
			return true
		}
	}
	return false
}

type StreamEncoder struct {
	model      string
	id         string
	created    int64
	started    bool
	completed  bool
	usageIn    int
	usageOut   int
	cacheRead  int
	reasoning  int
	stopReason string
	blockKind  map[int]string // IR block index -> text | thinking | tool_use
	itemIDs    map[int]string // IR block index -> 输出 item id
	textBuf    map[int]string
	toolArgs   map[int]string
	thinkBuf   map[int]string
	toolMeta   map[int]*translate.ToolUse // IR block index -> call_id/name
	items      map[int]map[string]any     // output_index -> 最终 item（completed 用）
	pendingFrames [][]byte                // ensureItemStarted 合成的 added 事件暂存区
}

func NewStreamEncoder(model, id string) *StreamEncoder {
	return &StreamEncoder{
		model:     model,
		id:        id,
		created:   time.Now().Unix(),
		blockKind: map[int]string{},
		itemIDs:   map[int]string{},
		textBuf:   map[int]string{},
		toolArgs:  map[int]string{},
		thinkBuf:  map[int]string{},
		toolMeta:  map[int]*translate.ToolUse{},
		items:     map[int]map[string]any{},
	}
}

func (e *StreamEncoder) Encode(evt *translate.StreamEvent) ([][]byte, error) {
	var frames [][]byte
	if !e.started && evt.Type != "message_start" {
		e.started = true
		frames = append(frames, e.createdFrames()...)
	}
	// 合成帧前置：ensureItemStarted 产生的 added 事件必须先于 delta 输出
	if len(e.pendingFrames) > 0 {
		frames = append(frames, e.pendingFrames...)
		e.pendingFrames = nil
	}
	switch evt.Type {
	case "message_start":
		e.usageIn = evt.InputTokens
		if evt.CacheReadTokens > 0 {
			e.cacheRead = evt.CacheReadTokens
		}
		if e.started {
			return nil, nil
		}
		e.started = true
		return e.createdFrames(), nil

	case "content_block_start":
		if evt.Block == nil {
			return nil, nil
		}
		idx := evt.Index
		switch evt.Block.Type {
		case "text":
			e.blockKind[idx] = "text"
			itemID := "msg_" + randHex(8)
			e.itemIDs[idx] = itemID
			frames = append(frames,
				sseFrame("response.output_item.added", map[string]any{
					"output_index": idx,
					"item": map[string]any{
						"id": itemID, "type": "message", "status": "in_progress", "role": "assistant", "content": []any{},
					},
				}),
				sseFrame("response.content_part.added", map[string]any{
					"item_id": itemID, "output_index": idx, "content_index": 0,
					"part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}},
				}),
			)
		case "thinking":
			e.blockKind[idx] = "thinking"
			itemID := "rs_" + randHex(8)
			e.itemIDs[idx] = itemID
			frames = append(frames, sseFrame("response.output_item.added", map[string]any{
				"output_index": idx,
				"item":         map[string]any{"id": itemID, "type": "reasoning", "summary": []any{}, "content": []any{}},
			}))
		case "tool_use":
			e.blockKind[idx] = "tool_use"
			itemID := "fc_" + randHex(8)
			e.itemIDs[idx] = itemID
			e.toolMeta[idx] = evt.Block.ToolUse
			frames = append(frames, sseFrame("response.output_item.added", map[string]any{
				"output_index": idx,
				"item": map[string]any{
					"id": itemID, "type": "function_call",
					"call_id": evt.Block.ToolUse.ID, "name": evt.Block.ToolUse.Name, "arguments": "",
				},
			}))
			// Anthropic 上游的 start 块自带完整 input：立即转发，避免 arguments 截断
			if input := string(evt.Block.ToolUse.Input); input != "" && input != "{}" {
				e.toolArgs[idx] = input
				frames = append(frames, sseFrame("response.function_call_arguments.delta", map[string]any{
					"item_id": itemID, "output_index": idx, "delta": input,
				}))
			}
		}
		return frames, nil

	case "content_block_delta":
		if evt.Delta == nil {
			return nil, nil
		}
		idx := evt.Index
		switch evt.Delta.Type {
		case "text_delta":
			e.ensureItemStarted(idx, "text")
			e.textBuf[idx] += evt.Delta.Text
			return [][]byte{sseFrame("response.output_text.delta", map[string]any{
				"item_id": e.itemIDs[idx], "output_index": idx, "content_index": 0, "delta": evt.Delta.Text,
			})}, nil
		case "input_json_delta":
			e.ensureItemStarted(idx, "tool_use")
			e.toolArgs[idx] += evt.Delta.PartialJSON
			return [][]byte{sseFrame("response.function_call_arguments.delta", map[string]any{
				"item_id": e.itemIDs[idx], "output_index": idx, "delta": evt.Delta.PartialJSON,
			})}, nil
		case "thinking_delta":
			e.ensureItemStarted(idx, "thinking")
			e.thinkBuf[idx] += evt.Delta.Thinking
			return [][]byte{sseFrame("response.reasoning_summary_text.delta", map[string]any{
				"item_id": e.itemIDs[idx], "output_index": idx, "delta": evt.Delta.Thinking,
			})}, nil
		case "signature_delta":
			// Responses 无签名概念
			return nil, nil
		}
		return nil, nil

	case "content_block_stop":
		idx := evt.Index
		switch e.blockKind[idx] {
		case "text":
			text := e.textBuf[idx]
			itemID := e.itemIDs[idx]
			frames = append(frames,
				sseFrame("response.output_text.done", map[string]any{
					"item_id": itemID, "output_index": idx, "content_index": 0, "text": text,
				}),
				sseFrame("response.content_part.done", map[string]any{
					"item_id": itemID, "output_index": idx, "content_index": 0,
					"part": map[string]any{"type": "output_text", "text": text, "annotations": []any{}},
				}),
			)
			item := map[string]any{
				"type": "message", "id": itemID, "status": "completed", "role": "assistant",
				"content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}},
			}
			e.items[idx] = item
			frames = append(frames, sseFrame("response.output_item.done", map[string]any{"output_index": idx, "item": item}))
		case "thinking":
			itemID := e.itemIDs[idx]
			summary := []any{map[string]any{"type": "summary_text", "text": e.thinkBuf[idx]}}
			frames = append(frames, sseFrame("response.reasoning_summary_text.done", map[string]any{
				"item_id": itemID, "output_index": idx, "summary": summary,
			}))
			item := map[string]any{"type": "reasoning", "id": itemID, "summary": summary, "content": []any{}}
			e.items[idx] = item
			frames = append(frames, sseFrame("response.output_item.done", map[string]any{"output_index": idx, "item": item}))
		case "tool_use":
			itemID := e.itemIDs[idx]
			args := e.toolArgs[idx]
			frames = append(frames, sseFrame("response.function_call_arguments.done", map[string]any{
				"item_id": itemID, "output_index": idx, "arguments": args,
			}))
			tm := e.toolMeta[idx]
			callID, name := "", ""
			if tm != nil {
				callID, name = tm.ID, tm.Name
			}
			item := map[string]any{
				"type": "function_call", "id": itemID, "call_id": callID, "name": name, "arguments": args,
			}
			e.items[idx] = item
			frames = append(frames, sseFrame("response.output_item.done", map[string]any{"output_index": idx, "item": item}))
		}
		return frames, nil

	case "message_delta":
		e.usageOut = evt.OutputTokens
		if evt.CacheReadTokens > 0 {
			e.cacheRead = evt.CacheReadTokens
		}
		if evt.ReasoningTokens > 0 {
			e.reasoning = evt.ReasoningTokens
		}
		if evt.StopReason != "" {
			e.stopReason = evt.StopReason
		}
		return nil, nil

	case "message_stop":
		if e.completed {
			return nil, nil
		}
		e.completed = true
		return e.completedFrames(), nil
	}
	return nil, nil
}

// ensureItemStarted 为缺失 content_block_start 的块补合成 added（+part）事件。
func (e *StreamEncoder) ensureItemStarted(idx int, kind string) {
	if _, ok := e.blockKind[idx]; ok {
		return
	}
	e.blockKind[idx] = kind
	switch kind {
	case "text":
		itemID := "msg_" + randHex(8)
		e.itemIDs[idx] = itemID
		e.pendingFrames = append(e.pendingFrames,
			sseFrame("response.output_item.added", map[string]any{
				"output_index": idx,
				"item": map[string]any{"id": itemID, "type": "message", "status": "in_progress", "role": "assistant", "content": []any{}},
			}),
			sseFrame("response.content_part.added", map[string]any{
				"item_id": itemID, "output_index": idx, "content_index": 0,
				"part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}},
			}),
		)
	case "thinking":
		// 注意：item id 前缀必须与 kind 匹配（rs_/fc_/msg_），后续 delta 帧
		// 用 e.itemIDs[idx] 引用同一个 id。
		itemID := "rs_" + randHex(8)
		e.itemIDs[idx] = itemID
		e.pendingFrames = append(e.pendingFrames, sseFrame("response.output_item.added", map[string]any{
			"output_index": idx,
			"item":         map[string]any{"id": itemID, "type": "reasoning", "summary": []any{}, "content": []any{}},
		}))
	case "tool_use":
		itemID := "fc_" + randHex(8)
		e.itemIDs[idx] = itemID
		e.pendingFrames = append(e.pendingFrames, sseFrame("response.output_item.added", map[string]any{
			"output_index": idx,
			"item":         map[string]any{"id": itemID, "type": "function_call", "call_id": "", "name": "", "arguments": ""},
		}))
	}
}

// pendingFrames 是合成事件暂存区，Encode 返回时统一前置。
func (e *StreamEncoder) createdFrames() [][]byte {
	resp := map[string]any{
		"id": e.id, "object": "response", "created_at": e.created,
		"status": "in_progress", "model": e.model, "output": []any{},
	}
	return [][]byte{
		sseFrame("response.created", map[string]any{"response": resp}),
		sseFrame("response.in_progress", map[string]any{"response": resp}),
	}
}

func (e *StreamEncoder) completedFrames() [][]byte {
	status := "completed"
	if e.stopReason == "max_tokens" {
		status = "incomplete"
	}
	output := make([]any, 0, len(e.items))
	var idxs []int
	for idx := range e.items {
		idxs = append(idxs, idx)
	}
	sort.Ints(idxs)
	for _, idx := range idxs {
		output = append(output, e.items[idx])
	}
	resp := map[string]any{
		"id": e.id, "object": "response", "created_at": e.created,
		"status": status, "model": e.model, "output": output,
	}
	if status == "incomplete" {
		resp["incomplete_details"] = map[string]any{"reason": "max_output_tokens"}
	}
	if e.usageIn > 0 || e.usageOut > 0 {
		usage := map[string]any{
			"input_tokens": e.usageIn, "output_tokens": e.usageOut,
			"total_tokens": e.usageIn + e.usageOut,
		}
		if e.cacheRead > 0 {
			usage["input_tokens_details"] = map[string]any{"cached_tokens": e.cacheRead}
		}
		if e.reasoning > 0 {
			usage["output_tokens_details"] = map[string]any{"reasoning_tokens": e.reasoning}
		}
		resp["usage"] = usage
	}
	return [][]byte{sseFrame("response.completed", map[string]any{"response": resp})}
}

// Flush 由网关在事件循环结束后调用。幂等。
func (e *StreamEncoder) Flush() [][]byte {
	if !e.started {
		e.started = true
		frames := e.createdFrames()
		e.completed = true
		return append(frames, e.completedFrames()...)
	}
	if e.completed {
		return nil
	}
	e.completed = true
	return e.completedFrames()
}

// Content 返回累积的模型输出（顺序 = IR block 顺序），网关存会话用。
func (e *StreamEncoder) Content() []translate.ContentBlock {
	var idxs []int
	for idx := range e.blockKind {
		idxs = append(idxs, idx)
	}
	sort.Ints(idxs)
	var out []translate.ContentBlock
	for _, idx := range idxs {
		switch e.blockKind[idx] {
		case "text":
			out = append(out, translate.ContentBlock{Type: "text", Text: e.textBuf[idx]})
		case "thinking":
			out = append(out, translate.ContentBlock{Type: "thinking", Thinking: e.thinkBuf[idx], Signature: e.itemIDs[idx]})
		case "tool_use":
			tm := e.toolMeta[idx]
			callID, name := "", ""
			if tm != nil {
				callID, name = tm.ID, tm.Name
			}
			out = append(out, translate.ContentBlock{Type: "tool_use", ToolUse: &translate.ToolUse{
				ID: callID, Name: name, Input: json.RawMessage(e.toolArgs[idx]),
			}})
		}
	}
	return out
}

// sseFrame 组装 Responses SSE 帧。type 必须进 data 载荷（真实 OpenAI 帧与
// StreamDecoder 都从 data 的 type 字段解析）；event: 行仅作可读提示。
func sseFrame(evType string, payload map[string]any) []byte {
	p := map[string]any{"type": evType}
	for k, v := range payload {
		p[k] = v
	}
	b, _ := json.Marshal(p)
	return []byte("event: " + evType + "\ndata: " + string(b) + "\n\n")
}
