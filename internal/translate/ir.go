package translate

import (
	"encoding/json"
	"strings"
)

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
	Type       string // "text" | "image" | "tool_use" | "tool_result" | "thinking" | "redacted_thinking"
	Text       string
	Thinking   string // thinking block content (Type == "thinking")
	Signature  string // thinking block signature (Type == "thinking")
	Data       string // redacted_thinking block data (Type == "redacted_thinking")
	Image      *Image
	ToolUse    *ToolUse
	ToolResult *ToolResult
	// Extra carries format-specific fields for block types without an IR
	// equivalent (e.g. Anthropic hosted server blocks like
	// web_search_tool_result / server_tool_use), so same-format round trips
	// don't lose them. Known block types ignore it.
	Extra map[string]any
}

type Image struct {
	URL       string // http(s) URL (OpenAI image_url form)
	Base64    string // base64-encoded data (Anthropic source form)
	MediaType string // media type when Base64 is set
}

// SplitDataURL parses an inline "data:<media_type>;base64,<payload>" image URL.
// ok is false for anything that is not a base64 data URL (plain http(s) URLs,
// empty strings, or data URLs with a non-base64 encoding).
func SplitDataURL(s string) (mediaType, payload string, ok bool) {
	rest, found := strings.CutPrefix(s, "data:")
	if !found {
		return "", "", false
	}
	meta, data, found := strings.Cut(rest, ",")
	if !found {
		return "", "", false
	}
	mediaType, enc, found := strings.Cut(meta, ";")
	if !found || enc != "base64" || mediaType == "" || data == "" {
		return "", "", false
	}
	return mediaType, data, true
}

// DataURL is the inverse of SplitDataURL.
func DataURL(mediaType, payload string) string {
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	return "data:" + mediaType + ";base64," + payload
}

// NewImage normalizes an image carried by a format with a single URL-ish field
// (OpenAI image_url, Responses input_image, Anthropic source.type=url): inline
// base64 data URLs are split into Base64 + MediaType, everything else stays a
// URL. Without this normalization a URL image re-encoded into another format
// degrades into an empty payload.
func NewImage(source string) *Image {
	if mediaType, payload, ok := SplitDataURL(source); ok {
		return &Image{Base64: payload, MediaType: mediaType}
	}
	return &Image{URL: source}
}

// SourceURL renders the image back into a single URL-ish string (for formats
// whose image field is one string): an inline payload becomes a data URL.
func (i *Image) SourceURL() string {
	if i == nil {
		return ""
	}
	if i.URL != "" {
		return i.URL
	}
	if i.Base64 != "" {
		return DataURL(i.MediaType, i.Base64)
	}
	return ""
}

// Base64Payload returns the image as (mediaType, base64 payload) whichever side
// of the IR it was populated from; ok is false for a plain URL with no inline
// data.
func (i *Image) Base64Payload() (mediaType, payload string, ok bool) {
	if i == nil {
		return "", "", false
	}
	if i.Base64 != "" {
		mediaType = i.MediaType
		if mediaType == "" {
			mediaType = "application/octet-stream"
		}
		return mediaType, i.Base64, true
	}
	return SplitDataURL(i.URL)
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
	// Type is the tool type for formats that distinguish one; empty for plain
	// function tools. Anthropic hosted tools use types like
	// "web_search_20250305" and carry no InputSchema.
	Type string
	// Extra holds format-specific tool fields with no IR equivalent (e.g.
	// Anthropic hosted-tool params max_uses / allowed_domains /
	// blocked_domains), preserved on same-format round trips.
	Extra map[string]any
}

type ToolChoice struct {
	Type string // "auto" | "none" | "required" | "tool"
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
	InputTokens         int
	OutputTokens        int
	CacheReadTokens     int // prompt cache hits (Anthropic cache_read_input_tokens / OpenAI cached_tokens)
	CacheCreationTokens int // Anthropic cache_creation_input_tokens (tokens written into the prompt cache)
	ReasoningTokens     int // OpenAI completion_tokens_details.reasoning_tokens
}

// StreamEvent is an Anthropic-style fine-grained streaming event.
type StreamEvent struct {
	Type                string          // message_start | content_block_start | content_block_delta | content_block_stop | message_delta | message_stop | ping | error
	MessageID           string          // message_start
	Model               string          // message_start
	InputTokens         int             // message_start
	Index               int             // content_block_*
	Block               *ContentBlock   // content_block_start
	Delta               *Delta          // content_block_delta
	StopReason          string          // message_delta
	OutputTokens        int             // message_delta
	CacheReadTokens     int             // prompt cache hits (message_start / message_delta)
	CacheCreationTokens int             // Anthropic cache writes (message_start / message_delta)
	ReasoningTokens     int             // OpenAI reasoning tokens (message_delta)
	RawMessage          json.RawMessage // message_start: raw `message` object from same-format upstream (pass-through)
	RawUsage            json.RawMessage // message_delta: raw `usage` object from same-format upstream (pass-through)
	// DeltaExtras carries raw fields from an upstream `message_delta.delta`
	// object that IR does not model (e.g. Claude Code's `safeguard_results`).
	// They are merged back verbatim on encode for same-format pass-through.
	DeltaExtras map[string]json.RawMessage
	// ErrType / ErrMessage 只在 Type == "error" 时有值：上游流内错误事件的原始
	// type/code 与 message（Anthropic 的 event:error、Responses 的
	// response.failed/error）。上游解码器填充；网关用它给客户端写带内错误帧，
	// 为空时回退通用文案。
	ErrType    string
	ErrMessage string
}

type Delta struct {
	Type        string // "text_delta" | "input_json_delta" | "thinking_delta" | "signature_delta"
	Text        string // text_delta
	PartialJSON string // input_json_delta
	Thinking    string // thinking_delta
	Signature   string // signature_delta (used by some upstreams)
}

// KeySafeguardResults 是 Claude Code 服务端分类器判决的字段名（非流式响应
// 的顶层字段 / 流式 message_delta 的 delta 字段）。IR 不解析语义，只原样
// 透传，保证同格式回环不丢。
const KeySafeguardResults = "safeguard_results"

// ExtractExtra 返回 all 中不在 known 里的键值对——请求体内 IR 未建模、
// 原样透传给同格式上游的字段。各格式的解码器共用（已知键表各自维护）；
// 无剩余字段时返回 nil（IR 对 Extra 的约定：nil = 没有额外字段）。
func ExtractExtra(all map[string]any, known map[string]bool) map[string]any {
	if len(all) == 0 {
		return nil
	}
	extra := map[string]any{}
	for k, v := range all {
		if !known[k] {
			extra[k] = v
		}
	}
	if len(extra) == 0 {
		return nil
	}
	return extra
}
