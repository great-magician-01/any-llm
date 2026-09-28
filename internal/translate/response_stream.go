package translate

import "encoding/json"

// ResponseStreamEvents 把一条完整的非流式响应展开成与流式解码器同构的 IR 事件
// 序列：message_start → 每个 content block 的 start/delta/stop → message_delta
// （带 usage 与 stop_reason）→ message_stop。
//
// 用途：上游无视 stream:true、直接以非流式 JSON 应答时，网关必须给客户端一个
// **合法的 SSE 流**而不是裸 JSON（严格 SDK 只会认 data:/event: 帧，裸 JSON 会被
// 当成无法解析的一行丢掉，客户端最终什么也拿不到）。展开后交给各格式的流编码器，
// 三种出站格式都能拿到带真实内容与 usage 的完整流。
func ResponseStreamEvents(r *Response) []*StreamEvent {
	if r == nil {
		return nil
	}
	evs := []*StreamEvent{{
		Type:                "message_start",
		MessageID:           r.ID,
		Model:               r.Model,
		InputTokens:         r.Usage.InputTokens,
		CacheReadTokens:     r.Usage.CacheReadTokens,
		CacheCreationTokens: r.Usage.CacheCreationTokens,
	}}

	for i, b := range r.Content {
		block := b
		evs = append(evs, &StreamEvent{Type: "content_block_start", Index: i, Block: &block})

		switch block.Type {
		case "text":
			if block.Text != "" {
				evs = append(evs, &StreamEvent{Type: "content_block_delta", Index: i,
					Delta: &Delta{Type: "text_delta", Text: block.Text}})
			}
		case "thinking":
			if block.Thinking != "" {
				evs = append(evs, &StreamEvent{Type: "content_block_delta", Index: i,
					Delta: &Delta{Type: "thinking_delta", Thinking: block.Thinking}})
			}
			if block.Signature != "" {
				evs = append(evs, &StreamEvent{Type: "content_block_delta", Index: i,
					Delta: &Delta{Type: "signature_delta", Signature: block.Signature}})
			}
		case "tool_use":
			if block.ToolUse != nil {
				if in := string(block.ToolUse.Input); in != "" && in != "null" {
					evs = append(evs, &StreamEvent{Type: "content_block_delta", Index: i,
						Delta: &Delta{Type: "input_json_delta", PartialJSON: in}})
				}
			}
		case "image":
			// 图片没有 delta 形态：start 帧里的 block 已带完整 source/url。
		}

		evs = append(evs, &StreamEvent{Type: "content_block_stop", Index: i})
	}

	evs = append(evs, &StreamEvent{
		Type:                "message_delta",
		StopReason:          r.StopReason,
		InputTokens:         r.Usage.InputTokens,
		OutputTokens:        r.Usage.OutputTokens,
		CacheReadTokens:     r.Usage.CacheReadTokens,
		CacheCreationTokens: r.Usage.CacheCreationTokens,
		ReasoningTokens:     r.Usage.ReasoningTokens,
	})
	// Same-format pass-through: carry fields like `safeguard_results` from
	// the non-stream response into the synthesized message_delta.
	if len(r.Extra) > 0 {
		if sr, ok := r.Extra["safeguard_results"]; ok {
			if raw, ok := sr.(json.RawMessage); ok {
				evs[len(evs)-1].DeltaExtras = map[string]json.RawMessage{"safeguard_results": raw}
			}
		}
	}
	evs = append(evs, &StreamEvent{Type: "message_stop"})
	return evs
}
