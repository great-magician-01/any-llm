package translate

import "encoding/json"

// Request is the normalized, format-agnostic representation of an inbound
// chat completion request.
type Request struct {
	Model       string
	System      []TextBlock
	Messages    []Message
	Tools       []Tool
	ToolChoice  *ToolChoice
	MaxTokens   int
	Temperature *float64
	TopP        *float64
	Stream      bool
	Stop        []string
	Extra       map[string]any
}

type TextBlock struct {
	Text string
}

type Message struct {
	Role    string // "user" | "assistant"
	Content []ContentBlock
}

// ContentBlock is a discriminated union; Type selects the populated field.
type ContentBlock struct {
	Type       string // "text" | "image" | "tool_use" | "tool_result"
	Text       string
	Image      *Image
	ToolUse    *ToolUse
	ToolResult *ToolResult
}

type Image struct {
	URL       string // http(s) URL (OpenAI image_url form)
	Base64    string // base64-encoded data (Anthropic source form)
	MediaType string // media type when Base64 is set
}

type ToolUse struct {
	ID    string
	Name  string
	Input json.RawMessage
}

type ToolResult struct {
	ToolUseID string
	Content   []ContentBlock
	IsError   bool
}

type Tool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

type ToolChoice struct {
	Type string // "auto" | "none" | "tool"
	Name string // set when Type == "tool"
}

type Response struct {
	ID         string
	Model      string
	Content    []ContentBlock
	StopReason string // canonical IR vocabulary: "stop" | "max_tokens" | "tool_calls" | "content_filter"
	Usage      Usage
	Extra      map[string]any
}

type Usage struct {
	InputTokens  int
	OutputTokens int
}

// StreamEvent is an Anthropic-style fine-grained streaming event.
type StreamEvent struct {
	Type         string        // message_start | content_block_start | content_block_delta | content_block_stop | message_delta | message_stop
	MessageID    string        // message_start
	Model        string        // message_start
	InputTokens  int           // message_start
	Index        int           // content_block_*
	Block        *ContentBlock // content_block_start
	Delta        *Delta        // content_block_delta
	StopReason   string        // message_delta
	OutputTokens int           // message_delta
}

type Delta struct {
	Type        string // "text_delta" | "input_json_delta"
	Text        string // text_delta
	PartialJSON string // input_json_delta
}
