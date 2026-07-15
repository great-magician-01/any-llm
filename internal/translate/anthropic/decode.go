package anthropic

import (
	"encoding/json"
	"fmt"

	"github.com/great-magician-01/any-llm/internal/translate"
)

func DecodeRequest(body []byte) (*translate.Request, error) {
	var known rawRequest
	if err := json.Unmarshal(body, &known); err != nil {
		return nil, fmt.Errorf("anthropic decode request: %w", err)
	}
	var all map[string]any
	_ = json.Unmarshal(body, &all)

	req := &translate.Request{
		Model:       known.Model,
		MaxTokens:   known.MaxTokens,
		Temperature: known.Temperature,
		TopP:        known.TopP,
		Stream:      known.Stream,
		Stop:        known.StopSequences,
	}
	// system: string or array of text blocks
	req.System = decodeSystem(known.System)
	for _, m := range known.Messages {
		blocks, err := decodeBlocks(m.Content)
		if err != nil {
			return nil, err
		}
		role := m.Role
		if role == "" {
			role = "user"
		}
		req.Messages = append(req.Messages, translate.Message{Role: role, Content: blocks})
	}
	for _, t := range known.Tools {
		req.Tools = append(req.Tools, translate.Tool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}
	if len(known.ToolChoice) > 0 {
		req.ToolChoice = decodeAnthropicToolChoice(known.ToolChoice)
	}
	req.Extra = extractExtra(all)
	return req, nil
}

func decodeSystem(raw json.RawMessage) []translate.TextBlock {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []translate.TextBlock{{Text: s}}
	}
	var parts []rawTextPart
	if err := json.Unmarshal(raw, &parts); err == nil {
		var out []translate.TextBlock
		for _, p := range parts {
			out = append(out, translate.TextBlock{Text: p.Text})
		}
		return out
	}
	return nil
}

func decodeBlocks(raw json.RawMessage) ([]translate.ContentBlock, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	// string content
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []translate.ContentBlock{{Type: "text", Text: s}}, nil
	}
	// array of typed parts
	var parts []json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, fmt.Errorf("anthropic content: %w", err)
	}
	var out []translate.ContentBlock
	for _, p := range parts {
		var head struct{ Type string `json:"type"` }
		if err := json.Unmarshal(p, &head); err != nil {
			return nil, err
		}
		switch head.Type {
		case "text":
			var tp rawTextPart
			_ = json.Unmarshal(p, &tp)
			out = append(out, translate.ContentBlock{Type: "text", Text: tp.Text})
		case "image":
			var ip rawImagePart
			_ = json.Unmarshal(p, &ip)
			out = append(out, translate.ContentBlock{Type: "image", Image: &translate.Image{
				Base64:    ip.Source.Data,
				MediaType: ip.Source.MediaType,
			}})
		case "tool_use":
			var tu rawToolUsePart
			_ = json.Unmarshal(p, &tu)
			out = append(out, translate.ContentBlock{Type: "tool_use", ToolUse: &translate.ToolUse{
				ID: tu.ID, Name: tu.Name, Input: tu.Input,
			}})
		case "tool_result":
			var tr rawToolResultPart
			_ = json.Unmarshal(p, &tr)
			out = append(out, translate.ContentBlock{Type: "tool_result", ToolResult: &translate.ToolResult{
				ToolUseID: tr.ToolUseID,
				Content:   decodeResultContent(tr.Content),
				IsError:   tr.IsError,
			}})
		}
	}
	return out, nil
}

func decodeResultContent(raw json.RawMessage) []translate.ContentBlock {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []translate.ContentBlock{{Type: "text", Text: s}}
	}
	blocks, _ := decodeBlocks(raw)
	return blocks
}

func decodeAnthropicToolChoice(raw json.RawMessage) *translate.ToolChoice {
	var obj struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return &translate.ToolChoice{Type: obj.Type, Name: obj.Name}
	}
	return &translate.ToolChoice{Type: "auto"}
}

var knownAnthropicKeys = map[string]bool{
	"model": true, "system": true, "messages": true, "tools": true, "tool_choice": true,
	"max_tokens": true, "temperature": true, "top_p": true, "stream": true, "stop_sequences": true,
}

func extractExtra(all map[string]any) map[string]any {
	if len(all) == 0 {
		return nil
	}
	extra := map[string]any{}
	for k, v := range all {
		if !knownAnthropicKeys[k] {
			extra[k] = v
		}
	}
	if len(extra) == 0 {
		return nil
	}
	return extra
}

func DecodeResponse(body []byte) (*translate.Response, error) {
	var rr rawResponse
	if err := json.Unmarshal(body, &rr); err != nil {
		return nil, fmt.Errorf("anthropic decode response: %w", err)
	}
	resp := &translate.Response{
		ID:         rr.ID,
		Model:      rr.Model,
		StopReason: mapStopReasonFromAnthropic(rr.StopReason),
		Usage: translate.Usage{
			InputTokens:  rr.Usage.InputTokens,
			OutputTokens: rr.Usage.OutputTokens,
		},
	}
	blocks, err := decodeBlocks(arrayToRaw(rr.Content))
	if err != nil {
		return nil, err
	}
	resp.Content = blocks
	return resp, nil
}

// arrayToRaw re-serializes a slice of RawMessage into a single JSON array RawMessage.
func arrayToRaw(parts []json.RawMessage) json.RawMessage {
	if len(parts) == 0 {
		return nil
	}
	b, _ := json.Marshal(parts)
	return b
}

func mapStopReasonFromAnthropic(reason string) string {
	switch reason {
	case "end_turn":
		return "stop"
	case "tool_use":
		return "tool_calls"
	case "max_tokens":
		return "max_tokens"
	case "content_filter":
		return "content_filter"
	}
	return reason
}
