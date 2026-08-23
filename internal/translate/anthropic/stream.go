package anthropic

import (
	"encoding/json"
	"fmt"

	"github.com/great-magician-01/any-llm/internal/translate"
)

func DecodeStreamEvent(data []byte) (*translate.StreamEvent, error) {
	var raw rawStreamEvent
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("anthropic stream decode: %w", err)
	}
	switch raw.Type {
	case "ping":
		return &translate.StreamEvent{Type: "ping"}, nil
	case "":
		return nil, nil
	case "message_start":
		evt := &translate.StreamEvent{Type: "message_start"}
		var msg struct {
			ID    string    `json:"id"`
			Model string    `json:"model"`
			Usage *rawUsage `json:"usage"`
		}
		_ = json.Unmarshal(raw.Message, &msg)
		evt.MessageID = msg.ID
		evt.Model = msg.Model
		if msg.Usage != nil {
			evt.InputTokens = msg.Usage.InputTokens
			evt.OutputTokens = msg.Usage.OutputTokens
			evt.CacheReadTokens = msg.Usage.CacheReadInputTokens
			evt.CacheCreationTokens = msg.Usage.CacheCreationInputTokens
		}
		evt.RawMessage = raw.Message
		return evt, nil
	case "content_block_start":
		evt := &translate.StreamEvent{Type: "content_block_start", Index: raw.Index}
		b, err := decodeStreamContentBlock(raw.ContentBlock)
		if err != nil {
			return nil, err
		}
		evt.Block = b
		return evt, nil
	case "content_block_delta":
		evt := &translate.StreamEvent{Type: "content_block_delta", Index: raw.Index}
		var d struct {
			Type        string `json:"type"`
			Text        string `json:"text,omitempty"`
			PartialJSON string `json:"partial_json,omitempty"`
			Thinking    string `json:"thinking,omitempty"`
			Signature   string `json:"signature,omitempty"`
		}
		_ = json.Unmarshal(raw.Delta, &d)
		evt.Delta = &translate.Delta{
			Type:        d.Type,
			Text:        d.Text,
			PartialJSON: d.PartialJSON,
			Thinking:    d.Thinking,
			Signature:   d.Signature,
		}
		return evt, nil
	case "content_block_stop":
		return &translate.StreamEvent{Type: "content_block_stop", Index: raw.Index}, nil
	case "message_delta":
		evt := &translate.StreamEvent{Type: "message_delta"}
		var d struct {
			StopReason   string  `json:"stop_reason"`
			StopSequence *string `json:"stop_sequence"`
		}
		_ = json.Unmarshal(raw.Delta, &d)
		evt.StopReason = d.StopReason
		if len(raw.Usage) > 0 {
			var u rawUsage
			_ = json.Unmarshal(raw.Usage, &u)
			evt.OutputTokens = u.OutputTokens
			evt.CacheReadTokens = u.CacheReadInputTokens
			evt.CacheCreationTokens = u.CacheCreationInputTokens
			evt.RawUsage = raw.Usage
		}
		return evt, nil
	case "message_stop":
		return &translate.StreamEvent{Type: "message_stop"}, nil
	}
	return nil, nil
}

func EncodeStreamEvent(evt *translate.StreamEvent) ([]byte, error) {
	payload := map[string]any{"type": evt.Type}
	switch evt.Type {
	case "message_start":
		if len(evt.RawMessage) > 0 {
			payload["message"] = json.RawMessage(evt.RawMessage)
		} else {
			msg := map[string]any{
				"id":            evt.MessageID,
				"type":          "message",
				"role":          "assistant",
				"model":         evt.Model,
				"content":       []any{},
				"stop_reason":   nil,
				"stop_sequence": nil,
				"usage": map[string]any{
					"input_tokens":                evt.InputTokens,
					"output_tokens":               evt.OutputTokens,
					"cache_creation_input_tokens": evt.CacheCreationTokens,
					"cache_read_input_tokens":     evt.CacheReadTokens,
				},
			}
			payload["message"] = msg
		}
	case "content_block_start":
		payload["index"] = evt.Index
		if evt.Block != nil {
			payload["content_block"] = blockToRaw(*evt.Block)
		}
	case "content_block_delta":
		payload["index"] = evt.Index
		if evt.Delta != nil {
			d := map[string]any{"type": evt.Delta.Type}
			switch evt.Delta.Type {
			case "text_delta":
				d["text"] = evt.Delta.Text
			case "input_json_delta":
				d["partial_json"] = evt.Delta.PartialJSON
			case "thinking_delta":
				d["thinking"] = evt.Delta.Thinking
			case "signature_delta":
				d["signature"] = evt.Delta.Signature
			default:
				if evt.Delta.Text != "" {
					d["text"] = evt.Delta.Text
				}
				if evt.Delta.PartialJSON != "" {
					d["partial_json"] = evt.Delta.PartialJSON
				}
				if evt.Delta.Thinking != "" {
					d["thinking"] = evt.Delta.Thinking
				}
				if evt.Delta.Signature != "" {
					d["signature"] = evt.Delta.Signature
				}
			}
			payload["delta"] = d
		}
	case "content_block_stop":
		payload["index"] = evt.Index
	case "message_delta":
		d := map[string]any{
			"stop_reason":   mapStopReasonToAnthropic(evt.StopReason),
			"stop_sequence": nil,
		}
		payload["delta"] = d
		if len(evt.RawUsage) > 0 {
			payload["usage"] = json.RawMessage(evt.RawUsage)
		} else if evt.OutputTokens > 0 || evt.InputTokens > 0 ||
			evt.CacheReadTokens > 0 || evt.CacheCreationTokens > 0 {
			usage := map[string]any{"output_tokens": evt.OutputTokens}
			if evt.InputTokens > 0 {
				usage["input_tokens"] = evt.InputTokens
			}
			if evt.CacheReadTokens > 0 {
				usage["cache_read_input_tokens"] = evt.CacheReadTokens
			}
			if evt.CacheCreationTokens > 0 {
				usage["cache_creation_input_tokens"] = evt.CacheCreationTokens
			}
			payload["usage"] = usage
		}
	case "message_stop":
		// no extra fields
	case "ping":
		// no extra fields; keepalive event
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("anthropic stream encode: %w", err)
	}
	return []byte("event: " + evt.Type + "\ndata: " + string(b) + "\n\n"), nil
}

// StreamEncoder is a stateful encoder that rewrites content_block indices to
// a 0-based, contiguous sequence, as required by the Anthropic streaming
// spec. The raw upstream (or the IR produced by an OpenAI upstream) may emit
// block indices that do not start at 0 — e.g. an OpenAI-only-tool-call turn
// produces its first tool_use block at IR index 1 because the text block at
// index 0 was never opened. Forwarding such indices verbatim yields a stream
// whose first content_block_start has index 1 (no index 0), which deviates
// from the spec and confuses strict clients.
type StreamEncoder struct {
	indexRemap map[int]int
	nextIdx    int
}

func NewStreamEncoder() *StreamEncoder {
	return &StreamEncoder{indexRemap: map[int]int{}}
}

// remapIndex maps an IR block index to the next contiguous 0-based output
// index, allocating a new slot on first sight.
func (e *StreamEncoder) remapIndex(irIdx int) int {
	if out, ok := e.indexRemap[irIdx]; ok {
		return out
	}
	out := e.nextIdx
	e.indexRemap[irIdx] = out
	e.nextIdx++
	return out
}

// Encode translates one IR stream event into a single Anthropic SSE frame
// (single-element slice, matching the openai.StreamEncoder interface so the
// gateway can use a uniform encoder variable). It does not mutate evt.
func (e *StreamEncoder) Encode(evt *translate.StreamEvent) ([][]byte, error) {
	out := *evt // shallow copy; Block/Delta pointers are read-only downstream
	switch out.Type {
	case "content_block_start", "content_block_delta", "content_block_stop":
		out.Index = e.remapIndex(out.Index)
	}
	b, err := EncodeStreamEvent(&out)
	if err != nil {
		return nil, err
	}
	return [][]byte{b}, nil
}

func blockToRaw(b translate.ContentBlock) map[string]any {
	switch b.Type {
	case "text":
		return map[string]any{"type": "text", "text": b.Text}
	case "thinking":
		m := map[string]any{"type": "thinking", "thinking": b.Thinking}
		if b.Signature != "" {
			m["signature"] = b.Signature
		}
		return m
	case "redacted_thinking":
		return map[string]any{"type": "redacted_thinking", "data": b.Data}
	case "tool_use":
		if b.ToolUse == nil {
			// Synthesized block (upstream omitted content_block_start): only
			// the type is known. Emit a minimal block instead of panicking.
			return map[string]any{"type": "tool_use"}
		}
		return map[string]any{"type": "tool_use", "id": b.ToolUse.ID, "name": b.ToolUse.Name, "input": json.RawMessage(b.ToolUse.Input)}
	}
	// Unknown block types (server_tool_use / web_search_tool_result 等 hosted
	// 工具块)：透传 Extra 里的原始字段。
	m := map[string]any{"type": b.Type}
	for k, v := range b.Extra {
		if _, exists := m[k]; !exists {
			m[k] = v
		}
	}
	return m
}

// decodeStreamContentBlock decodes a single content_block object from an SSE
// content_block_start event. Unlike decodeBlocks (which expects a JSON array),
// SSE events carry a single object.
func decodeStreamContentBlock(raw json.RawMessage) (*translate.ContentBlock, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, fmt.Errorf("anthropic stream content_block: %w", err)
	}
	switch head.Type {
	case "text":
		var tp rawTextPart
		_ = json.Unmarshal(raw, &tp)
		return &translate.ContentBlock{Type: "text", Text: tp.Text}, nil
	case "thinking":
		var tb struct {
			Type      string `json:"type"`
			Thinking  string `json:"thinking"`
			Signature string `json:"signature"`
		}
		_ = json.Unmarshal(raw, &tb)
		return &translate.ContentBlock{Type: "thinking", Thinking: tb.Thinking, Signature: tb.Signature}, nil
	case "redacted_thinking":
		var rb rawRedactedThinkingPart
		_ = json.Unmarshal(raw, &rb)
		return &translate.ContentBlock{Type: "redacted_thinking", Data: rb.Data}, nil
	case "tool_use":
		var tu rawToolUsePart
		_ = json.Unmarshal(raw, &tu)
		return &translate.ContentBlock{Type: "tool_use", ToolUse: &translate.ToolUse{
			ID: tu.ID, Name: tu.Name, Input: tu.Input,
		}}, nil
	}
	// Unknown block type: preserve type and keep the rest of the fields in
	// Extra so hosted server blocks (web_search_tool_result etc.) survive the
	// same-format round trip instead of being stripped to a bare type.
	b := decodeExtraBlock(head.Type, raw)
	return &b, nil
}
