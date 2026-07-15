package openai

import "encoding/json"

type rawRequest struct {
	Model       string          `json:"model"`
	Messages    []rawMessage    `json:"messages"`
	Tools       []rawTool       `json:"tools,omitempty"`
	ToolChoice  json.RawMessage `json:"tool_choice,omitempty"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
	TopP        *float64        `json:"top_p,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
	Stop        json.RawMessage `json:"stop,omitempty"`
	StreamOpts  json.RawMessage `json:"stream_options,omitempty"`
}

type rawMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content,omitempty"`
	ToolCalls  []rawToolCall   `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

type rawToolCall struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Function rawToolFunction `json:"function"`
}

type rawToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type rawTool struct {
	Type     string     `json:"type"`
	Function rawToolDef `json:"function"`
}

type rawToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Response (non-stream)
type rawResponse struct {
	ID      string      `json:"id"`
	Model   string      `json:"model"`
	Choices []rawChoice `json:"choices"`
	Usage   *rawUsage   `json:"usage,omitempty"`
}

type rawChoice struct {
	Index        int            `json:"index"`
	Message      rawRespMessage `json:"message"`
	FinishReason string         `json:"finish_reason"`
}

type rawRespMessage struct {
	Role      string        `json:"role"`
	Content   string        `json:"content,omitempty"`
	ToolCalls []rawToolCall `json:"tool_calls,omitempty"`
}

type rawUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Stream chunk
type rawChunk struct {
	ID      string           `json:"id"`
	Model   string           `json:"model,omitempty"`
	Choices []rawChunkChoice `json:"choices"`
	Usage   *rawUsage        `json:"usage,omitempty"`
}

type rawChunkChoice struct {
	Index        int      `json:"index"`
	Delta        rawDelta `json:"delta"`
	FinishReason *string  `json:"finish_reason,omitempty"`
}

type rawDelta struct {
	Role      string        `json:"role,omitempty"`
	Content   string        `json:"content,omitempty"`
	ToolCalls []rawToolCall `json:"tool_calls,omitempty"`
}
