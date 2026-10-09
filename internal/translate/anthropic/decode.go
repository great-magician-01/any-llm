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
	// Hosted tools (web_search_20250305 等) 没有 input_schema，参数是 type/
	// max_uses 等直接字段——从原始 JSON 里补全 Type 与 Extra，避免同格式
	// 往返时把 hosted 工具破坏成 input_schema:null 的普通函数工具。
	if rawTools, ok := all["tools"].([]any); ok {
		for i := range rawTools {
			if i >= len(req.Tools) {
				break
			}
			m, _ := rawTools[i].(map[string]any)
			if typ, ok := m["type"].(string); ok {
				req.Tools[i].Type = typ
			}
			extra := map[string]any{}
			for k, v := range m {
				switch k {
				case "name", "description", "input_schema", "type":
				default:
					extra[k] = v
				}
			}
			if len(extra) > 0 {
				req.Tools[i].Extra = extra
			}
		}
	}
	if len(known.ToolChoice) > 0 {
		req.ToolChoice = decodeAnthropicToolChoice(known.ToolChoice)
	}
	req.Extra = translate.ExtractExtra(all, knownAnthropicKeys)
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
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(p, &head); err != nil {
			return nil, err
		}
		switch head.Type {
		case "text":
			var tp rawTextPart
			if err := json.Unmarshal(p, &tp); err != nil {
				return nil, fmt.Errorf("anthropic content: malformed text block: %w", err)
			}
			out = append(out, translate.ContentBlock{Type: "text", Text: tp.Text})
		case "image":
			var ip rawImagePart
			if err := json.Unmarshal(p, &ip); err != nil {
				return nil, fmt.Errorf("anthropic content: malformed image block: %w", err)
			}
			out = append(out, translate.ContentBlock{Type: "image", Image: anthropicImage(ip.Source)})
		case "thinking":
			var tp rawThinkingPart
			if err := json.Unmarshal(p, &tp); err != nil {
				return nil, fmt.Errorf("anthropic content: malformed thinking block: %w", err)
			}
			out = append(out, translate.ContentBlock{Type: "thinking", Thinking: tp.Thinking, Signature: tp.Signature})
		case "redacted_thinking":
			var rp rawRedactedThinkingPart
			if err := json.Unmarshal(p, &rp); err != nil {
				return nil, fmt.Errorf("anthropic content: malformed redacted_thinking block: %w", err)
			}
			out = append(out, translate.ContentBlock{Type: "redacted_thinking", Data: rp.Data})
		case "tool_use":
			var tu rawToolUsePart
			if err := json.Unmarshal(p, &tu); err != nil {
				return nil, fmt.Errorf("anthropic content: malformed tool_use block: %w", err)
			}
			out = append(out, translate.ContentBlock{Type: "tool_use", ToolUse: &translate.ToolUse{
				ID: tu.ID, Name: tu.Name, Input: tu.Input,
			}})
		case "tool_result":
			var tr rawToolResultPart
			if err := json.Unmarshal(p, &tr); err != nil {
				return nil, fmt.Errorf("anthropic content: malformed tool_result block: %w", err)
			}
			out = append(out, translate.ContentBlock{Type: "tool_result", ToolResult: &translate.ToolResult{
				ToolUseID: tr.ToolUseID,
				Content:   decodeResultContent(tr.Content),
				IsError:   tr.IsError,
			}})
		default:
			// 未知块类型（server_tool_use / web_search_tool_result 等 hosted
			// 工具块）：保留 type，其余字段进 Extra 供同格式往返透传。
			out = append(out, decodeExtraBlock(head.Type, p))
		}
	}
	return out, nil
}

// decodeExtraBlock 把未知类型的内容块拆成 type + 其余字段。
func decodeExtraBlock(typ string, raw json.RawMessage) translate.ContentBlock {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return translate.ContentBlock{Type: typ}
	}
	delete(m, "type")
	if len(m) == 0 {
		m = nil
	}
	return translate.ContentBlock{Type: typ, Extra: m}
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
		tc := &translate.ToolChoice{Type: obj.Type, Name: obj.Name}
		// Anthropic expresses "must call a tool" as {"type":"any"}; OpenAI uses
		// "required". Normalize to the IR "required" value so cross-format
		// encoding works in both directions.
		if tc.Type == "any" {
			tc.Type = "required"
		}
		return tc
	}
	return &translate.ToolChoice{Type: "auto"}
}

var knownAnthropicKeys = map[string]bool{
	"model": true, "system": true, "messages": true, "tools": true, "tool_choice": true,
	"max_tokens": true, "temperature": true, "top_p": true, "stream": true, "stop_sequences": true,
}

// anthropicImage 归一化 Anthropic 的图片 source：base64 source 保留载荷，
// url source（以及塞在 url 里的 data URL）统一走 translate.NewImage。
// 只认 base64 source 会让 {"type":"url",...} 的图片整块变成空图片。
func anthropicImage(src rawImageSrc) *translate.Image {
	if src.Data != "" {
		return &translate.Image{Base64: src.Data, MediaType: src.MediaType}
	}
	return translate.NewImage(src.URL)
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
			InputTokens:         rr.Usage.InputTokens,
			OutputTokens:        rr.Usage.OutputTokens,
			CacheReadTokens:     rr.Usage.CacheReadInputTokens,
			CacheCreationTokens: rr.Usage.CacheCreationInputTokens,
		},
	}
	blocks, err := decodeBlocks(arrayToRaw(rr.Content))
	if err != nil {
		return nil, err
	}
	resp.Content = blocks
	if len(rr.SafeguardResults) > 0 {
		resp.Extra = map[string]any{translate.KeySafeguardResults: json.RawMessage(rr.SafeguardResults)}
	}
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
