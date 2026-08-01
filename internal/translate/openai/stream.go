package openai

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/great-magician-01/any-llm/internal/translate"
)

type StreamDecoder struct {
	started       bool
	textOpen      bool // a text block is open
	textIndex     int
	thinkingOpen  bool // a thinking block is open (DeepSeek reasoning_content)
	thinkingIndex int
	openTools     map[int]int // OpenAI tool_calls index -> IR block index
	nextBlock     int         // next IR block index to allocate
	slot0Taken    bool        // the index-0 slot is held by the first block opened
	id            string      // message id, used to synthesize the thinking signature
	inputTokens   int
	outputTokens  int
	cacheRead     int    // prompt cache hits (cached_tokens / prompt_cache_hit_tokens)
	reasoning     int    // completion_tokens_details.reasoning_tokens
	finished      bool   // finish_reason received
	deltaSent     bool   // message_delta already emitted
	stopReason    string // buffered stop reason (emitted with deferred message_delta)
}

func NewStreamDecoder() *StreamDecoder {
	return &StreamDecoder{openTools: map[int]int{}, nextBlock: 1}
}

// Decode consumes one SSE data payload (without "data: " prefix).
// Pass []byte("[DONE]") to signal stream end.
func (d *StreamDecoder) Decode(data []byte) ([]*translate.StreamEvent, error) {
	if strings.TrimSpace(string(data)) == "[DONE]" {
		var evs []*translate.StreamEvent
		evs = append(evs, d.closeThinking()...)
		if d.textOpen {
			evs = append(evs, &translate.StreamEvent{Type: "content_block_stop", Index: d.textIndex})
			d.textOpen = false
		}
		evs = append(evs, d.closeAllTools()...)
		if d.finished && !d.deltaSent {
			d.deltaSent = true
			evs = append(evs, &translate.StreamEvent{
				Type:       "message_delta",
				StopReason: d.stopReason,
			})
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
		d.id = ch.ID
		evs = append(evs, &translate.StreamEvent{
			Type:      "message_start",
			MessageID: ch.ID,
			Model:     ch.Model,
		})
	}

	// usage-only chunk (choices empty) — standard OpenAI pattern when
	// stream_options.include_usage is true: usage arrives in a separate chunk
	// AFTER finish_reason. Record the tokens and, if finish was already
	// received, emit the deferred message_delta carrying stop_reason + usage.
	if len(ch.Choices) == 0 {
		d.applyUsage(ch.Usage)
		if d.finished && !d.deltaSent {
			d.deltaSent = true
			evs = append(evs, d.messageDeltaEvent())
		}
		return evs, nil
	}

	c := ch.Choices[0]

	// reasoning content delta (DeepSeek streams thinking here, before content
	// and tool_calls). Converted to an Anthropic thinking block.
	if c.Delta.ReasoningContent != "" {
		if !d.thinkingOpen {
			d.thinkingOpen = true
			d.thinkingIndex = d.nextBlockIndex()
			evs = append(evs, &translate.StreamEvent{
				Type:  "content_block_start",
				Index: d.thinkingIndex,
				Block: &translate.ContentBlock{Type: "thinking"},
			})
		}
		evs = append(evs, &translate.StreamEvent{
			Type:  "content_block_delta",
			Index: d.thinkingIndex,
			Delta: &translate.Delta{Type: "thinking_delta", Thinking: c.Delta.ReasoningContent},
		})
	}

	// text content delta
	if c.Delta.Content != "" {
		// content starts after reasoning: close the thinking block first
		evs = append(evs, d.closeThinking()...)
		if !d.textOpen {
			d.textOpen = true
			d.textIndex = d.nextBlockIndex()
			evs = append(evs, &translate.StreamEvent{
				Type:  "content_block_start",
				Index: d.textIndex,
				Block: &translate.ContentBlock{Type: "text"},
			})
		}
		evs = append(evs, &translate.StreamEvent{
			Type:  "content_block_delta",
			Index: d.textIndex,
			Delta: &translate.Delta{Type: "text_delta", Text: c.Delta.Content},
		})
	}

	// tool_calls
	for _, tc := range c.Delta.ToolCalls {
		if tc.ID != "" {
			// new tool call: close thinking and text blocks if open
			evs = append(evs, d.closeThinking()...)
			if d.textOpen {
				evs = append(evs, &translate.StreamEvent{Type: "content_block_stop", Index: d.textIndex})
				d.textOpen = false
			}
			// if a tool with the same OpenAI index is already open, close it first
			if blockIdx, ok := d.openTools[tc.Index]; ok {
				evs = append(evs, &translate.StreamEvent{Type: "content_block_stop", Index: blockIdx})
				delete(d.openTools, tc.Index)
			}
			newBlock := d.nextBlock
			d.nextBlock++
			d.openTools[tc.Index] = newBlock
			evs = append(evs, &translate.StreamEvent{
				Type:  "content_block_start",
				Index: newBlock,
				Block: &translate.ContentBlock{
					Type:    "tool_use",
					ToolUse: &translate.ToolUse{ID: tc.ID, Name: tc.Function.Name, Input: json.RawMessage("{}")},
				},
			})
			// Some upstreams (e.g. DeepSeek) attach the first arguments
			// fragment to the same chunk as the id. Forward it, or the
			// accumulated tool input JSON would be truncated and unparseable.
			if tc.Function.Arguments != "" {
				evs = append(evs, &translate.StreamEvent{
					Type:  "content_block_delta",
					Index: newBlock,
					Delta: &translate.Delta{Type: "input_json_delta", PartialJSON: tc.Function.Arguments},
				})
			}
		} else {
			// argument fragment: route to the IR block for this OpenAI tool index
			blockIdx, ok := d.openTools[tc.Index]
			if !ok {
				continue
			}
			evs = append(evs, &translate.StreamEvent{
				Type:  "content_block_delta",
				Index: blockIdx,
				Delta: &translate.Delta{Type: "input_json_delta", PartialJSON: tc.Function.Arguments},
			})
		}
	}

	// finish_reason
	if c.FinishReason != nil && *c.FinishReason != "" {
		evs = append(evs, d.closeThinking()...)
		if d.textOpen {
			evs = append(evs, &translate.StreamEvent{Type: "content_block_stop", Index: d.textIndex})
			d.textOpen = false
		}
		evs = append(evs, d.closeAllTools()...)
		d.finished = true
		d.stopReason = mapStopReasonFromOpenAI(*c.FinishReason)
		// If usage is in this same chunk or was seen earlier, emit message_delta
		// immediately. Otherwise defer until the usage-only chunk or [DONE].
		d.applyUsage(ch.Usage)
		if d.inputTokens > 0 || d.outputTokens > 0 || d.cacheRead > 0 || d.reasoning > 0 {
			d.deltaSent = true
			evs = append(evs, d.messageDeltaEvent())
		}
	}

	return evs, nil
}

// nextBlockIndex assigns the next IR block index for text/thinking blocks.
// The first block opened takes index 0 (DeepSeek's own Anthropic streams put
// thinking at index 0); later blocks ascend from nextBlock so text/tools keep
// their historical indices.
func (d *StreamDecoder) nextBlockIndex() int {
	if !d.slot0Taken {
		d.slot0Taken = true
		return 0
	}
	idx := d.nextBlock
	d.nextBlock++
	return idx
}

// applyUsage records token usage from a chunk, including prompt cache hits
// and reasoning tokens (DeepSeek exposes cached_tokens in details and also as
// a top-level prompt_cache_hit_tokens field).
func (d *StreamDecoder) applyUsage(u *rawUsage) {
	if u == nil {
		return
	}
	d.inputTokens = u.PromptTokens
	d.outputTokens = u.CompletionTokens
	if u.PromptTokensDetails != nil {
		d.cacheRead = u.PromptTokensDetails.CachedTokens
	}
	if d.cacheRead == 0 {
		d.cacheRead = u.PromptCacheHitTokens
	}
	if u.CompletionTokensDetails != nil {
		d.reasoning = u.CompletionTokensDetails.ReasoningTokens
	}
}

// messageDeltaEvent builds the message_delta event carrying stop_reason and
// the full usage (input/output/cache/reasoning).
func (d *StreamDecoder) messageDeltaEvent() *translate.StreamEvent {
	return &translate.StreamEvent{
		Type:            "message_delta",
		StopReason:      d.stopReason,
		InputTokens:     d.inputTokens,
		OutputTokens:    d.outputTokens,
		CacheReadTokens: d.cacheRead,
		ReasoningTokens: d.reasoning,
	}
}

// closeThinking emits the signature delta and content_block_stop for an open
// thinking block. The signature is synthesized from the message id, mirroring
// DeepSeek's own Anthropic streams (signature_delta before content_block_stop).
func (d *StreamDecoder) closeThinking() []*translate.StreamEvent {
	if !d.thinkingOpen {
		return nil
	}
	var evs []*translate.StreamEvent
	if d.id != "" {
		evs = append(evs, &translate.StreamEvent{
			Type:  "content_block_delta",
			Index: d.thinkingIndex,
			Delta: &translate.Delta{Type: "signature_delta", Signature: d.id},
		})
	}
	evs = append(evs, &translate.StreamEvent{Type: "content_block_stop", Index: d.thinkingIndex})
	d.thinkingOpen = false
	return evs
}

// closeAllTools emits content_block_stop for every open tool block in
// ascending IR block index order, then clears the open-tool tracking.
func (d *StreamDecoder) closeAllTools() []*translate.StreamEvent {
	if len(d.openTools) == 0 {
		return nil
	}
	var idxs []int
	for _, blockIdx := range d.openTools {
		idxs = append(idxs, blockIdx)
	}
	sort.Ints(idxs)
	var evs []*translate.StreamEvent
	for _, blockIdx := range idxs {
		evs = append(evs, &translate.StreamEvent{Type: "content_block_stop", Index: blockIdx})
	}
	d.openTools = map[int]int{}
	return evs
}

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
						"index":    idx,
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
		if evt.InputTokens > 0 || evt.OutputTokens > 0 || evt.CacheReadTokens > 0 || evt.ReasoningTokens > 0 {
			usage := map[string]any{
				"prompt_tokens": evt.InputTokens, "completion_tokens": evt.OutputTokens,
				"total_tokens": evt.InputTokens + evt.OutputTokens,
			}
			if evt.CacheReadTokens > 0 {
				usage["prompt_tokens_details"] = map[string]any{"cached_tokens": evt.CacheReadTokens}
				usage["prompt_cache_hit_tokens"] = evt.CacheReadTokens
				if miss := evt.InputTokens - evt.CacheReadTokens; miss > 0 {
					usage["prompt_cache_miss_tokens"] = miss
				}
			}
			if evt.ReasoningTokens > 0 {
				usage["completion_tokens_details"] = map[string]any{"reasoning_tokens": evt.ReasoningTokens}
			}
			ch["usage"] = usage
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
