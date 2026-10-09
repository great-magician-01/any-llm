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
		// start 帧只带块头（类型，tool_use 的 id/name），正文一律走 delta——与
		// 真实流式上游的事件形态一致。若 start 帧带上全量内容，累积式客户端会拿
		// 到两份：Anthropic SDK 以 start 帧的 text/input 为初值再追加 delta（文本
		// 翻倍），Responses 编码器把 start 的 input 与随后的 input_json_delta 都当
		// 参数片段（工具参数被拼成非法 JSON）。image / redacted_thinking / hosted
		// 块没有 delta 形态，完整内容留在 start 帧。
		evs = append(evs, &StreamEvent{Type: "content_block_start", Index: i, Block: streamStartBlock(&block)})

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
		if sr, ok := r.Extra[KeySafeguardResults]; ok {
			if raw, ok := sr.(json.RawMessage); ok {
				evs[len(evs)-1].DeltaExtras = map[string]json.RawMessage{KeySafeguardResults: raw}
			}
		}
	}
	evs = append(evs, &StreamEvent{Type: "message_stop"})
	return evs
}

// streamStartBlock 返回 content_block_start 帧携带的块：text/thinking 清空正文
// （正文随 delta 到达），tool_use 只带 id/name 与空 input（与真实流式上游一致，
// Anthropic 的 tool_use start 帧 input 恒为 {}）；其余块类型（image、
// redacted_thinking、hosted server 块）没有 delta 形态，整块携带。
func streamStartBlock(b *ContentBlock) *ContentBlock {
	switch b.Type {
	case "text":
		return &ContentBlock{Type: "text"}
	case "thinking":
		return &ContentBlock{Type: "thinking"}
	case "tool_use":
		if b.ToolUse == nil {
			return b
		}
		return &ContentBlock{Type: "tool_use", ToolUse: &ToolUse{
			ID: b.ToolUse.ID, Name: b.ToolUse.Name, Input: json.RawMessage("{}"),
		}}
	}
	return b
}
