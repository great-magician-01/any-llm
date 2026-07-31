package responses

import (
	"encoding/json"
	"fmt"
	"strings"

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
			// 文本 parts 合并进 assistant 消息 item（在 function_call item 之前），
			// 工具调用转为顶层 function_call item，思考块丢弃。
			var parts []any
			var fcs []any
			for _, b := range m.Content {
				switch b.Type {
				case "text":
					parts = append(parts, map[string]any{"type": "input_text", "text": b.Text})
				case "tool_use":
					fcs = append(fcs, map[string]any{
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
			input = append(input, fcs...)
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
			tools = append(tools, map[string]any{
				"type":        "function",
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.InputSchema,
			})
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
