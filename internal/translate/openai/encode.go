package openai

import (
	"encoding/json"
	"fmt"

	"github.com/great-magician-01/any-llm/internal/translate"
)

func EncodeRequest(req *translate.Request) ([]byte, error) {
	var msgs []rawMessage
	// system messages first
	for _, s := range req.System {
		msgs = append(msgs, rawMessage{Role: "system", Content: jsonStr(s.Text)})
	}
	for _, m := range req.Messages {
		switch m.Role {
		case "user":
			// split tool_result blocks (-> role:tool) from other blocks (-> user content)
			var contentParts []map[string]any
			var toolResults []translate.ContentBlock
			for _, b := range m.Content {
				if b.Type == "tool_result" {
					toolResults = append(toolResults, b)
				} else {
					contentParts = append(contentParts, blockToOpenAIPart(b))
				}
			}
			for _, tr := range toolResults {
				msgs = append(msgs, rawMessage{
					Role:       "tool",
					ToolCallID: tr.ToolResult.ToolUseID,
					Content:    jsonStr(blocksToText(tr.ToolResult.Content)),
				})
			}
			if len(contentParts) > 0 {
				if len(contentParts) == 1 {
					if t, ok := contentParts[0]["type"].(string); ok && t == "text" {
						if txt, ok := contentParts[0]["text"].(string); ok {
							msgs = append(msgs, rawMessage{Role: "user", Content: jsonStr(txt)})
							continue
						}
					}
				}
				raw, _ := json.Marshal(contentParts)
				msgs = append(msgs, rawMessage{Role: "user", Content: raw})
			}
		case "assistant":
			rm := rawMessage{Role: "assistant"}
			var hasText bool
			var text string
			for _, b := range m.Content {
				if b.Type == "text" {
					text += b.Text
					hasText = true
				} else if b.Type == "tool_use" {
					rm.ToolCalls = append(rm.ToolCalls, rawToolCall{
						ID:   b.ToolUse.ID,
						Type: "function",
						Function: rawToolFunction{
							Name:      b.ToolUse.Name,
							Arguments: string(b.ToolUse.Input),
						},
					})
				}
			}
			if hasText {
				rm.Content = jsonStr(text)
			}
			msgs = append(msgs, rm)
		}
	}

	out := map[string]any{
		"model":    req.Model,
		"messages": msgs,
	}
	if req.MaxTokens > 0 {
		out["max_tokens"] = req.MaxTokens
	}
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
		out["stop"] = req.Stop
	}
	if len(req.Tools) > 0 {
		var tools []rawTool
		for _, t := range req.Tools {
			tools = append(tools, rawTool{Type: "function", Function: rawToolDef{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			}})
		}
		out["tools"] = tools
	}
	if req.ToolChoice != nil {
		switch req.ToolChoice.Type {
		case "auto", "none", "required":
			out["tool_choice"] = req.ToolChoice.Type
		case "tool":
			out["tool_choice"] = map[string]any{
				"type":     "function",
				"function": map[string]any{"name": req.ToolChoice.Name},
			}
		}
	}
	for k, v := range req.Extra {
		if _, exists := out[k]; !exists {
			out[k] = v
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("openai encode request: %w", err)
	}
	return b, nil
}

func jsonStr(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

func blockToOpenAIPart(b translate.ContentBlock) map[string]any {
	switch b.Type {
	case "text":
		return map[string]any{"type": "text", "text": b.Text}
	case "image":
		return map[string]any{"type": "image_url", "image_url": map[string]any{"url": b.Image.URL}}
	}
	return map[string]any{"type": "text", "text": ""}
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

// EncodeResponse produces a non-stream OpenAI chat completion response.
func EncodeResponse(resp *translate.Response) ([]byte, error) {
	rm := rawRespMessage{Role: "assistant"}
	for _, b := range resp.Content {
		switch b.Type {
		case "text":
			rm.Content += b.Text
		case "thinking":
			// Anthropic thinking blocks map to reasoning_content for
			// OpenAI-format clients (DeepSeek-style).
			rm.ReasoningContent += b.Thinking
		case "tool_use":
			rm.ToolCalls = append(rm.ToolCalls, rawToolCall{
				ID:   b.ToolUse.ID,
				Type: "function",
				Function: rawToolFunction{
					Name:      b.ToolUse.Name,
					Arguments: string(b.ToolUse.Input),
				},
			})
		}
	}
	rr := rawResponse{
		ID:    resp.ID,
		Model: resp.Model,
		Choices: []rawChoice{{
			Index:        0,
			Message:      rm,
			FinishReason: mapStopReasonToOpenAI(resp.StopReason),
		}},
	}
	if resp.Usage.InputTokens > 0 || resp.Usage.OutputTokens > 0 {
		usage := &rawUsage{
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
			TotalTokens:      resp.Usage.InputTokens + resp.Usage.OutputTokens,
		}
		if resp.Usage.CacheReadTokens > 0 {
			usage.PromptTokensDetails = &rawPromptTokensDetails{CachedTokens: resp.Usage.CacheReadTokens}
			usage.PromptCacheHitTokens = resp.Usage.CacheReadTokens
			if miss := resp.Usage.InputTokens - resp.Usage.CacheReadTokens; miss > 0 {
				usage.PromptCacheMissTokens = miss
			}
		}
		if resp.Usage.ReasoningTokens > 0 {
			usage.CompletionTokensDetails = &rawCompletionTokensDetails{ReasoningTokens: resp.Usage.ReasoningTokens}
		}
		rr.Usage = usage
	}
	b, err := json.Marshal(rr)
	if err != nil {
		return nil, fmt.Errorf("openai encode response: %w", err)
	}
	return b, nil
}

func mapStopReasonToOpenAI(reason string) string {
	switch reason {
	case "stop", "end_turn":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_calls", "tool_use":
		return "tool_calls"
	case "content_filter":
		return "content_filter"
	}
	return "stop"
}
