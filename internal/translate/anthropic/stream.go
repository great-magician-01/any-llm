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
	case "ping", "":
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
		}
		return evt, nil
	case "content_block_start":
		evt := &translate.StreamEvent{Type: "content_block_start", Index: raw.Index}
		blocks, err := decodeBlocks(raw.ContentBlock)
		if err != nil {
			return nil, err
		}
		if len(blocks) > 0 {
			evt.Block = &blocks[0]
		}
		return evt, nil
	case "content_block_delta":
		evt := &translate.StreamEvent{Type: "content_block_delta", Index: raw.Index}
		var d struct {
			Type        string `json:"type"`
			Text        string `json:"text,omitempty"`
			PartialJSON string `json:"partial_json,omitempty"`
		}
		_ = json.Unmarshal(raw.Delta, &d)
		evt.Delta = &translate.Delta{Type: d.Type, Text: d.Text, PartialJSON: d.PartialJSON}
		return evt, nil
	case "content_block_stop":
		return &translate.StreamEvent{Type: "content_block_stop", Index: raw.Index}, nil
	case "message_delta":
		evt := &translate.StreamEvent{Type: "message_delta"}
		var d struct {
			StopReason string `json:"stop_reason"`
		}
		_ = json.Unmarshal(raw.Delta, &d)
		evt.StopReason = d.StopReason
		if raw.Usage != nil {
			evt.OutputTokens = raw.Usage.OutputTokens
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
		msg := map[string]any{"id": evt.MessageID, "model": evt.Model, "role": "assistant"}
		if evt.InputTokens > 0 {
			msg["usage"] = map[string]any{"input_tokens": evt.InputTokens}
		}
		payload["message"] = msg
	case "content_block_start":
		payload["index"] = evt.Index
		if evt.Block != nil {
			payload["content_block"] = blockToRaw(*evt.Block)
		}
	case "content_block_delta":
		payload["index"] = evt.Index
		if evt.Delta != nil {
			d := map[string]any{"type": evt.Delta.Type}
			if evt.Delta.Type == "text_delta" {
				d["text"] = evt.Delta.Text
			} else {
				d["partial_json"] = evt.Delta.PartialJSON
			}
			payload["delta"] = d
		}
	case "content_block_stop":
		payload["index"] = evt.Index
	case "message_delta":
		d := map[string]any{"stop_reason": evt.StopReason}
		payload["delta"] = d
		if evt.OutputTokens > 0 || evt.InputTokens > 0 {
			payload["usage"] = map[string]any{"output_tokens": evt.OutputTokens}
		}
	case "message_stop":
		// no extra fields
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("anthropic stream encode: %w", err)
	}
	return []byte("event: " + evt.Type + "\ndata: " + string(b) + "\n\n"), nil
}

func blockToRaw(b translate.ContentBlock) map[string]any {
	switch b.Type {
	case "text":
		return map[string]any{"type": "text", "text": b.Text}
	case "tool_use":
		return map[string]any{"type": "tool_use", "id": b.ToolUse.ID, "name": b.ToolUse.Name, "input": json.RawMessage(b.ToolUse.Input)}
	}
	return map[string]any{"type": b.Type}
}
