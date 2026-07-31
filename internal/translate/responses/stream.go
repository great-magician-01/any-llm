package responses

import (
	"encoding/json"
	"fmt"

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
