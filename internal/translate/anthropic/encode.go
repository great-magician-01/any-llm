package anthropic

import (
	"encoding/json"
	"fmt"

	"github.com/great-magician-01/any-llm/internal/translate"
)

const defaultMaxTokens = 4096

func EncodeRequest(req *translate.Request) ([]byte, error) {
	mt := req.MaxTokens
	if mt <= 0 {
		mt = defaultMaxTokens
	}
	out := map[string]any{
		"model":      req.Model,
		"max_tokens": mt,
	}
	// system
	if len(req.System) > 0 {
		if len(req.System) == 1 {
			out["system"] = req.System[0].Text
		} else {
			var sys []map[string]string
			for _, s := range req.System {
				sys = append(sys, map[string]string{"type": "text", "text": s.Text})
			}
			out["system"] = sys
		}
	}
	// messages — merge adjacent same-role messages so the output satisfies
	// Anthropic's "roles must alternate" constraint. OpenAI clients emit one
	// role:"tool" message per tool result, which decodes into one IR "user"
	// message per result; without merging, two parallel tool results become
	// two consecutive user messages and Anthropic rejects the request.
	var msgs []rawMessage
	var curRole string
	var curParts []map[string]any
	flush := func() {
		if curRole == "" {
			return
		}
		raw, _ := json.Marshal(curParts)
		msgs = append(msgs, rawMessage{Role: curRole, Content: raw})
		curRole, curParts = "", nil
	}
	for _, m := range req.Messages {
		parts := encodeBlocks(m.Content)
		if m.Role == curRole {
			curParts = append(curParts, parts...)
			continue
		}
		flush()
		curRole, curParts = m.Role, parts
	}
	flush()
	out["messages"] = msgs
	if req.Temperature != nil {
		out["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		out["top_p"] = *req.TopP
	}
	if req.Stream {
		out["stream"] = true
	}
	if len(req.Stop) > 0 {
		out["stop_sequences"] = req.Stop
	}
	if len(req.Tools) > 0 {
		var tools []map[string]any
		for _, t := range req.Tools {
			m := map[string]any{"name": t.Name}
			if t.Description != "" {
				m["description"] = t.Description
			}
			if t.Type != "" {
				m["type"] = t.Type
			}
			// hosted 工具（web_search_20250305 等）没有 input_schema；
			// 输出 null 会让上游把工具当成 schema 为 null 的函数而报 400。
			if len(t.InputSchema) > 0 {
				m["input_schema"] = t.InputSchema
			}
			for k, v := range t.Extra {
				if _, exists := m[k]; !exists {
					m[k] = v
				}
			}
			tools = append(tools, m)
		}
		out["tools"] = tools
	}
	if req.ToolChoice != nil {
		tcType := req.ToolChoice.Type
		// IR "required" (must call a tool) maps to Anthropic's "any".
		if tcType == "required" {
			tcType = "any"
		}
		tc := map[string]any{"type": tcType}
		if req.ToolChoice.Type == "tool" {
			tc["name"] = req.ToolChoice.Name
		}
		out["tool_choice"] = tc
	}
	for k, v := range req.Extra {
		if _, exists := out[k]; !exists {
			out[k] = v
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("anthropic encode request: %w", err)
	}
	return b, nil
}

func encodeBlocks(blocks []translate.ContentBlock) []map[string]any {
	var parts []map[string]any
	for _, b := range blocks {
		switch b.Type {
		case "text":
			parts = append(parts, map[string]any{"type": "text", "text": b.Text})
		case "image":
			parts = append(parts, map[string]any{
				"type": "image",
				"source": map[string]any{
					"type": "base64", "media_type": b.Image.MediaType, "data": b.Image.Base64,
				},
			})
		case "thinking":
			m := map[string]any{"type": "thinking", "thinking": b.Thinking}
			if b.Signature != "" {
				m["signature"] = b.Signature
			}
			parts = append(parts, m)
		case "redacted_thinking":
			parts = append(parts, map[string]any{"type": "redacted_thinking", "data": b.Data})
		case "tool_use":
			parts = append(parts, map[string]any{
				"type": "tool_use", "id": b.ToolUse.ID, "name": b.ToolUse.Name, "input": json.RawMessage(b.ToolUse.Input),
			})
		case "tool_result":
			parts = append(parts, map[string]any{
				"type":        "tool_result",
				"tool_use_id": b.ToolResult.ToolUseID,
				"content":     encodeResultContent(b.ToolResult.Content),
				"is_error":    b.ToolResult.IsError,
			})
		default:
			// 未知块类型（含 hosted server 块）原样透传。
			m := map[string]any{"type": b.Type}
			for k, v := range b.Extra {
				if _, exists := m[k]; !exists {
					m[k] = v
				}
			}
			parts = append(parts, m)
		}
	}
	return parts
}

func encodeResultContent(blocks []translate.ContentBlock) any {
	if len(blocks) == 0 {
		return ""
	}
	if len(blocks) == 1 && blocks[0].Type == "text" {
		return blocks[0].Text
	}
	return encodeBlocks(blocks)
}

// EncodeResponse produces a non-stream Anthropic message response.
func EncodeResponse(resp *translate.Response) ([]byte, error) {
	var content []map[string]any
	for _, b := range resp.Content {
		switch b.Type {
		case "text":
			content = append(content, map[string]any{"type": "text", "text": b.Text})
		case "thinking":
			m := map[string]any{"type": "thinking", "thinking": b.Thinking}
			if b.Signature != "" {
				m["signature"] = b.Signature
			}
			content = append(content, m)
		case "redacted_thinking":
			content = append(content, map[string]any{"type": "redacted_thinking", "data": b.Data})
		case "tool_use":
			content = append(content, map[string]any{
				"type": "tool_use", "id": b.ToolUse.ID, "name": b.ToolUse.Name, "input": json.RawMessage(b.ToolUse.Input),
			})
		default:
			// hosted server 块（web_search_tool_result 等）原样透传。
			m := map[string]any{"type": b.Type}
			for k, v := range b.Extra {
				if _, exists := m[k]; !exists {
					m[k] = v
				}
			}
			content = append(content, m)
		}
	}
	out := map[string]any{
		"id":          resp.ID,
		"model":       resp.Model,
		"role":        "assistant",
		"content":     content,
		"stop_reason": mapStopReasonToAnthropic(resp.StopReason),
		"type":        "message",
		"usage": map[string]any{
			"input_tokens":                resp.Usage.InputTokens,
			"output_tokens":               resp.Usage.OutputTokens,
			"cache_creation_input_tokens": resp.Usage.CacheCreationTokens,
			"cache_read_input_tokens":     resp.Usage.CacheReadTokens,
		},
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("anthropic encode response: %w", err)
	}
	return b, nil
}

func mapStopReasonToAnthropic(reason string) string {
	switch reason {
	case "stop":
		return "end_turn"
	case "max_tokens":
		return "max_tokens"
	case "tool_calls", "tool_use":
		return "tool_use"
	case "content_filter":
		return "end_turn"
	}
	return reason
}
