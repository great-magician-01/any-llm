# any-llm Translation Layer Implementation Plan (Plan A)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a pure, I/O-free translation layer that converts between OpenAI/Anthropic wire formats and a normalized intermediate representation (IR), covering requests, responses, and SSE streams (including function calling and images).

**Architecture:** Define IR types in package `translate` (`internal/translate/ir.go`). Two codec subpackages `openai` and `anthropic` each provide `DecodeRequest`, `EncodeRequest`, `DecodeResponse`, `EncodeResponse`, plus a stateful `StreamDecoder`/`StreamEncoder` for SSE. Processing flow: `入站 body → DecodeRequest → IR → EncodeRequest → 上游`; responses reverse. Streams: Anthropic-style IR events are the intermediate; OpenAI codec synthesizes/collapses block boundaries.

**Tech Stack:** Go 1.25, standard library only (no external deps for this plan). Tests via `go test`.

## Global Constraints

- Module path: `github.com/great-magician-01/any-llm` (from existing go.mod)
- Go 1.25.5
- No external dependencies in this plan — standard library only
- IR lives in package `translate` at `internal/translate/ir.go`; codecs in `internal/translate/openai` and `internal/translate/anthropic` (subpackages import the parent `translate` package — no cycle since `translate` never imports them)
- All content normalized to `[]ContentBlock` (never a bare string) inside IR
- Tool results are `ContentBlock{Type:"tool_result"}` inside a `Message{Role:"user"}` (Anthropic-native); OpenAI `role:"tool"` maps to/from this
- `Extra map[string]any` captures fields IR does not model explicitly
- Stream IR events use Anthropic-style fine-grained types: `message_start`, `content_block_start`, `content_block_delta`, `content_block_stop`, `message_delta`, `message_stop`

## File Structure

| File | Responsibility |
|------|----------------|
| `internal/translate/ir.go` | IR type definitions (Request, Response, StreamEvent, ContentBlock, etc.) |
| `internal/translate/ir_test.go` | IR round-trip JSON sanity tests |
| `internal/translate/openai/types.go` | OpenAI raw wire structs |
| `internal/translate/openai/decode.go` | `DecodeRequest`, `DecodeResponse` |
| `internal/translate/openai/encode.go` | `EncodeRequest`, `EncodeResponse` |
| `internal/translate/openai/stream.go` | `StreamDecoder`, `StreamEncoder` (stateful) |
| `internal/translate/openai/decode_test.go` | Table tests for decode |
| `internal/translate/openai/encode_test.go` | Table tests for encode |
| `internal/translate/openai/stream_test.go` | Table tests for stream |
| `internal/translate/anthropic/types.go` | Anthropic raw wire structs |
| `internal/translate/anthropic/decode.go` | `DecodeRequest`, `DecodeResponse` |
| `internal/translate/anthropic/encode.go` | `EncodeRequest`, `EncodeResponse` |
| `internal/translate/anthropic/stream.go` | `StreamDecoder`, `StreamEncoder` |
| `internal/translate/anthropic/decode_test.go` | Table tests for decode |
| `internal/translate/anthropic/encode_test.go` | Table tests for encode |
| `internal/translate/anthropic/stream_test.go` | Table tests for stream |
| `internal/translate/cross_test.go` | Cross-format round-trip integration tests (OAI→IR→ANT→IR→OAI) |

---

### Task 1: IR types

**Files:**
- Create: `internal/translate/ir.go`
- Test: `internal/translate/ir_test.go`

**Interfaces:**
- Produces: `translate.Request`, `translate.Response`, `translate.StreamEvent`, `translate.Message`, `translate.ContentBlock`, `translate.ToolUse`, `translate.ToolResult`, `translate.Tool`, `translate.ToolChoice`, `translate.Image`, `translate.Usage`, `translate.TextBlock`, `translate.Delta` — used by all later tasks.

- [ ] **Step 1: Write the IR types file**

Create `internal/translate/ir.go`:

```go
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
	StopReason string
	Usage      Usage
	Extra      map[string]any
}

type Usage struct {
	InputTokens  int
	OutputTokens int
}

// StreamEvent is an Anthropic-style fine-grained streaming event.
type StreamEvent struct {
	Type         string // message_start | content_block_start | content_block_delta | content_block_stop | message_delta | message_stop
	MessageID    string // message_start
	Model        string // message_start
	InputTokens  int    // message_start
	Index        int    // content_block_* 
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
```

- [ ] **Step 2: Write a sanity test**

Create `internal/translate/ir_test.go`:

```go
package translate

import (
	"encoding/json"
	"testing"
)

func TestContentBlockToolUseRoundTrip(t *testing.T) {
	b := ContentBlock{
		Type: "tool_use",
		ToolUse: &ToolUse{
			ID:    "call_1",
			Name:  "get_weather",
			Input: json.RawMessage(`{"city":"SF"}`),
		},
	}
	raw, err := json.Marshal(b.ToolUse.Input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal input: %v", err)
	}
	if got["city"] != "SF" {
		t.Fatalf("city = %q, want SF", got["city"])
	}
}

func TestRequestZeroValue(t *testing.T) {
	var r Request
	if r.Stream != false || r.Model != "" || r.Extra != nil {
		t.Fatalf("zero value not clean: %+v", r)
	}
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/translate/`
Expected: PASS (2 tests)

- [ ] **Step 4: Commit**

```bash
git add internal/translate/ir.go internal/translate/ir_test.go
git commit -m "feat(translate): add IR type definitions"
```

---

### Task 2: OpenAI raw wire types

**Files:**
- Create: `internal/translate/openai/types.go`

**Interfaces:**
- Produces: internal raw structs `rawRequest`, `rawMessage`, `rawToolCall`, `rawTool`, `rawResponse`, `rawChoice`, `rawChunk` used by decode/encode/stream tasks.

- [ ] **Step 1: Write the OpenAI wire structs**

Create `internal/translate/openai/types.go`:

```go
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
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function rawToolFunction  `json:"function"`
}

type rawToolFunction struct {
	Name      string          `json:"name"`
	Arguments string          `json:"arguments"`
}

type rawTool struct {
	Type     string           `json:"type"`
	Function rawToolDef       `json:"function"`
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
	Index        int          `json:"index"`
	Message      rawRespMessage `json:"message"`
	FinishReason string       `json:"finish_reason"`
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
	ID      string          `json:"id"`
	Model   string          `json:"model,omitempty"`
	Choices []rawChunkChoice `json:"choices"`
	Usage   *rawUsage       `json:"usage,omitempty"`
}

type rawChunkChoice struct {
	Index        int             `json:"index"`
	Delta        rawDelta        `json:"delta"`
	FinishReason *string         `json:"finish_reason,omitempty"`
}

type rawDelta struct {
	Role      string        `json:"role,omitempty"`
	Content   string        `json:"content,omitempty"`
	ToolCalls []rawToolCall `json:"tool_calls,omitempty"`
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/translate/openai/`
Expected: no output (compiles)

- [ ] **Step 3: Commit**

```bash
git add internal/translate/openai/types.go
git commit -m "feat(openai): add raw wire structs"
```

---

### Task 3: OpenAI DecodeRequest

**Files:**
- Create: `internal/translate/openai/decode.go`
- Test: `internal/translate/openai/decode_test.go`

**Interfaces:**
- Consumes: `translate.*` types from Task 1
- Produces: `func DecodeRequest(body []byte) (*translate.Request, error)` — used by the gateway in Plan B.

- [ ] **Step 1: Write the failing test**

Create `internal/translate/openai/decode_test.go`:

```go
package openai

import (
	"encoding/json"
	"testing"

	"github.com/great-magician-01/any-llm/internal/translate"
)

func TestDecodeRequest_TextOnly(t *testing.T) {
	body := []byte(`{
		"model":"gpt-4o",
		"messages":[
			{"role":"system","content":"You are helpful"},
			{"role":"user","content":"Hello"}
		],
		"max_tokens":100,
		"temperature":0.5
	}`)
	req, err := DecodeRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if req.Model != "gpt-4o" {
		t.Fatalf("model=%q", req.Model)
	}
	if len(req.System) != 1 || req.System[0].Text != "You are helpful" {
		t.Fatalf("system=%+v", req.System)
	}
	if len(req.Messages) != 1 || req.Messages[0].Role != "user" {
		t.Fatalf("messages=%+v", req.Messages)
	}
	if req.Messages[0].Content[0].Type != "text" || req.Messages[0].Content[0].Text != "Hello" {
		t.Fatalf("content=%+v", req.Messages[0].Content)
	}
	if req.MaxTokens != 100 {
		t.Fatalf("max_tokens=%d", req.MaxTokens)
	}
	if req.Temperature == nil || *req.Temperature != 0.5 {
		t.Fatalf("temperature=%v", req.Temperature)
	}
}

func TestDecodeRequest_ImageAndToolCallAndToolResult(t *testing.T) {
	body := []byte(`{
		"model":"gpt-4o",
		"messages":[
			{"role":"user","content":[
				{"type":"text","text":"What is this?"},
				{"type":"image_url","image_url":{"url":"https://x/a.png"}}
			]},
			{"role":"assistant","content":"Sure","tool_calls":[
				{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"SF\"}"}}
			]},
			{"role":"tool","tool_call_id":"call_1","content":"sunny"}
		],
		"tools":[{"type":"function","function":{"name":"get_weather","description":"w","parameters":{"type":"object"}}}],
		"tool_choice":"auto",
		"stop":["END"]
	}`)
	req, err := DecodeRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	// user with text + image
	u := req.Messages[0]
	if u.Content[0].Type != "text" || u.Content[0].Text != "What is this?" {
		t.Fatalf("user[0]=%+v", u.Content[0])
	}
	if u.Content[1].Type != "image" || u.Content[1].Image.URL != "https://x/a.png" {
		t.Fatalf("user[1]=%+v", u.Content[1])
	}
	// assistant with text + tool_use
	a := req.Messages[1]
	if a.Role != "assistant" || a.Content[0].Type != "text" || a.Content[0].Text != "Sure" {
		t.Fatalf("asst text=%+v", a.Content[0])
	}
	if a.Content[1].Type != "tool_use" || a.Content[1].ToolUse.ID != "call_1" || a.Content[1].ToolUse.Name != "get_weather" {
		t.Fatalf("asst tool_use=%+v", a.Content[1])
	}
	var inp map[string]string
	_ = json.Unmarshal(a.Content[1].ToolUse.Input, &inp)
	if inp["city"] != "SF" {
		t.Fatalf("tool input=%s", a.Content[1].ToolUse.Input)
	}
	// tool result mapped to user message with tool_result block
	tr := req.Messages[2]
	if tr.Role != "user" || tr.Content[0].Type != "tool_result" {
		t.Fatalf("tool role=%+v", tr)
	}
	if tr.Content[0].ToolResult.ToolUseID != "call_1" || tr.Content[0].ToolResult.Content[0].Text != "sunny" {
		t.Fatalf("tool_result=%+v", tr.Content[0].ToolResult)
	}
	// tools
	if len(req.Tools) != 1 || req.Tools[0].Name != "get_weather" {
		t.Fatalf("tools=%+v", req.Tools)
	}
	// tool_choice
	if req.ToolChoice == nil || req.ToolChoice.Type != "auto" {
		t.Fatalf("tool_choice=%+v", req.ToolChoice)
	}
	// stop
	if len(req.Stop) != 1 || req.Stop[0] != "END" {
		t.Fatalf("stop=%+v", req.Stop)
	}
}

func TestDecodeRequest_ExtraFields(t *testing.T) {
	body := []byte(`{"model":"gpt-4o","messages":[],"top_k":40,"logprobs":true}`)
	req, err := DecodeRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if req.Extra["top_k"] != float64(40) {
		t.Fatalf("extra top_k=%v", req.Extra["top_k"])
	}
	if req.Extra["logprobs"] != true {
		t.Fatalf("extra logprobs=%v", req.Extra["logprobs"])
	}
}

func _useTranslate() { _ = translate.Request{} }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/translate/openai/`
Expected: FAIL — `DecodeRequest` undefined

- [ ] **Step 3: Write DecodeRequest implementation**

Create `internal/translate/openai/decode.go`:

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/translate/openai/`
Expected: PASS (3 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/translate/openai/decode.go internal/translate/openai/decode_test.go
git commit -m "feat(openai): implement DecodeRequest"
```

---

### Task 4: OpenAI EncodeRequest

**Files:**
- Modify: `internal/translate/openai/encode.go` (create)
- Test: `internal/translate/openai/encode_test.go`

**Interfaces:**
- Consumes: `*translate.Request`
- Produces: `func EncodeRequest(req *translate.Request) ([]byte, error)`

- [ ] **Step 1: Write the failing test**

Create `internal/translate/openai/encode_test.go`:

```go
package openai

import (
	"encoding/json"
	"testing"

	"github.com/great-magician-01/any-llm/internal/translate"
)

func TestEncodeRequest_RoundTripText(t *testing.T) {
	temp := 0.5
	req := &translate.Request{
		Model:       "gpt-4o",
		System:      []translate.TextBlock{{Text: "You are helpful"}},
		Messages:    []translate.Message{{Role: "user", Content: []translate.ContentBlock{{Type: "text", Text: "Hello"}}}},
		MaxTokens:   100,
		Temperature: &temp,
		Stop:        []string{"END"},
	}
	out, err := EncodeRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	var got rawRequest
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got.Model != "gpt-4o" || got.MaxTokens != 100 {
		t.Fatalf("got=%+v", got)
	}
	// system becomes messages[0]
	if got.Messages[0].Role != "system" {
		t.Fatalf("messages=%+v", got.Messages)
	}
	if decodeString(got.Messages[0].Content) != "You are helpful" {
		t.Fatalf("system content=%s", got.Messages[0].Content)
	}
	if got.Messages[1].Role != "user" || decodeString(got.Messages[1].Content) != "Hello" {
		t.Fatalf("user content=%s", got.Messages[1].Content)
	}
	// stop as array
	var arr []string
	_ = json.Unmarshal(got.Stop, &arr)
	if len(arr) != 1 || arr[0] != "END" {
		t.Fatalf("stop=%s", got.Stop)
	}
}

func TestEncodeRequest_ToolUseAndResult(t *testing.T) {
	req := &translate.Request{
		Model: "gpt-4o",
		Messages: []translate.Message{
			{Role: "assistant", Content: []translate.ContentBlock{
				{Type: "text", Text: "Sure"},
				{Type: "tool_use", ToolUse: &translate.ToolUse{ID: "call_1", Name: "get_weather", Input: json.RawMessage(`{"city":"SF"}`)}},
			}},
			{Role: "user", Content: []translate.ContentBlock{
				{Type: "tool_result", ToolResult: &translate.ToolResult{ToolUseID: "call_1", Content: []translate.ContentBlock{{Type: "text", Text: "sunny"}}}},
			}},
		},
		Tools: []translate.Tool{{Name: "get_weather", Description: "w", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		ToolChoice: &translate.ToolChoice{Type: "tool", Name: "get_weather"},
	}
	out, err := EncodeRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	var got rawRequest
	_ = json.Unmarshal(out, &got)
	// assistant: content + tool_calls
	asst := got.Messages[0]
	if asst.Role != "assistant" || decodeString(asst.Content) != "Sure" {
		t.Fatalf("asst=%+v", asst)
	}
	if len(asst.ToolCalls) != 1 || asst.ToolCalls[0].ID != "call_1" || asst.ToolCalls[0].Function.Name != "get_weather" {
		t.Fatalf("tool_calls=%+v", asst.ToolCalls)
	}
	// tool_result user message -> role:tool
	tool := got.Messages[1]
	if tool.Role != "tool" || tool.ToolCallID != "call_1" || decodeString(tool.Content) != "sunny" {
		t.Fatalf("tool msg=%+v", tool)
	}
	// tools
	if len(got.Tools) != 1 || got.Tools[0].Function.Name != "get_weather" {
		t.Fatalf("tools=%+v", got.Tools)
	}
	// tool_choice object form
	var tc struct {
		Type     string `json:"type"`
		Function struct{ Name string `json:"name"` } `json:"function"`
	}
	_ = json.Unmarshal(got.ToolChoice, &tc)
	if tc.Type != "function" || tc.Function.Name != "get_weather" {
		t.Fatalf("tool_choice=%s", got.ToolChoice)
	}
}

func TestEncodeRequest_ExtraMerged(t *testing.T) {
	req := &translate.Request{
		Model:    "gpt-4o",
		Messages: []translate.Message{},
		Extra:    map[string]any{"top_k": 40},
	}
	out, err := EncodeRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal(out, &got)
	if got["top_k"] != float64(40) {
		t.Fatalf("extra not merged: %v", got["top_k"])
	}
}

func TestEncodeResponse_TextAndToolUse(t *testing.T) {
	resp := &translate.Response{
		ID:    "c1",
		Model: "gpt-4o",
		Content: []translate.ContentBlock{
			{Type: "text", Text: "Hi"},
			{Type: "tool_use", ToolUse: &translate.ToolUse{ID: "call_1", Name: "get_weather", Input: json.RawMessage(`{"city":"SF"}`)}},
		},
		StopReason: "tool_calls",
		Usage:      translate.Usage{InputTokens: 10, OutputTokens: 5},
	}
	out, err := EncodeResponse(resp)
	if err != nil {
		t.Fatal(err)
	}
	var got rawResponse
	_ = json.Unmarshal(out, &got)
	if got.ID != "c1" || got.Model != "gpt-4o" {
		t.Fatalf("id/model=%+v", got)
	}
	if got.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("finish=%q", got.Choices[0].FinishReason)
	}
	if decodeString(got.Choices[0].Message.Content) != "Hi" {
		t.Fatalf("content=%s", got.Choices[0].Message.Content)
	}
	if len(got.Choices[0].Message.ToolCalls) != 1 || got.Choices[0].Message.ToolCalls[0].Function.Name != "get_weather" {
		t.Fatalf("tool_calls=%+v", got.Choices[0].Message.ToolCalls)
	}
	if got.Usage == nil || got.Usage.PromptTokens != 10 || got.Usage.CompletionTokens != 5 {
		t.Fatalf("usage=%+v", got.Usage)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/translate/openai/`
Expected: FAIL — `EncodeRequest` undefined

- [ ] **Step 3: Write EncodeRequest implementation**

Create `internal/translate/openai/encode.go`:

```go
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
		case "auto", "none":
			out["tool_choice"] = req.ToolChoice.Type
		case "tool":
			out["tool_choice"] = map[string]any{
				"type": "function",
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
		rr.Usage = &rawUsage{
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
			TotalTokens:      resp.Usage.InputTokens + resp.Usage.OutputTokens,
		}
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
```

Note: `EncodeResponse` is included here because it shares helpers (`mapStopReasonToOpenAI`); it is tested in this task's `encode_test.go` (`TestEncodeResponse_TextAndToolUse`).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/translate/openai/`
Expected: PASS (encode tests pass; `EncodeResponse` compiles, tested next)

- [ ] **Step 5: Commit**

```bash
git add internal/translate/openai/encode.go internal/translate/openai/encode_test.go
git commit -m "feat(openai): implement EncodeRequest and EncodeResponse"
```

---

### Task 5: OpenAI DecodeResponse

**Files:**
- Modify: `internal/translate/openai/decode.go` (append `DecodeResponse`)
- Test: append to `internal/translate/openai/decode_test.go`

**Interfaces:**
- Produces: `func DecodeResponse(body []byte) (*translate.Response, error)`

- [ ] **Step 1: Write the failing test**

Append to `internal/translate/openai/decode_test.go`:

```go
func TestDecodeResponse_TextAndUsage(t *testing.T) {
	body := []byte(`{
		"id":"chatcmpl-1","model":"gpt-4o",
		"choices":[{"index":0,"message":{"role":"assistant","content":"Hi"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}
	}`)
	resp, err := DecodeResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.ID != "chatcmpl-1" || resp.Model != "gpt-4o" {
		t.Fatalf("id/model=%+v", resp)
	}
	if len(resp.Content) != 1 || resp.Content[0].Type != "text" || resp.Content[0].Text != "Hi" {
		t.Fatalf("content=%+v", resp.Content)
	}
	if resp.StopReason != "stop" {
		t.Fatalf("stop_reason=%q", resp.StopReason)
	}
	if resp.Usage.InputTokens != 10 || resp.Usage.OutputTokens != 5 {
		t.Fatalf("usage=%+v", resp.Usage)
	}
}

func TestDecodeResponse_ToolCalls(t *testing.T) {
	body := []byte(`{
		"id":"chatcmpl-2","model":"gpt-4o",
		"choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[
			{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"SF\"}"}}
		]},"finish_reason":"tool_calls"}],
		"usage":{"prompt_tokens":8,"completion_tokens":12,"total_tokens":20}
	}`)
	resp, err := DecodeResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != "tool_calls" {
		t.Fatalf("stop_reason=%q", resp.StopReason)
	}
	if len(resp.Content) != 1 || resp.Content[0].Type != "tool_use" || resp.Content[0].ToolUse.Name != "get_weather" {
		t.Fatalf("content=%+v", resp.Content)
	}
	if resp.Usage.OutputTokens != 12 {
		t.Fatalf("usage=%+v", resp.Usage)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/translate/openai/`
Expected: FAIL — `DecodeResponse` undefined

- [ ] **Step 3: Write DecodeResponse implementation**

Append to `internal/translate/openai/decode.go`:

```go
func DecodeResponse(body []byte) (*translate.Response, error) {
	var rr rawResponse
	if err := json.Unmarshal(body, &rr); err != nil {
		return nil, fmt.Errorf("openai decode response: %w", err)
	}
	resp := &translate.Response{
		ID:         rr.ID,
		Model:      rr.Model,
		StopReason: mapStopReasonFromOpenAI(firstFinishReason(rr.Choices)),
	}
	if len(rr.Choices) > 0 {
		msg := rr.Choices[0].Message
		if msg.Content != "" {
			resp.Content = append(resp.Content, translate.ContentBlock{Type: "text", Text: msg.Content})
		}
		for _, tc := range msg.ToolCalls {
			resp.Content = append(resp.Content, translate.ContentBlock{
				Type: "tool_use",
				ToolUse: &translate.ToolUse{
					ID:    tc.ID,
					Name:  tc.Function.Name,
					Input: json.RawMessage(tc.Function.Arguments),
				},
			})
		}
	}
	if rr.Usage != nil {
		resp.Usage.InputTokens = rr.Usage.PromptTokens
		resp.Usage.OutputTokens = rr.Usage.CompletionTokens
	}
	return resp, nil
}

func firstFinishReason(choices []rawChoice) string {
	if len(choices) == 0 {
		return ""
	}
	return choices[0].FinishReason
}

func mapStopReasonFromOpenAI(reason string) string {
	switch reason {
	case "stop":
		return "stop"
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_calls"
	case "content_filter":
		return "content_filter"
	}
	return reason
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/translate/openai/`
Expected: PASS (all openai decode + encode tests)

- [ ] **Step 5: Commit**

```bash
git add internal/translate/openai/decode.go internal/translate/openai/decode_test.go
git commit -m "feat(openai): implement DecodeResponse"
```

---

### Task 6: OpenAI stream decoder (OpenAI SSE → IR events)

**Files:**
- Create: `internal/translate/openai/stream.go`
- Test: `internal/translate/openai/stream_test.go`

**Interfaces:**
- Produces: `type StreamDecoder struct`; `func NewStreamDecoder() *StreamDecoder`; `func (d *StreamDecoder) Decode(data []byte) ([]*translate.StreamEvent, error)` — consumes one `data:` JSON payload (without the `data: ` prefix, already stripped by the SSE reader in Plan B). Returns 0..N IR events. A `data: [DONE]` is signaled by passing `[]byte("[DONE]")` and returns a single `message_stop` event.

The decoder is stateful because OpenAI lacks explicit block start/stop markers; it synthesizes them:
- first chunk with `delta.role == "assistant"` → `message_start` + `content_block_start(text, index 0)`
- `delta.content` non-empty → `content_block_delta(text_delta)` on the open text block (index 0)
- `delta.tool_calls` with a new tool index → `content_block_stop` for the text block (if it was opened), then `content_block_start(tool_use)` at the tool's index
- `delta.tool_calls` with an existing index (argument fragments) → `content_block_delta(input_json_delta)`
- `finish_reason` set → `content_block_stop` for the open block, then `message_delta(stop_reason)`
- `usage` present (separate final chunk, choices empty) → update pending usage; emit in the `message_delta` (add `OutputTokens`)
- `[DONE]` → `message_stop`

- [ ] **Step 1: Write the failing test**

Create `internal/translate/openai/stream_test.go`:

```go
package openai

import (
	"testing"

	"github.com/great-magician-01/any-llm/internal/translate"
)

func TestStreamDecode_TextFlow(t *testing.T) {
	d := NewStreamDecoder()
	var got []*translate.StreamEvent

	evs, err := d.Decode([]byte(`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, evs...)
	evs, err = d.Decode([]byte(`{"id":"c1","choices":[{"index":0,"delta":{"content":" world"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, evs...)
	fr := "stop"
	evs, err = d.Decode([]byte(`{"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`))
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, evs...)
	evs, err = d.Decode([]byte("[DONE]"))
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, evs...)
	_ = fr

	// Expect: message_start, content_block_start(text,0), content_block_delta(text), content_block_delta(text),
	//         content_block_stop(0), message_delta(stop, usage), message_stop
	wantTypes := []string{
		"message_start", "content_block_start", "content_block_delta", "content_block_delta",
		"content_block_stop", "message_delta", "message_stop",
	}
	if len(got) != len(wantTypes) {
		t.Fatalf("event count=%d want %d", len(got), len(wantTypes))
	}
	for i, w := range wantTypes {
		if got[i].Type != w {
			t.Fatalf("event[%d]=%q want %q", i, got[i].Type, w)
		}
	}
	if got[0].Model != "gpt-4o" || got[0].MessageID != "c1" {
		t.Fatalf("message_start=%+v", got[0])
	}
	// text deltas
	if got[2].Delta.Text != "Hello" || got[3].Delta.Text != " world" {
		t.Fatalf("deltas=%q %q", got[2].Delta.Text, got[3].Delta.Text)
	}
	// message_delta usage
	if got[5].StopReason != "stop" || got[5].OutputTokens != 2 || got[5].InputTokens != 3 {
		t.Fatalf("message_delta=%+v", got[5])
	}
}

func TestStreamDecode_ToolUseFlow(t *testing.T) {
	d := NewStreamDecoder()
	var got []*translate.StreamEvent
	dec := func(s string) {
		evs, err := d.Decode([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, evs...)
	}
	dec(`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`)
	dec(`{"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":""}}]}}]}`)
	dec(`{"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":\"SF\"}"}}]}}]}`)
	dec(`{"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"completion_tokens":10,"total_tokens":15}}`)
	dec("[DONE]")

	// Find the tool_use content_block_start
	var toolStart *translate.StreamEvent
	for _, e := range got {
		if e.Type == "content_block_start" && e.Block != nil && e.Block.Type == "tool_use" {
			toolStart = e
		}
	}
	if toolStart == nil || toolStart.Block.ToolUse.ID != "call_1" || toolStart.Block.ToolUse.Name != "get_weather" {
		t.Fatalf("no tool_use start: %+v", toolStart)
	}
	// input_json_delta
	var jsonDelta *translate.StreamEvent
	for _, e := range got {
		if e.Type == "content_block_delta" && e.Delta != nil && e.Delta.Type == "input_json_delta" {
			jsonDelta = e
		}
	}
	if jsonDelta == nil || jsonDelta.Delta.PartialJSON != `{"city":"SF"}` {
		t.Fatalf("no json delta: %+v", jsonDelta)
	}
	if got[len(got)-2].StopReason != "tool_calls" {
		t.Fatalf("stop_reason=%+v", got[len(got)-2])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/translate/openai/`
Expected: FAIL — `NewStreamDecoder` undefined

- [ ] **Step 3: Write the stream decoder**

Create `internal/translate/openai/stream.go`:

```go
package openai

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/great-magician-01/any-llm/internal/translate"
)

type StreamDecoder struct {
	started     bool
	textOpen    bool // a text block at index 0 is open
	textIndex   int
	toolOpenIdx int  // which tool_calls index is currently open (-1 = none)
	toolBlock   int  // IR block index for the open tool block
	nextBlock   int  // next IR block index to allocate
	inputTokens int
}

func NewStreamDecoder() *StreamDecoder {
	return &StreamDecoder{toolOpenIdx: -1, nextBlock: 1}
}

// Decode consumes one SSE data payload (without "data: " prefix).
// Pass []byte("[DONE]") to signal stream end.
func (d *StreamDecoder) Decode(data []byte) ([]*translate.StreamEvent, error) {
	if strings.TrimSpace(string(data)) == "[DONE]" {
		var evs []*translate.StreamEvent
		if d.textOpen {
			evs = append(evs, &translate.StreamEvent{Type: "content_block_stop", Index: d.textIndex})
			d.textOpen = false
		}
		if d.toolOpenIdx >= 0 {
			evs = append(evs, &translate.StreamEvent{Type: "content_block_stop", Index: d.toolBlock})
			d.toolOpenIdx = -1
		}
		evs = append(evs, &translate.StreamEvent{Type: "message_stop"})
		return evs, nil
	}

	var ch rawChunk
	if err := json.Unmarshal(data, &ch); err != nil {
		return nil, fmt.Errorf("openai stream decode: %w", err)
	}
	var evs []*translate.StreamEvent

	// message_start on first chunk that has a role or model
	if !d.started && ((len(ch.Choices) > 0 && ch.Choices[0].Delta.Role != "") || ch.Model != "") {
		d.started = true
		evs = append(evs, &translate.StreamEvent{
			Type:      "message_start",
			MessageID: ch.ID,
			Model:     ch.Model,
		})
	}

	// usage-only chunk (choices empty) — record and emit into a message_delta if finish already sent
	if len(ch.Choices) == 0 {
		if ch.Usage != nil {
			d.inputTokens = ch.Usage.PromptTokens
		}
		return evs, nil
	}

	c := ch.Choices[0]

	// text content delta
	if c.Delta.Content != "" {
		if !d.textOpen {
			d.textOpen = true
			d.textIndex = 0
			evs = append(evs, &translate.StreamEvent{
				Type:  "content_block_start",
				Index: 0,
				Block: &translate.ContentBlock{Type: "text"},
			})
		}
		evs = append(evs, &translate.StreamEvent{
			Type:  "content_block_delta",
			Index: 0,
			Delta: &translate.Delta{Type: "text_delta", Text: c.Delta.Content},
		})
	}

	// tool_calls
	for _, tc := range c.Delta.ToolCalls {
		if tc.ID != "" {
			// new tool call: close text block if open
			if d.textOpen {
				evs = append(evs, &translate.StreamEvent{Type: "content_block_stop", Index: d.textIndex})
				d.textOpen = false
			}
			if d.toolOpenIdx >= 0 {
				evs = append(evs, &translate.StreamEvent{Type: "content_block_stop", Index: d.toolBlock})
			}
			d.toolOpenIdx = tcIndex(tc) // OpenAI tool_calls index (0-based)
			d.toolBlock = d.nextBlock
			d.nextBlock++
			evs = append(evs, &translate.StreamEvent{
				Type:  "content_block_start",
				Index: d.toolBlock,
				Block: &translate.ContentBlock{
					Type:    "tool_use",
					ToolUse: &translate.ToolUse{ID: tc.ID, Name: tc.Function.Name, Input: json.RawMessage("{}")},
				},
			})
		} else if d.toolOpenIdx >= 0 {
			// argument fragment
			evs = append(evs, &translate.StreamEvent{
				Type:  "content_block_delta",
				Index: d.toolBlock,
				Delta: &translate.Delta{Type: "input_json_delta", PartialJSON: tc.Function.Arguments},
			})
		}
	}

	// finish_reason
	if c.FinishReason != nil && *c.FinishReason != "" {
		if d.textOpen {
			evs = append(evs, &translate.StreamEvent{Type: "content_block_stop", Index: d.textIndex})
			d.textOpen = false
		}
		if d.toolOpenIdx >= 0 {
			evs = append(evs, &translate.StreamEvent{Type: "content_block_stop", Index: d.toolBlock})
			d.toolOpenIdx = -1
		}
		md := &translate.StreamEvent{
			Type:       "message_delta",
			StopReason: mapStopReasonFromOpenAI(*c.FinishReason),
		}
		if ch.Usage != nil {
			md.InputTokens = ch.Usage.PromptTokens
			md.OutputTokens = ch.Usage.CompletionTokens
		} else if d.inputTokens > 0 {
			md.InputTokens = d.inputTokens
		}
		evs = append(evs, md)
	}

	return evs, nil
}

// tcIndex extracts the OpenAI tool_calls array index from a delta entry.
// The raw struct doesn't carry index; it is the position in the slice.
// We rely on the caller having one tool call per chunk for the "new" case.
func tcIndex(tc rawToolCall) int { return 0 }
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/translate/openai/`
Expected: PASS (stream tests + all prior)

- [ ] **Step 5: Commit**

```bash
git add internal/translate/openai/stream.go internal/translate/openai/stream_test.go
git commit -m "feat(openai): implement stream decoder (OpenAI SSE -> IR events)"
```

---

### Task 7: OpenAI stream encoder (IR events → OpenAI SSE)

**Files:**
- Modify: `internal/translate/openai/stream.go` (append `StreamEncoder`)
- Test: append to `internal/translate/openai/stream_test.go`

**Interfaces:**
- Produces: `type StreamEncoder struct`; `func NewStreamEncoder(model string) *StreamEncoder`; `func (e *StreamEncoder) Encode(evt *translate.StreamEvent) ([][]byte, error)` — returns 0..N `data: <json>\n\n` lines (each element is the full SSE frame including `data: ` prefix and trailing `\n\n`).

Encoder collapses Anthropic-style block events into OpenAI delta chunks:
- `message_start` → `data: {id, model, choices:[{index:0,delta:{role:"assistant",content:""}}]}` (one chunk)
- `content_block_delta(text_delta)` → `data: {choices:[{index:0,delta:{content:"..."}}]}`
- `content_block_start(tool_use)` → `data: {choices:[{index:0,delta:{tool_calls:[{index, id, type:function, function:{name, arguments:""}}]}}]}`
- `content_block_delta(input_json_delta)` → `data: {choices:[{index:0,delta:{tool_calls:[{index, function:{arguments: partial}}]}}]}`
- `message_delta(stop_reason)` → `data: {choices:[{index:0,delta:{},finish_reason: mapped}]}` (plus `usage` if tokens present)
- `message_stop` → `data: [DONE]\n\n`
- `content_block_start(text)` → no output (OpenAI has no explicit text block start)
- `content_block_stop` → no output (OpenAI has no explicit block stop; stop emitted on finish)

State: track the OpenAI tool index counter and a map from IR block index → OpenAI tool index.

- [ ] **Step 1: Write the failing test**

Append to `internal/translate/openai/stream_test.go`:

```go
func TestStreamEncode_TextFlow(t *testing.T) {
	e := NewStreamEncoder("gpt-4o")
	var lines []string
	enc := func(evt *translate.StreamEvent) {
		fs, err := e.Encode(evt)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range fs {
			lines = append(lines, string(f))
		}
	}
	enc(&translate.StreamEvent{Type: "message_start", MessageID: "m1", Model: "gpt-4o"})
	enc(&translate.StreamEvent{Type: "content_block_start", Index: 0, Block: &translate.ContentBlock{Type: "text"}})
	enc(&translate.StreamEvent{Type: "content_block_delta", Index: 0, Delta: &translate.Delta{Type: "text_delta", Text: "Hi"}})
	enc(&translate.StreamEvent{Type: "content_block_stop", Index: 0})
	enc(&translate.StreamEvent{Type: "message_delta", StopReason: "stop", InputTokens: 3, OutputTokens: 2})
	enc(&translate.StreamEvent{Type: "message_stop"})

	// first line: role assistant
	if !strings.Contains(lines[0], `"role":"assistant"`) {
		t.Fatalf("line0=%s", lines[0])
	}
	// second: content Hi
	if !strings.Contains(lines[1], `"content":"Hi"`) {
		t.Fatalf("line1=%s", lines[1])
	}
	// finish_reason stop
	found := false
	for _, l := range lines {
		if strings.Contains(l, `"finish_reason":"stop"`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("no finish_reason: %v", lines)
	}
	// [DONE]
	if !strings.HasSuffix(lines[len(lines)-1], "data: [DONE]\n\n") {
		t.Fatalf("last line=%q", lines[len(lines)-1])
	}
}

func TestStreamEncode_ToolUseFlow(t *testing.T) {
	e := NewStreamEncoder("gpt-4o")
	var lines []string
	enc := func(evt *translate.StreamEvent) {
		fs, err := e.Encode(evt)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range fs {
			lines = append(lines, string(f))
		}
	}
	enc(&translate.StreamEvent{Type: "message_start", MessageID: "m1"})
	enc(&translate.StreamEvent{Type: "content_block_start", Index: 1, Block: &translate.ContentBlock{Type: "tool_use", ToolUse: &translate.ToolUse{ID: "call_1", Name: "get_weather"}}})
	enc(&translate.StreamEvent{Type: "content_block_delta", Index: 1, Delta: &translate.Delta{Type: "input_json_delta", PartialJSON: `{"city":"SF"}`}})
	enc(&translate.StreamEvent{Type: "content_block_stop", Index: 1})
	enc(&translate.StreamEvent{Type: "message_delta", StopReason: "tool_calls"})
	enc(&translate.StreamEvent{Type: "message_stop"})

	joined := strings.Join(lines, "")
	if !strings.Contains(joined, `"name":"get_weather"`) || !strings.Contains(joined, `"id":"call_1"`) {
		t.Fatalf("tool start missing: %s", joined)
	}
	if !strings.Contains(joined, `"arguments":"{\"city\":\"SF\"}"`) {
		t.Fatalf("tool args missing: %s", joined)
	}
	if !strings.Contains(joined, `"finish_reason":"tool_calls"`) {
		t.Fatalf("no tool_calls finish: %s", joined)
	}
}
```

Add `"strings"` to the import block of `stream_test.go` if not present.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/translate/openai/`
Expected: FAIL — `NewStreamEncoder` undefined

- [ ] **Step 3: Write the stream encoder**

Append to `internal/translate/openai/stream.go`:

```go
type StreamEncoder struct {
	model     string
	id        string
	toolIdx   map[int]int // IR block index -> OpenAI tool index
	nextTool  int
}

func NewStreamEncoder(model string) *StreamEncoder {
	return &StreamEncoder{model: model, toolIdx: map[int]int{}}
}

func (e *StreamEncoder) Encode(evt *translate.StreamEvent) ([][]byte, error) {
	switch evt.Type {
	case "message_start":
		e.id = evt.MessageID
		if e.id == "" {
			e.id = "chatcmpl-anylem"
		}
		ch := map[string]any{
			"id":      e.id,
			"object":  "chat.completion.chunk",
			"choices": []map[string]any{{"index": 0, "delta": map[string]any{"role": "assistant", "content": ""}}},
		}
		if e.model != "" || evt.Model != "" {
			m := evt.Model
			if m == "" {
				m = e.model
			}
			ch["model"] = m
			e.model = m
		}
		return [][]byte{frame(ch)}, nil

	case "content_block_start":
		if evt.Block != nil && evt.Block.Type == "tool_use" {
			idx := e.nextTool
			e.nextTool++
			e.toolIdx[evt.Index] = idx
			ch := map[string]any{
				"id": e.id, "object": "chat.completion.chunk",
				"choices": []map[string]any{{"index": 0, "delta": map[string]any{
					"tool_calls": []map[string]any{{
						"index": idx, "id": evt.Block.ToolUse.ID, "type": "function",
						"function": map[string]any{"name": evt.Block.ToolUse.Name, "arguments": ""},
					}},
				}}},
			}
			return [][]byte{frame(ch)}, nil
		}
		// text block start -> no OpenAI output
		return nil, nil

	case "content_block_delta":
		if evt.Delta == nil {
			return nil, nil
		}
		switch evt.Delta.Type {
		case "text_delta":
			ch := map[string]any{
				"id": e.id, "object": "chat.completion.chunk",
				"choices": []map[string]any{{"index": 0, "delta": map[string]any{"content": evt.Delta.Text}}},
			}
			return [][]byte{frame(ch)}, nil
		case "input_json_delta":
			idx, ok := e.toolIdx[evt.Index]
			if !ok {
				idx = 0
			}
			ch := map[string]any{
				"id": e.id, "object": "chat.completion.chunk",
				"choices": []map[string]any{{"index": 0, "delta": map[string]any{
					"tool_calls": []map[string]any{{
						"index": idx,
						"function": map[string]any{"arguments": evt.Delta.PartialJSON},
					}},
				}}},
			}
			return [][]byte{frame(ch)}, nil
		}

	case "content_block_stop":
		return nil, nil

	case "message_delta":
		choice := map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": mapStopReasonToOpenAI(evt.StopReason)}
		ch := map[string]any{"id": e.id, "object": "chat.completion.chunk", "choices": []map[string]any{choice}}
		if evt.InputTokens > 0 || evt.OutputTokens > 0 {
			ch["usage"] = map[string]any{
				"prompt_tokens": evt.InputTokens, "completion_tokens": evt.OutputTokens,
				"total_tokens": evt.InputTokens + evt.OutputTokens,
			}
		}
		return [][]byte{frame(ch)}, nil

	case "message_stop":
		return [][]byte{[]byte("data: [DONE]\n\n")}, nil
	}
	return nil, nil
}

func frame(v any) []byte {
	b, _ := json.Marshal(v)
	return []byte("data: " + string(b) + "\n\n")
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/translate/openai/`
Expected: PASS (all openai tests)

- [ ] **Step 5: Commit**

```bash
git add internal/translate/openai/stream.go internal/translate/openai/stream_test.go
git commit -m "feat(openai): implement stream encoder (IR events -> OpenAI SSE)"
```

---

### Task 8: Anthropic raw wire types

**Files:**
- Create: `internal/translate/anthropic/types.go`

**Interfaces:**
- Produces: internal raw structs for Anthropic wire format.

- [ ] **Step 1: Write the Anthropic wire structs**

Create `internal/translate/anthropic/types.go`:

```go
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
	ID         string             `json:"id"`
	Model      string             `json:"model"`
	Content    []json.RawMessage  `json:"content"`
	StopReason string             `json:"stop_reason"`
	Usage      rawUsage           `json:"usage"`
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
	Usage        *rawUsage       `json:"usage,omitempty"`
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/translate/anthropic/`
Expected: no output

- [ ] **Step 3: Commit**

```bash
git add internal/translate/anthropic/types.go
git commit -m "feat(anthropic): add raw wire structs"
```

---

### Task 9: Anthropic DecodeRequest

**Files:**
- Create: `internal/translate/anthropic/decode.go`
- Test: `internal/translate/anthropic/decode_test.go`

**Interfaces:**
- Produces: `func DecodeRequest(body []byte) (*translate.Request, error)`

- [ ] **Step 1: Write the failing test**

Create `internal/translate/anthropic/decode_test.go`:

```go
package anthropic

import (
	"encoding/json"
	"testing"

	"github.com/great-magician-01/any-llm/internal/translate"
)

func TestDecodeRequest_TextSystem(t *testing.T) {
	body := []byte(`{
		"model":"claude-3-5",
		"system":"You are helpful",
		"messages":[{"role":"user","content":"Hello"}],
		"max_tokens":100,
		"temperature":0.5,
		"stop_sequences":["END"]
	}`)
	req, err := DecodeRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if req.Model != "claude-3-5" {
		t.Fatalf("model=%q", req.Model)
	}
	if len(req.System) != 1 || req.System[0].Text != "You are helpful" {
		t.Fatalf("system=%+v", req.System)
	}
	if len(req.Messages) != 1 || req.Messages[0].Content[0].Type != "text" || req.Messages[0].Content[0].Text != "Hello" {
		t.Fatalf("messages=%+v", req.Messages)
	}
	if req.MaxTokens != 100 || req.Stop[0] != "END" {
		t.Fatalf("max/stop=%d %v", req.MaxTokens, req.Stop)
	}
}

func TestDecodeRequest_ImageToolUseToolResult(t *testing.T) {
	body := []byte(`{
		"model":"claude-3-5",
		"messages":[
			{"role":"user","content":[
				{"type":"text","text":"look"},
				{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAA"}}
			]},
			{"role":"assistant","content":[
				{"type":"text","text":"ok"},
				{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{"city":"SF"}}
			]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"toolu_1","content":"sunny"}
			]}
		],
		"tools":[{"name":"get_weather","description":"w","input_schema":{"type":"object"}}],
		"tool_choice":{"type":"tool","name":"get_weather"}
	}`)
	req, err := DecodeRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	u := req.Messages[0].Content
	if u[0].Type != "text" || u[1].Type != "image" || u[1].Image.Base64 != "AAA" || u[1].Image.MediaType != "image/png" {
		t.Fatalf("user=%+v %+v", u[0], u[1])
	}
	a := req.Messages[1].Content
	if a[1].Type != "tool_use" || a[1].ToolUse.ID != "toolu_1" || a[1].ToolUse.Name != "get_weather" {
		t.Fatalf("asst tool_use=%+v", a[1])
	}
	var inp map[string]string
	_ = json.Unmarshal(a[1].ToolUse.Input, &inp)
	if inp["city"] != "SF" {
		t.Fatalf("input=%s", a[1].ToolUse.Input)
	}
	tr := req.Messages[2].Content[0]
	if tr.Type != "tool_result" || tr.ToolResult.ToolUseID != "toolu_1" || tr.ToolResult.Content[0].Text != "sunny" {
		t.Fatalf("tool_result=%+v", tr.ToolResult)
	}
	if req.ToolChoice == nil || req.ToolChoice.Type != "tool" || req.ToolChoice.Name != "get_weather" {
		t.Fatalf("tool_choice=%+v", req.ToolChoice)
	}
}

func TestDecodeRequest_ExtraFields(t *testing.T) {
	body := []byte(`{"model":"c","messages":[],"max_tokens":10,"top_k":40}`)
	req, err := DecodeRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if req.Extra["top_k"] != float64(40) {
		t.Fatalf("extra=%v", req.Extra["top_k"])
	}
}

func _useT() { _ = translate.Request{} }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/translate/anthropic/`
Expected: FAIL — `DecodeRequest` undefined

- [ ] **Step 3: Write DecodeRequest**

Create `internal/translate/anthropic/decode.go`:

```go
package anthropic

import (
	"encoding/json"
	"fmt"

	"github.com/great-magician-01/any-llm/internal/translate"
)

func DecodeRequest(body []byte) (*translate.Request, error) {
	var known rawRequest
	if err := json.Unmarshal(body, &known); err != nil {
		return nil, fmt.Errorf("anthropic decode request: %w", err)
	}
	var all map[string]any
	_ = json.Unmarshal(body, &all)

	req := &translate.Request{
		Model:       known.Model,
		MaxTokens:   known.MaxTokens,
		Temperature: known.Temperature,
		TopP:        known.TopP,
		Stream:      known.Stream,
		Stop:        known.StopSequences,
	}
	// system: string or array of text blocks
	req.System = decodeSystem(known.System)
	for _, m := range known.Messages {
		blocks, err := decodeBlocks(m.Content)
		if err != nil {
			return nil, err
		}
		role := m.Role
		if role == "" {
			role = "user"
		}
		req.Messages = append(req.Messages, translate.Message{Role: role, Content: blocks})
	}
	for _, t := range known.Tools {
		req.Tools = append(req.Tools, translate.Tool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}
	if len(known.ToolChoice) > 0 {
		req.ToolChoice = decodeAnthropicToolChoice(known.ToolChoice)
	}
	req.Extra = extractExtra(all)
	return req, nil
}

func decodeSystem(raw json.RawMessage) []translate.TextBlock {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []translate.TextBlock{{Text: s}}
	}
	var parts []rawTextPart
	if err := json.Unmarshal(raw, &parts); err == nil {
		var out []translate.TextBlock
		for _, p := range parts {
			out = append(out, translate.TextBlock{Text: p.Text})
		}
		return out
	}
	return nil
}

func decodeBlocks(raw json.RawMessage) ([]translate.ContentBlock, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	// string content
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []translate.ContentBlock{{Type: "text", Text: s}}, nil
	}
	// array of typed parts
	var parts []json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, fmt.Errorf("anthropic content: %w", err)
	}
	var out []translate.ContentBlock
	for _, p := range parts {
		var head struct{ Type string `json:"type"` }
		if err := json.Unmarshal(p, &head); err != nil {
			return nil, err
		}
		switch head.Type {
		case "text":
			var tp rawTextPart
			_ = json.Unmarshal(p, &tp)
			out = append(out, translate.ContentBlock{Type: "text", Text: tp.Text})
		case "image":
			var ip rawImagePart
			_ = json.Unmarshal(p, &ip)
			out = append(out, translate.ContentBlock{Type: "image", Image: &translate.Image{
				Base64:    ip.Source.Data,
				MediaType: ip.Source.MediaType,
			}})
		case "tool_use":
			var tu rawToolUsePart
			_ = json.Unmarshal(p, &tu)
			out = append(out, translate.ContentBlock{Type: "tool_use", ToolUse: &translate.ToolUse{
				ID: tu.ID, Name: tu.Name, Input: tu.Input,
			}})
		case "tool_result":
			var tr rawToolResultPart
			_ = json.Unmarshal(p, &tr)
			out = append(out, translate.ContentBlock{Type: "tool_result", ToolResult: &translate.ToolResult{
				ToolUseID: tr.ToolUseID,
				Content:   decodeResultContent(tr.Content),
				IsError:   tr.IsError,
			}})
		}
	}
	return out, nil
}

func decodeResultContent(raw json.RawMessage) []translate.ContentBlock {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []translate.ContentBlock{{Type: "text", Text: s}}
	}
	blocks, _ := decodeBlocks(raw)
	return blocks
}

func decodeAnthropicToolChoice(raw json.RawMessage) *translate.ToolChoice {
	var obj struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return &translate.ToolChoice{Type: obj.Type, Name: obj.Name}
	}
	return &translate.ToolChoice{Type: "auto"}
}

var knownAnthropicKeys = map[string]bool{
	"model": true, "system": true, "messages": true, "tools": true, "tool_choice": true,
	"max_tokens": true, "temperature": true, "top_p": true, "stream": true, "stop_sequences": true,
}

func extractExtra(all map[string]any) map[string]any {
	if len(all) == 0 {
		return nil
	}
	extra := map[string]any{}
	for k, v := range all {
		if !knownAnthropicKeys[k] {
			extra[k] = v
		}
	}
	if len(extra) == 0 {
		return nil
	}
	return extra
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/translate/anthropic/`
Expected: PASS (3 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/translate/anthropic/decode.go internal/translate/anthropic/decode_test.go
git commit -m "feat(anthropic): implement DecodeRequest"
```

---

### Task 10: Anthropic EncodeRequest

**Files:**
- Create: `internal/translate/anthropic/encode.go`
- Test: `internal/translate/anthropic/encode_test.go`

**Interfaces:**
- Produces: `func EncodeRequest(req *translate.Request) ([]byte, error)`, `func EncodeResponse(resp *translate.Response) ([]byte, error)`

- [ ] **Step 1: Write the failing test**

Create `internal/translate/anthropic/encode_test.go`:

```go
package anthropic

import (
	"encoding/json"
	"testing"

	"github.com/great-magician-01/any-llm/internal/translate"
)

func TestEncodeRequest_RoundTrip(t *testing.T) {
	temp := 0.5
	req := &translate.Request{
		Model:       "claude-3-5",
		System:      []translate.TextBlock{{Text: "You are helpful"}},
		Messages:    []translate.Message{{Role: "user", Content: []translate.ContentBlock{{Type: "text", Text: "Hello"}}}},
		MaxTokens:   100,
		Temperature: &temp,
		Stop:        []string{"END"},
	}
	out, err := EncodeRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	var got rawRequest
	_ = json.Unmarshal(out, &got)
	if got.Model != "claude-3-5" || got.MaxTokens != 100 {
		t.Fatalf("got=%+v", got)
	}
	// system as string
	var sys string
	_ = json.Unmarshal(got.System, &sys)
	if sys != "You are helpful" {
		t.Fatalf("system=%s", got.System)
	}
	// user content as array of text
	var parts []map[string]any
	_ = json.Unmarshal(got.Messages[0].Content, &parts)
	if parts[0]["type"] != "text" || parts[0]["text"] != "Hello" {
		t.Fatalf("content=%+v", parts)
	}
	// stop_sequences
	if len(got.StopSequences) != 1 || got.StopSequences[0] != "END" {
		t.Fatalf("stop=%v", got.StopSequences)
	}
}

func TestEncodeRequest_ImageAndToolUse(t *testing.T) {
	req := &translate.Request{
		Model: "claude-3-5",
		Messages: []translate.Message{
			{Role: "user", Content: []translate.ContentBlock{
				{Type: "image", Image: &translate.Image{Base64: "AAA", MediaType: "image/png"}},
			}},
			{Role: "assistant", Content: []translate.ContentBlock{
				{Type: "tool_use", ToolUse: &translate.ToolUse{ID: "toolu_1", Name: "get_weather", Input: json.RawMessage(`{"city":"SF"}`)}},
			}},
			{Role: "user", Content: []translate.ContentBlock{
				{Type: "tool_result", ToolResult: &translate.ToolResult{ToolUseID: "toolu_1", Content: []translate.ContentBlock{{Type: "text", Text: "sunny"}}}},
			}},
		},
		Tools:      []translate.Tool{{Name: "get_weather", Description: "w", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		ToolChoice: &translate.ToolChoice{Type: "auto"},
		MaxTokens:  10,
	}
	out, err := EncodeRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	var got rawRequest
	_ = json.Unmarshal(out, &got)
	// image
	var imgParts []map[string]any
	_ = json.Unmarshal(got.Messages[0].Content, &imgParts)
	src := imgParts[0]["source"].(map[string]any)
	if src["data"] != "AAA" || src["media_type"] != "image/png" {
		t.Fatalf("image=%+v", imgParts[0])
	}
	// tool_use
	var asstParts []map[string]any
	_ = json.Unmarshal(got.Messages[1].Content, &asstParts)
	if asstParts[0]["type"] != "tool_use" || asstParts[0]["id"] != "toolu_1" {
		t.Fatalf("tool_use=%+v", asstParts[0])
	}
	// tool_result
	var trParts []map[string]any
	_ = json.Unmarshal(got.Messages[2].Content, &trParts)
	if trParts[0]["type"] != "tool_result" || trParts[0]["tool_use_id"] != "toolu_1" {
		t.Fatalf("tool_result=%+v", trParts[0])
	}
	// tool_choice auto
	var tc map[string]any
	_ = json.Unmarshal(got.ToolChoice, &tc)
	if tc["type"] != "auto" {
		t.Fatalf("tool_choice=%+v", tc)
	}
}

func TestEncodeRequest_ExtraMerged(t *testing.T) {
	req := &translate.Request{
		Model: "c", Messages: []translate.Message{}, MaxTokens: 10,
		Extra: map[string]any{"top_k": 40},
	}
	out, err := EncodeRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal(out, &got)
	if got["top_k"] != float64(40) {
		t.Fatalf("extra=%v", got["top_k"])
	}
}

func TestEncodeResponse_TextAndToolUse(t *testing.T) {
	resp := &translate.Response{
		ID:    "msg_1",
		Model: "claude-3-5",
		Content: []translate.ContentBlock{
			{Type: "text", Text: "Hi"},
			{Type: "tool_use", ToolUse: &translate.ToolUse{ID: "toolu_1", Name: "get_weather", Input: json.RawMessage(`{"city":"SF"}`)}},
		},
		StopReason: "stop",
		Usage:      translate.Usage{InputTokens: 10, OutputTokens: 5},
	}
	out, err := EncodeResponse(resp)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal(out, &got)
	if got["id"] != "msg_1" || got["model"] != "claude-3-5" {
		t.Fatalf("id/model=%v %v", got["id"], got["model"])
	}
	if got["stop_reason"] != "end_turn" {
		t.Fatalf("stop_reason=%v", got["stop_reason"])
	}
	content := got["content"].([]any)
	first := content[0].(map[string]any)
	if first["type"] != "text" || first["text"] != "Hi" {
		t.Fatalf("text=%+v", first)
	}
	second := content[1].(map[string]any)
	if second["type"] != "tool_use" || second["name"] != "get_weather" {
		t.Fatalf("tool_use=%+v", second)
	}
	usage := got["usage"].(map[string]any)
	if usage["input_tokens"] != float64(10) || usage["output_tokens"] != float64(5) {
		t.Fatalf("usage=%+v", usage)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/translate/anthropic/`
Expected: FAIL — `EncodeRequest` undefined

- [ ] **Step 3: Write EncodeRequest and EncodeResponse**

Create `internal/translate/anthropic/encode.go`:

```go
package anthropic

import (
	"encoding/json"
	"fmt"

	"github.com/great-magician-01/any-llm/internal/translate"
)

func EncodeRequest(req *translate.Request) ([]byte, error) {
	out := map[string]any{
		"model":      req.Model,
		"max_tokens": req.MaxTokens,
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
	// messages
	var msgs []rawMessage
	for _, m := range req.Messages {
		parts := encodeBlocks(m.Content)
		raw, _ := json.Marshal(parts)
		msgs = append(msgs, rawMessage{Role: m.Role, Content: raw})
	}
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
		var tools []rawTool
		for _, t := range req.Tools {
			tools = append(tools, rawTool{
				Name: t.Name, Description: t.Description, InputSchema: t.InputSchema,
			})
		}
		out["tools"] = tools
	}
	if req.ToolChoice != nil {
		tc := map[string]any{"type": req.ToolChoice.Type}
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
		case "tool_use":
			content = append(content, map[string]any{
				"type": "tool_use", "id": b.ToolUse.ID, "name": b.ToolUse.Name, "input": json.RawMessage(b.ToolUse.Input),
			})
		}
	}
	out := map[string]any{
		"id":           resp.ID,
		"model":        resp.Model,
		"role":         "assistant",
		"content":      content,
		"stop_reason":  mapStopReasonToAnthropic(resp.StopReason),
		"type":         "message",
		"usage": map[string]any{
			"input_tokens":  resp.Usage.InputTokens,
			"output_tokens": resp.Usage.OutputTokens,
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/translate/anthropic/`
Expected: PASS (encode tests)

- [ ] **Step 5: Commit**

```bash
git add internal/translate/anthropic/encode.go internal/translate/anthropic/encode_test.go
git commit -m "feat(anthropic): implement EncodeRequest and EncodeResponse"
```

---

### Task 11: Anthropic DecodeResponse

**Files:**
- Modify: `internal/translate/anthropic/decode.go` (append `DecodeResponse`)
- Test: append to `internal/translate/anthropic/decode_test.go`

**Interfaces:**
- Produces: `func DecodeResponse(body []byte) (*translate.Response, error)`

- [ ] **Step 1: Write the failing test**

Append to `internal/translate/anthropic/decode_test.go`:

```go
func TestDecodeResponse_TextAndToolUse(t *testing.T) {
	body := []byte(`{
		"id":"msg_1","model":"claude-3-5",
		"content":[
			{"type":"text","text":"Hi"},
			{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{"city":"SF"}}
		],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":10,"output_tokens":8}
	}`)
	resp, err := DecodeResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.ID != "msg_1" || resp.Model != "claude-3-5" {
		t.Fatalf("id/model=%+v", resp)
	}
	if len(resp.Content) != 2 {
		t.Fatalf("content len=%d", len(resp.Content))
	}
	if resp.Content[0].Type != "text" || resp.Content[0].Text != "Hi" {
		t.Fatalf("text=%+v", resp.Content[0])
	}
	if resp.Content[1].Type != "tool_use" || resp.Content[1].ToolUse.Name != "get_weather" {
		t.Fatalf("tool_use=%+v", resp.Content[1])
	}
	if resp.StopReason != "end_turn" {
		t.Fatalf("stop=%q", resp.StopReason)
	}
	if resp.Usage.InputTokens != 10 || resp.Usage.OutputTokens != 8 {
		t.Fatalf("usage=%+v", resp.Usage)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/translate/anthropic/`
Expected: FAIL — `DecodeResponse` undefined

- [ ] **Step 3: Write DecodeResponse**

Append to `internal/translate/anthropic/decode.go`:

```go
func DecodeResponse(body []byte) (*translate.Response, error) {
	var rr rawResponse
	if err := json.Unmarshal(body, &rr); err != nil {
		return nil, fmt.Errorf("anthropic decode response: %w", err)
	}
	resp := &translate.Response{
		ID:         rr.ID,
		Model:      rr.Model,
		StopReason: rr.StopReason,
		Usage: translate.Usage{
			InputTokens:  rr.Usage.InputTokens,
			OutputTokens: rr.Usage.OutputTokens,
		},
	}
	blocks, err := decodeBlocks(arrayToRaw(rr.Content))
	if err != nil {
		return nil, err
	}
	resp.Content = blocks
	return resp, nil
}

// arrayToRaw re-serializes a slice of RawMessage into a single JSON array RawMessage.
func arrayToRaw(parts []json.RawMessage) json.RawMessage {
	if len(parts) == 0 {
		return nil
	}
	b, _ := json.Marshal(parts)
	return b
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/translate/anthropic/`
Expected: PASS (all anthropic decode + encode tests)

- [ ] **Step 5: Commit**

```bash
git add internal/translate/anthropic/decode.go internal/translate/anthropic/decode_test.go
git commit -m "feat(anthropic): implement DecodeResponse"
```

---

### Task 12: Anthropic stream codec

**Files:**
- Create: `internal/translate/anthropic/stream.go`
- Test: `internal/translate/anthropic/stream_test.go`

**Interfaces:**
- Produces: `func DecodeStreamEvent(data []byte) (*translate.StreamEvent, error)` — 1:1 parse of one `data:` payload (prefix stripped). Returns nil for non-meaningful events (e.g. `ping`).
- Produces: `func EncodeStreamEvent(evt *translate.StreamEvent) ([]byte, error)` — writes one Anthropic SSE frame `event: <type>\ndata: <json>\n\n`.

Anthropic stream events are nearly 1:1 with IR, so the codec is stateless.

Decode mapping:
- `message_start` → IR `message_start` (parse nested `message.{id,model,usage.input_tokens}`)
- `content_block_start` → IR `content_block_start` (parse `content_block` into `ContentBlock`)
- `content_block_delta` → IR `content_block_delta` (parse `delta` into `Delta`: `text_delta`/`input_json_delta`)
- `content_block_stop` → IR `content_block_stop`
- `message_delta` → IR `message_delta` (parse `delta.stop_reason` and `usage.output_tokens`)
- `message_stop` → IR `message_stop`
- `ping` / others → nil

Encode mapping: inverse, plus emit `event: <type>` line.

- [ ] **Step 1: Write the failing test**

Create `internal/translate/anthropic/stream_test.go`:

```go
package anthropic

import (
	"strings"
	"testing"

	"github.com/great-magician-01/any-llm/internal/translate"
)

func TestDecodeStreamEvent_MessageStart(t *testing.T) {
	evt, err := DecodeStreamEvent([]byte(`{"type":"message_start","message":{"id":"msg_1","model":"claude-3-5","usage":{"input_tokens":10}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if evt == nil || evt.Type != "message_start" || evt.MessageID != "msg_1" || evt.Model != "claude-3-5" || evt.InputTokens != 10 {
		t.Fatalf("evt=%+v", evt)
	}
}

func TestDecodeStreamEvent_ContentBlockDelta(t *testing.T) {
	evt, err := DecodeStreamEvent([]byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if evt.Type != "content_block_delta" || evt.Delta.Text != "Hi" || evt.Delta.Type != "text_delta" {
		t.Fatalf("evt=%+v", evt)
	}
}

func TestDecodeStreamEvent_Ping(t *testing.T) {
	evt, err := DecodeStreamEvent([]byte(`{"type":"ping"}`))
	if err != nil {
		t.Fatal(err)
	}
	if evt != nil {
		t.Fatalf("ping should be nil")
	}
}

func TestDecodeStreamEvent_MessageDelta(t *testing.T) {
	evt, err := DecodeStreamEvent([]byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":12}}`))
	if err != nil {
		t.Fatal(err)
	}
	if evt.Type != "message_delta" || evt.StopReason != "end_turn" || evt.OutputTokens != 12 {
		t.Fatalf("evt=%+v", evt)
	}
}

func TestEncodeStreamEvent_TextDelta(t *testing.T) {
	out, err := EncodeStreamEvent(&translate.StreamEvent{
		Type: "content_block_delta", Index: 0,
		Delta: &translate.Delta{Type: "text_delta", Text: "Hi"},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.HasPrefix(s, "event: content_block_delta\n") {
		t.Fatalf("no event line: %q", s)
	}
	if !strings.Contains(s, `"text":"Hi"`) {
		t.Fatalf("no text: %q", s)
	}
	if !strings.HasSuffix(s, "\n\n") {
		t.Fatalf("no trailing newlines: %q", s)
	}
}

func TestEncodeStreamEvent_MessageStop(t *testing.T) {
	out, err := EncodeStreamEvent(&translate.StreamEvent{Type: "message_stop"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "event: message_stop\n") {
		t.Fatalf("msg stop: %q", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/translate/anthropic/`
Expected: FAIL — `DecodeStreamEvent` undefined

- [ ] **Step 3: Write the stream codec**

Create `internal/translate/anthropic/stream.go`:

```go
package anthropic

import (
	"encoding/json"
	"fmt"

	"github.com/great-magician-01/any-llm/internal/translate"
)

func DecodeStreamEvent(data []byte) (*translate.StreamEvent, error) {
	var raw rawStreamEvent
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("anthropic stream decode: %w", err)
	}
	switch raw.Type {
	case "ping", "":
		return nil, nil
	case "message_start":
		evt := &translate.StreamEvent{Type: "message_start"}
		var msg struct {
			ID    string   `json:"id"`
			Model string   `json:"model"`
			Usage *rawUsage `json:"usage"`
		}
		_ = json.Unmarshal(raw.Message, &msg)
		evt.MessageID = msg.ID
		evt.Model = msg.Model
		if msg.Usage != nil {
			evt.InputTokens = msg.Usage.InputTokens
		}
		return evt, nil
	case "content_block_start":
		evt := &translate.StreamEvent{Type: "content_block_start", Index: raw.Index}
		blocks, err := decodeBlocks(raw.ContentBlock)
		if err != nil {
			return nil, err
		}
		if len(blocks) > 0 {
			evt.Block = &blocks[0]
		}
		return evt, nil
	case "content_block_delta":
		evt := &translate.StreamEvent{Type: "content_block_delta", Index: raw.Index}
		var d struct {
			Type        string `json:"type"`
			Text        string `json:"text,omitempty"`
			PartialJSON string `json:"partial_json,omitempty"`
		}
		_ = json.Unmarshal(raw.Delta, &d)
		evt.Delta = &translate.Delta{Type: d.Type, Text: d.Text, PartialJSON: d.PartialJSON}
		return evt, nil
	case "content_block_stop":
		return &translate.StreamEvent{Type: "content_block_stop", Index: raw.Index}, nil
	case "message_delta":
		evt := &translate.StreamEvent{Type: "message_delta"}
		var d struct {
			StopReason string `json:"stop_reason"`
		}
		_ = json.Unmarshal(raw.Delta, &d)
		evt.StopReason = d.StopReason
		if raw.Usage != nil {
			evt.OutputTokens = raw.Usage.OutputTokens
		}
		return evt, nil
	case "message_stop":
		return &translate.StreamEvent{Type: "message_stop"}, nil
	}
	return nil, nil
}

func EncodeStreamEvent(evt *translate.StreamEvent) ([]byte, error) {
	payload := map[string]any{"type": evt.Type}
	switch evt.Type {
	case "message_start":
		msg := map[string]any{"id": evt.MessageID, "model": evt.Model, "role": "assistant"}
		if evt.InputTokens > 0 {
			msg["usage"] = map[string]any{"input_tokens": evt.InputTokens}
		}
		payload["message"] = msg
	case "content_block_start":
		payload["index"] = evt.Index
		if evt.Block != nil {
			payload["content_block"] = blockToRaw(evt.Block)
		}
	case "content_block_delta":
		payload["index"] = evt.Index
		if evt.Delta != nil {
			d := map[string]any{"type": evt.Delta.Type}
			if evt.Delta.Type == "text_delta" {
				d["text"] = evt.Delta.Text
			} else {
				d["partial_json"] = evt.Delta.PartialJSON
			}
			payload["delta"] = d
		}
	case "content_block_stop":
		payload["index"] = evt.Index
	case "message_delta":
		d := map[string]any{"stop_reason": evt.StopReason}
		payload["delta"] = d
		if evt.OutputTokens > 0 || evt.InputTokens > 0 {
			payload["usage"] = map[string]any{"output_tokens": evt.OutputTokens}
		}
	case "message_stop":
		// no extra fields
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("anthropic stream encode: %w", err)
	}
	return []byte("event: " + evt.Type + "\ndata: " + string(b) + "\n\n"), nil
}

func blockToRaw(b translate.ContentBlock) map[string]any {
	switch b.Type {
	case "text":
		return map[string]any{"type": "text", "text": b.Text}
	case "tool_use":
		return map[string]any{"type": "tool_use", "id": b.ToolUse.ID, "name": b.ToolUse.Name, "input": json.RawMessage(b.ToolUse.Input)}
	}
	return map[string]any{"type": b.Type}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/translate/anthropic/`
Expected: PASS (all anthropic tests)

- [ ] **Step 5: Commit**

```bash
git add internal/translate/anthropic/stream.go internal/translate/anthropic/stream_test.go
git commit -m "feat(anthropic): implement stream codec"
```

---

### Task 13: Cross-format integration test

**Files:**
- Test: `internal/translate/cross_test.go`

**Goal:** Verify a request decoded from one format, encoded to the other, then decoded back, preserves semantic content (text, tools, tool_use, tool_result, images, usage). This is the end-to-end proof of the IR design.

**Interfaces:**
- Consumes: all codecs from Tasks 3-12.

- [ ] **Step 1: Write the cross-format test**

Create `internal/translate/cross_test.go`:

```go
package translate_test

import (
	"encoding/json"
	"testing"

	"github.com/great-magician-01/any-llm/internal/translate"
	"github.com/great-magician-01/any-llm/internal/translate/anthropic"
	"github.com/great-magician-01/any-llm/internal/translate/openai"
)

// OpenAI request -> IR -> Anthropic request -> IR -> should match first IR semantically
func TestCrossRequest_OpenAIToAnthropic(t *testing.T) {
	src := []byte(`{
		"model":"gpt-4o",
		"messages":[
			{"role":"system","content":"be good"},
			{"role":"user","content":[
				{"type":"text","text":"hi"},
				{"type":"image_url","image_url":{"url":"https://x/a.png"}}
			]},
			{"role":"assistant","tool_calls":[
				{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"SF\"}"}}
			]},
			{"role":"tool","tool_call_id":"call_1","content":"sunny"}
		],
		"tools":[{"type":"function","function":{"name":"get_weather","description":"w","parameters":{"type":"object"}}}],
		"tool_choice":"auto","max_tokens":50
	}`)
	ir1, err := openai.DecodeRequest(src)
	if err != nil {
		t.Fatal(err)
	}
	antBytes, err := anthropic.EncodeRequest(ir1)
	if err != nil {
		t.Fatal(err)
	}
	ir2, err := anthropic.DecodeRequest(antBytes)
	if err != nil {
		t.Fatal(err)
	}
	assertRequestsMatch(t, ir1, ir2)
}

func TestCrossRequest_AnthropicToOpenAI(t *testing.T) {
	src := []byte(`{
		"model":"claude-3-5","system":"be good",
		"messages":[
			{"role":"user","content":[
				{"type":"text","text":"hi"},
				{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAA"}}
			]},
			{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{"city":"SF"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"sunny"}]}
		],
		"tools":[{"name":"get_weather","description":"w","input_schema":{"type":"object"}}],
		"tool_choice":{"type":"auto"},"max_tokens":50
	}`)
	ir1, err := anthropic.DecodeRequest(src)
	if err != nil {
		t.Fatal(err)
	}
	oaiBytes, err := openai.EncodeRequest(ir1)
	if err != nil {
		t.Fatal(err)
	}
	ir2, err := openai.DecodeRequest(oaiBytes)
	if err != nil {
		t.Fatal(err)
	}
	assertRequestsMatch(t, ir1, ir2)
}

func TestCrossResponse_OpenAIToAnthropic(t *testing.T) {
	src := []byte(`{
		"id":"c1","model":"gpt-4o",
		"choices":[{"index":0,"message":{"role":"assistant","content":"Hi"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}
	}`)
	ir1, err := openai.DecodeResponse(src)
	if err != nil {
		t.Fatal(err)
	}
	antBytes, err := anthropic.EncodeResponse(ir1)
	if err != nil {
		t.Fatal(err)
	}
	ir2, err := anthropic.DecodeResponse(antBytes)
	if err != nil {
		t.Fatal(err)
	}
	if ir2.Content[0].Text != "Hi" {
		t.Fatalf("text=%q", ir2.Content[0].Text)
	}
	if ir2.Usage.InputTokens != 10 || ir2.Usage.OutputTokens != 5 {
		t.Fatalf("usage=%+v", ir2.Usage)
	}
}

func assertRequestsMatch(t *testing.T, a, b *translate.Request) {
	t.Helper()
	if a.Model != b.Model {
		t.Errorf("model %q != %q", a.Model, b.Model)
	}
	if len(a.System) != len(b.System) {
		t.Fatalf("system len %d != %d", len(a.System), len(b.System))
	}
	if len(a.Messages) != len(b.Messages) {
		t.Fatalf("messages len %d != %d", len(a.Messages), len(b.Messages))
	}
	for i := range a.Messages {
		if a.Messages[i].Role != b.Messages[i].Role {
			t.Errorf("msg[%d] role %q != %q", i, a.Messages[i].Role, b.Messages[i].Role)
		}
		if len(a.Messages[i].Content) != len(b.Messages[i].Content) {
			t.Fatalf("msg[%d] content len %d != %d", i, len(a.Messages[i].Content), len(b.Messages[i].Content))
		}
		for j := range a.Messages[i].Content {
			ca, cb := a.Messages[i].Content[j], b.Messages[i].Content[j]
			if ca.Type != cb.Type {
				t.Errorf("msg[%d].content[%d] type %q != %q", i, j, ca.Type, cb.Type)
			}
		}
	}
	if len(a.Tools) != len(b.Tools) {
		t.Errorf("tools len %d != %d", len(a.Tools), len(b.Tools))
	}
	if (a.ToolChoice == nil) != (b.ToolChoice == nil) {
		t.Errorf("tool_choice presence mismatch")
	}
	// Note: image URL vs base64 is not preserved across formats by design (different sources),
	// so we only assert block types match, not image payload. Assert text payload where present.
	for i := range a.Messages {
		for j := range a.Messages[i].Content {
			ca, cb := a.Messages[i].Content[j], b.Messages[i].Content[j]
			if ca.Type == "text" && ca.Text != cb.Text {
				t.Errorf("msg[%d].content[%d] text %q != %q", i, j, ca.Text, cb.Text)
			}
			if ca.Type == "tool_use" && ca.ToolUse.Name != cb.ToolUse.Name {
				t.Errorf("tool_use name %q != %q", ca.ToolUse.Name, cb.ToolUse.Name)
			}
		}
	}
	_ = json.RawMessage(nil)
}
```

- [ ] **Step 2: Run the cross tests**

Run: `go test ./internal/translate/`
Expected: PASS — all tests including cross-format

- [ ] **Step 3: Run the entire translate package**

Run: `go test ./internal/translate/...`
Expected: PASS — all packages green

- [ ] **Step 4: Commit**

```bash
git add internal/translate/cross_test.go
git commit -m "test(translate): add cross-format round-trip integration tests"
```

---

## Self-Review

**1. Spec coverage (Plan A scope = translation layer only):**
- IR types (§3 of spec): Task 1 ✓
- Content block unification (text/image/tool_use/tool_result): Tasks 3,5,9,11 ✓
- function calling归一 (OpenAI tool_calls ↔ Anthropic tool_use/tool_result): Tasks 3,4,9,10 ✓
- Extra 透传: Tasks 3,4,9,10 ✓
- Stream: Anthropic-style IR events + OpenAI fold/unfold: Tasks 6,7,12 ✓
- 4 combinations (OAI↔ANT request/response): Task 13 cross tests ✓
- Plan B will cover gateway/upstream/usage/db; Plan C covers auth/frontend — explicitly out of Plan A scope.

**2. Placeholder scan:** No TBD/TODO; every code step has complete code; every test has real assertions.

**3. Type consistency:**
- `translate.Request`, `translate.Response`, `translate.StreamEvent` used consistently across all tasks ✓
- `StreamDecoder.Decode` returns `[]*translate.StreamEvent` (Task 6); `StreamEncoder.Encode` returns `[][]byte` (Task 7) ✓
- `DecodeStreamEvent`/`EncodeStreamEvent` (Anthropic, Task 12) — different names than OpenAI's struct methods, intentional (Anthropic codec is stateless) ✓
- `mapStopReasonToOpenAI`/`mapStopReasonFromOpenAI` (Task 4/5) and `mapStopReasonToAnthropic` (Task 10) — consistent naming ✓
- `rawUsage` defined separately in each package (no cross-package dependency) ✓

No issues found.
