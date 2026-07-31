package responses

import (
	"encoding/json"
	"fmt"

	"github.com/great-magician-01/any-llm/internal/translate"
)

func DecodeRequest(body []byte) (*translate.Request, error) {
	var known rawRequest
	if err := json.Unmarshal(body, &known); err != nil {
		return nil, fmt.Errorf("responses decode request: %w", err)
	}
	var all map[string]any
	_ = json.Unmarshal(body, &all)

	req := &translate.Request{
		Model:      known.Model,
		MaxTokens:  known.MaxOutputTokens,
		Stream:     known.Stream,
		ToolChoice: decodeToolChoice(known.ToolChoice),
	}
	req.System = append(req.System, decodeInstructions(known.Instructions)...)
	if known.Store {
		req.Extra = map[string]any{"store": true}
	}
	if known.PreviousResponse != "" {
		if req.Extra == nil {
			req.Extra = map[string]any{}
		}
		req.Extra["previous_response_id"] = known.PreviousResponse
	}
	for _, item := range known.Input {
		if item.Type == "" {
			// 角色消息
			if item.Role == "system" {
				req.System = append(req.System, decodePartsToText(item.Content)...)
				continue
			}
			if item.Role != "user" && item.Role != "assistant" {
				return nil, fmt.Errorf("responses decode request: unknown input role %q", item.Role)
			}
			blocks, err := decodeParts(item.Content)
			if err != nil {
				return nil, err
			}
			req.Messages = append(req.Messages, translate.Message{Role: item.Role, Content: blocks})
			continue
		}
		switch item.Type {
		case "function_call":
			req.Messages = append(req.Messages, translate.Message{
				Role: "assistant",
				Content: []translate.ContentBlock{{
					Type: "tool_use",
					ToolUse: &translate.ToolUse{
						ID:    item.CallID,
						Name:  item.Name,
						Input: json.RawMessage(item.Arguments),
					},
				}},
			})
		case "function_call_output":
			req.Messages = append(req.Messages, translate.Message{
				Role: "user",
				Content: []translate.ContentBlock{{
					Type: "tool_result",
					ToolResult: &translate.ToolResult{
						ToolUseID: item.CallID,
						Content:   []translate.ContentBlock{{Type: "text", Text: item.Output}},
					},
				}},
			})
		default:
			return nil, fmt.Errorf("responses decode request: unknown input item type %q", item.Type)
		}
	}
	for _, t := range known.Tools {
		req.Tools = append(req.Tools, translate.Tool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.Parameters,
		})
	}
	req.Extra = mergeExtra(req.Extra, extractExtra(all))
	return req, nil
}

// decodeInstructions 处理 instructions（字符串或 input_text 数组）。
func decodeInstructions(raw json.RawMessage) []translate.TextBlock {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []translate.TextBlock{{Text: s}}
	}
	var parts []rawPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil
	}
	var out []translate.TextBlock
	for _, p := range parts {
		if p.Type == "input_text" {
			out = append(out, translate.TextBlock{Text: p.Text})
		}
	}
	return out
}

// decodeParts 把消息 content parts 转成 IR 块。未知类型报错（不静默丢数据）。
func decodeParts(parts []rawPart) ([]translate.ContentBlock, error) {
	var blocks []translate.ContentBlock
	for _, p := range parts {
		switch p.Type {
		case "input_text":
			blocks = append(blocks, translate.ContentBlock{Type: "text", Text: p.Text})
		case "input_image":
			blocks = append(blocks, translate.ContentBlock{Type: "image", Image: &translate.Image{URL: p.ImageURL}})
		default:
			return nil, fmt.Errorf("responses decode request: unsupported content part type %q", p.Type)
		}
	}
	return blocks, nil
}

func decodePartsToText(parts []rawPart) []translate.TextBlock {
	var out []translate.TextBlock
	for _, p := range parts {
		if p.Type == "input_text" {
			out = append(out, translate.TextBlock{Text: p.Text})
		}
	}
	return out
}

func decodeToolChoice(raw json.RawMessage) *translate.ToolChoice {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return &translate.ToolChoice{Type: s} // auto / none / required
	}
	var obj struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && obj.Type == "function" {
		return &translate.ToolChoice{Type: "tool", Name: obj.Name}
	}
	return &translate.ToolChoice{Type: "auto"}
}

func mergeExtra(existing, more map[string]any) map[string]any {
	if len(more) == 0 {
		return existing
	}
	if existing == nil {
		return more
	}
	for k, v := range more {
		existing[k] = v
	}
	return existing
}

func DecodeResponse(body []byte) (*translate.Response, error) {
	var rr rawResponse
	if err := json.Unmarshal(body, &rr); err != nil {
		return nil, fmt.Errorf("responses decode response: %w", err)
	}
	resp := &translate.Response{ID: rr.ID, Model: rr.Model}
	hasToolCall := false
	for _, item := range rr.Output {
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				if part.Type == "output_text" {
					resp.Content = append(resp.Content, translate.ContentBlock{Type: "text", Text: part.Text})
				}
				// output_refusal 等其他 part 类型跳过
			}
		case "function_call":
			hasToolCall = true
			resp.Content = append(resp.Content, translate.ContentBlock{
				Type: "tool_use",
				ToolUse: &translate.ToolUse{
					ID:    item.CallID,
					Name:  item.Name,
					Input: json.RawMessage(item.Arguments),
				},
			})
		case "reasoning":
			var summary string
			for _, sp := range item.Summary {
				if sp.Type == "summary_text" {
					summary += sp.Text
				}
			}
			if summary != "" {
				resp.Content = append(resp.Content, translate.ContentBlock{
					Type: "thinking", Thinking: summary, Signature: item.ID,
				})
			}
		}
		// 未知 item 类型跳过
	}
	resp.StopReason = mapStopFromStatus(rr.Status, hasToolCall)
	if rr.Usage != nil {
		resp.Usage.InputTokens = rr.Usage.InputTokens
		resp.Usage.OutputTokens = rr.Usage.OutputTokens
		if rr.Usage.InputTokensDetails != nil {
			resp.Usage.CacheReadTokens = rr.Usage.InputTokensDetails.CachedTokens
		}
		if rr.Usage.OutputTokensDetails != nil {
			resp.Usage.ReasoningTokens = rr.Usage.OutputTokensDetails.ReasoningTokens
		}
	}
	return resp, nil
}

// mapStopFromStatus 把 Responses status 映射到 IR StopReason 词表。
// completed 且含 function_call 时推 tool_calls（与 chat completions 一致）。
func mapStopFromStatus(status string, hasToolCall bool) string {
	switch status {
	case "incomplete":
		return "max_tokens"
	case "completed":
		if hasToolCall {
			return "tool_calls"
		}
		return "stop"
	default: // failed 等：错误由 HTTP/事件层报告
		return "stop"
	}
}
