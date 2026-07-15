package openai

import (
	"encoding/json"
	"fmt"

	"github.com/great-magician-01/any-llm/internal/translate"
)

func DecodeRequest(body []byte) (*translate.Request, error) {
	var known rawRequest
	if err := json.Unmarshal(body, &known); err != nil {
		return nil, fmt.Errorf("openai decode request: %w", err)
	}
	var all map[string]any
	_ = json.Unmarshal(body, &all)

	req := &translate.Request{
		Model:       known.Model,
		MaxTokens:   known.MaxTokens,
		Temperature: known.Temperature,
		TopP:        known.TopP,
		Stream:      known.Stream,
	}
	for _, m := range known.Messages {
		switch m.Role {
		case "system":
			req.System = append(req.System, translate.TextBlock{Text: decodeString(m.Content)})
		case "user":
			blocks, err := decodeUserContent(m.Content)
			if err != nil {
				return nil, err
			}
			req.Messages = append(req.Messages, translate.Message{Role: "user", Content: blocks})
		case "assistant":
			var blocks []translate.ContentBlock
			if txt := decodeString(m.Content); txt != "" {
				blocks = append(blocks, translate.ContentBlock{Type: "text", Text: txt})
			}
			for _, tc := range m.ToolCalls {
				blocks = append(blocks, translate.ContentBlock{
					Type: "tool_use",
					ToolUse: &translate.ToolUse{
						ID:    tc.ID,
						Name:  tc.Function.Name,
						Input: json.RawMessage(tc.Function.Arguments),
					},
				})
			}
			req.Messages = append(req.Messages, translate.Message{Role: "assistant", Content: blocks})
		case "tool":
			req.Messages = append(req.Messages, translate.Message{
				Role: "user",
				Content: []translate.ContentBlock{{
					Type: "tool_result",
					ToolResult: &translate.ToolResult{
						ToolUseID: m.ToolCallID,
						Content:   []translate.ContentBlock{{Type: "text", Text: decodeString(m.Content)}},
					},
				}},
			})
		}
	}
	for _, t := range known.Tools {
		req.Tools = append(req.Tools, translate.Tool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: t.Function.Parameters,
		})
	}
	if len(known.ToolChoice) > 0 {
		req.ToolChoice = decodeToolChoice(known.ToolChoice)
	}
	if len(known.Stop) > 0 {
		req.Stop = decodeStop(known.Stop)
	}
	req.Extra = extractExtra(all)
	return req, nil
}

// decodeString handles content that is a JSON string or null/empty.
func decodeString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return ""
}

func decodeUserContent(raw json.RawMessage) ([]translate.ContentBlock, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	// try string
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []translate.ContentBlock{{Type: "text", Text: s}}, nil
	}
	// array of parts
	var parts []struct {
		Type     string `json:"type"`
		Text     string `json:"text,omitempty"`
		ImageURL *struct {
			URL string `json:"url"`
		} `json:"image_url,omitempty"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, fmt.Errorf("openai user content: %w", err)
	}
	var blocks []translate.ContentBlock
	for _, p := range parts {
		switch p.Type {
		case "text":
			blocks = append(blocks, translate.ContentBlock{Type: "text", Text: p.Text})
		case "image_url":
			blocks = append(blocks, translate.ContentBlock{Type: "image", Image: &translate.Image{URL: p.ImageURL.URL}})
		}
	}
	return blocks, nil
}

func decodeToolChoice(raw json.RawMessage) *translate.ToolChoice {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return &translate.ToolChoice{Type: s}
	}
	var obj struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		tc := &translate.ToolChoice{Type: obj.Type}
		if obj.Type == "function" {
			tc.Type = "tool"
		}
		tc.Name = obj.Function.Name
		return tc
	}
	return &translate.ToolChoice{Type: "auto"}
}

func decodeStop(raw json.RawMessage) []string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []string{s}
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr
	}
	return nil
}

var knownRequestKeys = map[string]bool{
	"model": true, "messages": true, "tools": true, "tool_choice": true,
	"max_tokens": true, "temperature": true, "top_p": true, "stream": true,
	"stop": true, "stream_options": true,
}

func extractExtra(all map[string]any) map[string]any {
	if len(all) == 0 {
		return nil
	}
	extra := map[string]any{}
	for k, v := range all {
		if !knownRequestKeys[k] {
			extra[k] = v
		}
	}
	if len(extra) == 0 {
		return nil
	}
	return extra
}
