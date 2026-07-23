package anthropic

import "encoding/json"

type rawRequest struct {
	Model         string          `json:"model"`
	System        json.RawMessage `json:"system,omitempty"`
	Messages      []rawMessage    `json:"messages"`
	Tools         []rawTool       `json:"tools,omitempty"`
	ToolChoice    json.RawMessage `json:"tool_choice,omitempty"`
	MaxTokens     int             `json:"max_tokens"`
	Temperature   *float64        `json:"temperature,omitempty"`
	TopP          *float64        `json:"top_p,omitempty"`
	Stream        bool            `json:"stream,omitempty"`
	StopSequences []string        `json:"stop_sequences,omitempty"`
}

type rawMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// content part variants
type rawTextPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type rawImagePart struct {
	Type   string      `json:"type"`
	Source rawImageSrc `json:"source"`
}

type rawImageSrc struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type rawThinkingPart struct {
	Type      string `json:"type"`
	Thinking  string `json:"thinking"`
	Signature string `json:"signature"`
}

type rawRedactedThinkingPart struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

type rawToolUsePart struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type rawToolResultPart struct {
	Type      string          `json:"type"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error,omitempty"`
}

type rawTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// Response (non-stream)
type rawResponse struct {
	ID         string            `json:"id"`
	Model      string            `json:"model"`
	Content    []json.RawMessage `json:"content"`
	StopReason string            `json:"stop_reason"`
	Usage      rawUsage          `json:"usage"`
}

type rawUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Stream event (generic, Type field discriminates)
type rawStreamEvent struct {
	Type         string          `json:"type"`
	Message      json.RawMessage `json:"message,omitempty"`
	Index        int             `json:"index,omitempty"`
	ContentBlock json.RawMessage `json:"content_block,omitempty"`
	Delta        json.RawMessage `json:"delta,omitempty"`
	Usage        json.RawMessage `json:"usage,omitempty"`
}
