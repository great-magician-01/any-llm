package responses

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/great-magician-01/any-llm/internal/translate"
)

func EncodeRequest(req *translate.Request) ([]byte, error) {
	out := map[string]any{}
	if req.Model != "" {
		out["model"] = req.Model
	}
	var sys []string
	for _, s := range req.System {
		sys = append(sys, s.Text)
	}
	if len(sys) > 0 {
		out["instructions"] = strings.Join(sys, "\n")
	}
	var input []any
	for _, m := range req.Messages {
		switch m.Role {
		case "user":
			var parts []any
			var toolResults []translate.ContentBlock
			for _, b := range m.Content {
				if b.Type == "tool_result" {
					toolResults = append(toolResults, b)
				} else {
					parts = append(parts, blockToPart(b))
				}
			}
			for _, tr := range toolResults {
				input = append(input, map[string]any{
					"type":    "function_call_output",
					"call_id": tr.ToolResult.ToolUseID,
					"output":  blocksToText(tr.ToolResult.Content),
				})
			}
			if len(parts) > 0 {
				input = append(input, map[string]any{"role": "user", "content": parts})
			}
		case "assistant":
			var parts []any
			var fcItems []any // 延迟收集：assistant 文本项在前、function_call 项在后（对话顺序）
			for _, b := range m.Content {
				switch b.Type {
				case "text":
					parts = append(parts, map[string]any{"type": "input_text", "text": b.Text})
				case "tool_use":
					fcItems = append(fcItems, map[string]any{
						"type":      "function_call",
						"call_id":   b.ToolUse.ID,
						"name":      b.ToolUse.Name,
						"arguments": string(b.ToolUse.Input),
					})
				case "thinking", "redacted_thinking":
					// Responses input 无思考载体，丢弃
				}
			}
			if len(parts) > 0 {
				input = append(input, map[string]any{"role": "assistant", "content": parts})
			}
			input = append(input, fcItems...)
		case "system":
			var parts []any
			for _, b := range m.Content {
				if b.Type == "text" {
					parts = append(parts, map[string]any{"type": "input_text", "text": b.Text})
				}
			}
			if len(parts) > 0 {
				input = append(input, map[string]any{"role": "system", "content": parts})
			}
		}
	}
	if len(input) > 0 {
		out["input"] = input
	}
	if req.MaxTokens > 0 {
		out["max_output_tokens"] = req.MaxTokens
	}
	if req.Stream {
		out["stream"] = true
	}
	if len(req.Tools) > 0 {
		var tools []any
		for _, t := range req.Tools {
			m := map[string]any{
				"type": "function",
				"name": t.Name,
			}
			if t.Description != "" {
				m["description"] = t.Description
			}
			// 同 openai 编码器：无 schema 时省略 parameters，避免 null。
			if len(t.InputSchema) > 0 {
				m["parameters"] = t.InputSchema
			}
			tools = append(tools, m)
		}
		out["tools"] = tools
	}
	if req.ToolChoice != nil {
		switch req.ToolChoice.Type {
		case "auto", "none", "required":
			out["tool_choice"] = req.ToolChoice.Type
		case "tool":
			out["tool_choice"] = map[string]any{"type": "function", "name": req.ToolChoice.Name}
		}
	}
	for k, v := range req.Extra {
		switch k {
		case "previous_response_id", "store", "text":
			// 网关层消费或忽略，不转发给上游
			continue
		}
		if _, exists := out[k]; !exists {
			out[k] = v
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("responses encode request: %w", err)
	}
	return b, nil
}

// blockToPart 把 IR 内容块转成 input part。
func blockToPart(b translate.ContentBlock) map[string]any {
	switch b.Type {
	case "text":
		return map[string]any{"type": "input_text", "text": b.Text}
	case "image":
		url := b.Image.URL
		if b.Image.Base64 != "" {
			url = "data:" + b.Image.MediaType + ";base64," + b.Image.Base64
		}
		return map[string]any{"type": "input_image", "image_url": url}
	}
	return map[string]any{"type": "input_text", "text": ""}
}

func blocksToText(blocks []translate.ContentBlock) string {
	var s string
	for _, b := range blocks {
		if b.Type == "text" {
			s += b.Text
		}
	}
	return s
}

// NewID 生成客户端可见的响应 id，也是会话存储的 key。
func NewID() string {
	return "resp_" + randHex(16)
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func EncodeResponse(resp *translate.Response) ([]byte, error) {
	if resp.ID == "" {
		resp.ID = NewID()
	}
	out := make([]map[string]any, 0, len(resp.Content))
	for _, b := range resp.Content {
		switch b.Type {
		case "text":
			// 连续 text 块合并进同一个 message item
			if len(out) > 0 {
				if last, ok := out[len(out)-1]["type"].(string); ok && last == "message" {
					content, _ := out[len(out)-1]["content"].([]any)
					out[len(out)-1]["content"] = append(content, map[string]any{
						"type": "output_text", "text": b.Text, "annotations": []any{},
					})
					continue
				}
			}
			out = append(out, map[string]any{
				"type": "message", "id": "msg_" + randHex(8), "status": "completed", "role": "assistant",
				"content": []any{map[string]any{"type": "output_text", "text": b.Text, "annotations": []any{}}},
			})
		case "thinking":
			out = append(out, map[string]any{
				"type": "reasoning", "id": "rs_" + randHex(8),
				"summary": []any{map[string]any{"type": "summary_text", "text": b.Thinking}},
				"content": []any{},
			})
		case "tool_use":
			out = append(out, map[string]any{
				"type": "function_call", "id": "fc_" + randHex(8),
				"call_id": b.ToolUse.ID, "name": b.ToolUse.Name,
				"arguments": string(b.ToolUse.Input),
			})
		}
		// redacted_thinking / image 无法在 Responses 表示，跳过
	}
	status := "completed"
	switch resp.StopReason {
	case "max_tokens":
		status = "incomplete"
	}
	obj := map[string]any{
		"id": resp.ID, "object": "response", "created_at": time.Now().Unix(),
		"status": status, "model": resp.Model, "output": out,
	}
	if status == "incomplete" {
		obj["incomplete_details"] = map[string]any{"reason": "max_output_tokens"}
	}
	if resp.Usage.InputTokens > 0 || resp.Usage.OutputTokens > 0 {
		usage := map[string]any{
			"input_tokens":  resp.Usage.InputTokens,
			"output_tokens": resp.Usage.OutputTokens,
			"total_tokens":  resp.Usage.InputTokens + resp.Usage.OutputTokens,
		}
		if resp.Usage.CacheReadTokens > 0 {
			usage["input_tokens_details"] = map[string]any{"cached_tokens": resp.Usage.CacheReadTokens}
		}
		if resp.Usage.ReasoningTokens > 0 {
			usage["output_tokens_details"] = map[string]any{"reasoning_tokens": resp.Usage.ReasoningTokens}
		}
		obj["usage"] = usage
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("responses encode response: %w", err)
	}
	return b, nil
}
