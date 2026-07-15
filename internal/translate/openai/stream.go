package openai

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/great-magician-01/any-llm/internal/translate"
)

type StreamDecoder struct {
	started     bool
	textOpen    bool // a text block at index 0 is open
	textIndex   int
	toolOpenIdx int  // which tool_calls index is currently open (-1 = none)
	toolBlock   int  // IR block index for the open tool block
	nextBlock   int  // next IR block index to allocate
	inputTokens int
}

func NewStreamDecoder() *StreamDecoder {
	return &StreamDecoder{toolOpenIdx: -1, nextBlock: 1}
}

// Decode consumes one SSE data payload (without "data: " prefix).
// Pass []byte("[DONE]") to signal stream end.
func (d *StreamDecoder) Decode(data []byte) ([]*translate.StreamEvent, error) {
	if strings.TrimSpace(string(data)) == "[DONE]" {
		var evs []*translate.StreamEvent
		if d.textOpen {
			evs = append(evs, &translate.StreamEvent{Type: "content_block_stop", Index: d.textIndex})
			d.textOpen = false
		}
		if d.toolOpenIdx >= 0 {
			evs = append(evs, &translate.StreamEvent{Type: "content_block_stop", Index: d.toolBlock})
			d.toolOpenIdx = -1
		}
		evs = append(evs, &translate.StreamEvent{Type: "message_stop"})
		return evs, nil
	}

	var ch rawChunk
	if err := json.Unmarshal(data, &ch); err != nil {
		return nil, fmt.Errorf("openai stream decode: %w", err)
	}
	var evs []*translate.StreamEvent

	// message_start on first chunk that has a role or model
	if !d.started && ((len(ch.Choices) > 0 && ch.Choices[0].Delta.Role != "") || ch.Model != "") {
		d.started = true
		evs = append(evs, &translate.StreamEvent{
			Type:      "message_start",
			MessageID: ch.ID,
			Model:     ch.Model,
		})
	}

	// usage-only chunk (choices empty) — record and emit into a message_delta if finish already sent
	if len(ch.Choices) == 0 {
		if ch.Usage != nil {
			d.inputTokens = ch.Usage.PromptTokens
		}
		return evs, nil
	}

	c := ch.Choices[0]

	// text content delta
	if c.Delta.Content != "" {
		if !d.textOpen {
			d.textOpen = true
			d.textIndex = 0
			evs = append(evs, &translate.StreamEvent{
				Type:  "content_block_start",
				Index: 0,
				Block: &translate.ContentBlock{Type: "text"},
			})
		}
		evs = append(evs, &translate.StreamEvent{
			Type:  "content_block_delta",
			Index: 0,
			Delta: &translate.Delta{Type: "text_delta", Text: c.Delta.Content},
		})
	}

	// tool_calls
	for _, tc := range c.Delta.ToolCalls {
		if tc.ID != "" {
			// new tool call: close text block if open
			if d.textOpen {
				evs = append(evs, &translate.StreamEvent{Type: "content_block_stop", Index: d.textIndex})
				d.textOpen = false
			}
			if d.toolOpenIdx >= 0 {
				evs = append(evs, &translate.StreamEvent{Type: "content_block_stop", Index: d.toolBlock})
			}
			d.toolOpenIdx = tcIndex(tc) // OpenAI tool_calls index (0-based)
			d.toolBlock = d.nextBlock
			d.nextBlock++
			evs = append(evs, &translate.StreamEvent{
				Type:  "content_block_start",
				Index: d.toolBlock,
				Block: &translate.ContentBlock{
					Type:    "tool_use",
					ToolUse: &translate.ToolUse{ID: tc.ID, Name: tc.Function.Name, Input: json.RawMessage("{}")},
				},
			})
		} else if d.toolOpenIdx >= 0 {
			// argument fragment
			evs = append(evs, &translate.StreamEvent{
				Type:  "content_block_delta",
				Index: d.toolBlock,
				Delta: &translate.Delta{Type: "input_json_delta", PartialJSON: tc.Function.Arguments},
			})
		}
	}

	// finish_reason
	if c.FinishReason != nil && *c.FinishReason != "" {
		if d.textOpen {
			evs = append(evs, &translate.StreamEvent{Type: "content_block_stop", Index: d.textIndex})
			d.textOpen = false
		}
		if d.toolOpenIdx >= 0 {
			evs = append(evs, &translate.StreamEvent{Type: "content_block_stop", Index: d.toolBlock})
			d.toolOpenIdx = -1
		}
		md := &translate.StreamEvent{
			Type:       "message_delta",
			StopReason: mapStopReasonFromOpenAI(*c.FinishReason),
		}
		if ch.Usage != nil {
			md.InputTokens = ch.Usage.PromptTokens
			md.OutputTokens = ch.Usage.CompletionTokens
		} else if d.inputTokens > 0 {
			md.InputTokens = d.inputTokens
		}
		evs = append(evs, md)
	}

	return evs, nil
}

// tcIndex extracts the OpenAI tool_calls array index from a delta entry.
// The raw struct doesn't carry index; it is the position in the slice.
// We rely on the caller having one tool call per chunk for the "new" case.
func tcIndex(tc rawToolCall) int { return 0 }

type StreamEncoder struct {
	model    string
	id       string
	toolIdx  map[int]int // IR block index -> OpenAI tool index
	nextTool int
}

func NewStreamEncoder(model string) *StreamEncoder {
	return &StreamEncoder{model: model, toolIdx: map[int]int{}}
}

func (e *StreamEncoder) Encode(evt *translate.StreamEvent) ([][]byte, error) {
	switch evt.Type {
	case "message_start":
		e.id = evt.MessageID
		if e.id == "" {
			e.id = "chatcmpl-anylem"
		}
		ch := map[string]any{
			"id":      e.id,
			"object":  "chat.completion.chunk",
			"choices": []map[string]any{{"index": 0, "delta": map[string]any{"role": "assistant", "content": ""}}},
		}
		if e.model != "" || evt.Model != "" {
			m := evt.Model
			if m == "" {
				m = e.model
			}
			ch["model"] = m
			e.model = m
		}
		return [][]byte{frame(ch)}, nil

	case "content_block_start":
		if evt.Block != nil && evt.Block.Type == "tool_use" {
			idx := e.nextTool
			e.nextTool++
			e.toolIdx[evt.Index] = idx
			ch := map[string]any{
				"id": e.id, "object": "chat.completion.chunk",
				"choices": []map[string]any{{"index": 0, "delta": map[string]any{
					"tool_calls": []map[string]any{{
						"index": idx, "id": evt.Block.ToolUse.ID, "type": "function",
						"function": map[string]any{"name": evt.Block.ToolUse.Name, "arguments": ""},
					}},
				}}},
			}
			return [][]byte{frame(ch)}, nil
		}
		// text block start -> no OpenAI output
		return nil, nil

	case "content_block_delta":
		if evt.Delta == nil {
			return nil, nil
		}
		switch evt.Delta.Type {
		case "text_delta":
			ch := map[string]any{
				"id": e.id, "object": "chat.completion.chunk",
				"choices": []map[string]any{{"index": 0, "delta": map[string]any{"content": evt.Delta.Text}}},
			}
			return [][]byte{frame(ch)}, nil
		case "input_json_delta":
			idx, ok := e.toolIdx[evt.Index]
			if !ok {
				idx = 0
			}
			ch := map[string]any{
				"id": e.id, "object": "chat.completion.chunk",
				"choices": []map[string]any{{"index": 0, "delta": map[string]any{
					"tool_calls": []map[string]any{{
						"index": idx,
						"function": map[string]any{"arguments": evt.Delta.PartialJSON},
					}},
				}}},
			}
			return [][]byte{frame(ch)}, nil
		}

	case "content_block_stop":
		return nil, nil

	case "message_delta":
		choice := map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": mapStopReasonToOpenAI(evt.StopReason)}
		ch := map[string]any{"id": e.id, "object": "chat.completion.chunk", "choices": []map[string]any{choice}}
		if evt.InputTokens > 0 || evt.OutputTokens > 0 {
			ch["usage"] = map[string]any{
				"prompt_tokens": evt.InputTokens, "completion_tokens": evt.OutputTokens,
				"total_tokens": evt.InputTokens + evt.OutputTokens,
			}
		}
		return [][]byte{frame(ch)}, nil

	case "message_stop":
		return [][]byte{[]byte("data: [DONE]\n\n")}, nil
	}
	return nil, nil
}

func frame(v any) []byte {
	b, _ := json.Marshal(v)
	return []byte("data: " + string(b) + "\n\n")
}
