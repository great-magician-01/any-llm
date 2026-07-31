# Responses API 格式转换 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 any-llm 网关增加 OpenAI Responses API 格式支持（入站 `/v1/responses` + 上游 `responses` 格式），含流式、thinking↔reasoning 互转、token 明细透传、DB 会话存储（`previous_response_id`）。

**Architecture:** 新建 `internal/translate/responses` 包（与 `openai/`、`anthropic/` 对称，通过 IR 互转），网关与上游各加一个分支；会话状态由网关层 `SessionStore`（DB 表 `response_sessions`）维护。DB 不再用 CHECK 约束管格式，改为应用层校验。

**Tech Stack:** Go 1.22+、SQLite (modernc) / PostgreSQL (pgx)、Vue 3 + Naive UI、encoding/json、crypto/rand。

**Spec:** `docs/superpowers/specs/2026-07-31-responses-format-design.md`

## Global Constraints

- 遵守现有格式包的代码模式（错误串 `"responses decode request: %w"` 风格、`extractExtra` 模式、`frame()` SSE 帧辅助函数）。
- `internal/translate` 包不得 import `logger` 或 `gateway`；`translate.Request.Extra` 是透传通道，但 responses 编码器必须显式跳过 `previous_response_id`、`store`、`text` 三个 key。
- DB：不新增/不保留 CHECK 约束；SQLite 重建表必须先 `RENAME` 备份、再建新表、显式列名还原、成功后才 `DROP` 备份，整体在一个事务里。
- 会话 TTL 24h，`Put` 时惰性清扫。
- 测试用 `go test ./...`；SSE 帧格式 `event: <type>\ndata: <json>\n\n`。
- 不得在任何日志/输出/提交信息中打印 API key。
- Windows 环境，CRLF 行尾问题为既有噪音，不要顺手"修复"（`git diff --ignore-space-at-eol` 查看改动）。

---

### Task 1: responses 包——请求解码/编码（types + DecodeRequest + EncodeRequest）

**Files:**
- Create: `internal/translate/responses/types.go`
- Create: `internal/translate/responses/decode.go`（本任务只写 DecodeRequest + 辅助函数）
- Create: `internal/translate/responses/encode.go`（本任务只写 EncodeRequest + 辅助函数）
- Test: `internal/translate/responses/decode_test.go`、`encode_test.go`

**Interfaces:**
- Consumes: `translate.Request/Message/ContentBlock/Tool/ToolChoice/TextBlock`（见 `internal/translate/ir.go`）
- Produces:
  - `func DecodeRequest(body []byte) (*translate.Request, error)` — 与 openai/anthropic 同签名
  - `func EncodeRequest(req *translate.Request) ([]byte, error)`
  - `func extractExtra(all map[string]any) map[string]any` — 包内私有

- [ ] **Step 1: 写 types.go**

```go
package responses

import "encoding/json"

type rawRequest struct {
	Model            string          `json:"model"`
	Instructions     json.RawMessage `json:"instructions,omitempty"` // string 或 []input_text
	Input            []rawItem       `json:"input,omitempty"`
	Tools            []rawTool       `json:"tools,omitempty"`
	ToolChoice       json.RawMessage `json:"tool_choice,omitempty"` // "auto"|"none"|"required" 或 {"type":"function","name":...}
	MaxOutputTokens  int             `json:"max_output_tokens,omitempty"`
	Stream           bool            `json:"stream,omitempty"`
	PreviousResponse string          `json:"previous_response_id,omitempty"`
	Store            bool            `json:"store,omitempty"`
}

// rawItem 是 input[] 里的元素：角色消息（含 role 字段）或顶层工具 item。
type rawItem struct {
	Type     string          `json:"type,omitempty"` // "function_call" | "function_call_output"（角色消息无此字段）
	Role     string          `json:"role,omitempty"` // "user" | "assistant" | "system"
	Content  []rawPart       `json:"content,omitempty"`
	CallID   string          `json:"call_id,omitempty"`
	Name     string          `json:"name,omitempty"`
	Arguments string         `json:"arguments,omitempty"` // JSON 字符串
	Output   string          `json:"output,omitempty"`    // function_call_output 的结果字符串
}

type rawPart struct {
	Type     string `json:"type"` // "input_text" | "input_image" | 其他未知类型
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"` // 图片 URL 或 data URL
}

type rawTool struct {
	Type       string     `json:"type"` // 恒为 "function"
	Name       string     `json:"name,omitempty"`
	Description string    `json:"description,omitempty"`
	Parameters json.RawMessage `json:"parameters,omitempty"`
}

// 已知 key 表：用于 extractExtra（store/previous_response_id 单独处理，text 忽略）。
var knownRequestKeys = map[string]bool{
	"model": true, "instructions": true, "input": true, "tools": true,
	"tool_choice": true, "max_output_tokens": true, "stream": true,
	"store": true, "previous_response_id": true, "text": true,
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

- [ ] **Step 2: 写解码失败测试（先 TDD）**

`internal/translate/responses/decode_test.go`：

```go
package responses

import (
	"encoding/json"
	"testing"

	"github.com/great-magician-01/any-llm/internal/translate"
)

func TestDecodeRequest_Basic(t *testing.T) {
	body := []byte(`{
		"model": "gpt-4o",
		"instructions": "be good",
		"input": [
			{"role": "user", "content": [{"type": "input_text", "text": "hi"}]},
			{"role": "assistant", "content": [{"type": "input_text", "text": "hello"}]},
			{"type": "function_call", "call_id": "call_1", "name": "get_weather", "arguments": "{\"city\":\"SF\"}"},
			{"type": "function_call_output", "call_id": "call_1", "output": "sunny"}
		],
		"tools": [{"type": "function", "name": "get_weather", "description": "w", "parameters": {"type": "object"}}],
		"tool_choice": "auto",
		"max_output_tokens": 50
	}`)
	req, err := DecodeRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if req.Model != "gpt-4o" || req.MaxTokens != 50 {
		t.Fatalf("model/max_tokens: %+v", req)
	}
	if len(req.System) != 1 || req.System[0].Text != "be good" {
		t.Fatalf("system=%+v", req.System)
	}
	// 注意：assistant 文本历史消息必须保留（忠实转换，不丢对话历史）
	if len(req.Messages) != 4 {
		t.Fatalf("messages len=%d", len(req.Messages))
	}
	u := req.Messages[0]
	if u.Role != "user" || len(u.Content) != 1 || u.Content[0].Type != "text" || u.Content[0].Text != "hi" {
		t.Fatalf("msg0=%+v", u)
	}
	at := req.Messages[1]
	if at.Role != "assistant" || len(at.Content) != 1 || at.Content[0].Type != "text" || at.Content[0].Text != "hello" {
		t.Fatalf("msg1(assistant text)=%+v", at)
	}
	a := req.Messages[2]
	if a.Role != "assistant" || len(a.Content) != 1 || a.Content[0].Type != "tool_use" {
		t.Fatalf("msg2=%+v", a)
	}
	if a.Content[0].ToolUse.ID != "call_1" || a.Content[0].ToolUse.Name != "get_weather" ||
		string(a.Content[0].ToolUse.Input) != `{"city":"SF"}` {
		t.Fatalf("tool_use=%+v", a.Content[0].ToolUse)
	}
	tr := req.Messages[3]
	if tr.Role != "user" || tr.Content[0].Type != "tool_result" || tr.Content[0].ToolResult.ToolUseID != "call_1" ||
		tr.Content[0].ToolResult.Content[0].Text != "sunny" {
		t.Fatalf("msg2=%+v", tr)
	}
	if len(req.Tools) != 1 || req.Tools[0].Name != "get_weather" {
		t.Fatalf("tools=%+v", req.Tools)
	}
	if req.ToolChoice == nil || req.ToolChoice.Type != "auto" {
		t.Fatalf("tool_choice=%+v", req.ToolChoice)
	}
}

func TestDecodeRequest_StatefulFields(t *testing.T) {
	body := []byte(`{"model":"m","input":[{"role":"user","content":[{"type":"input_text","text":"hi"}]}],
		"previous_response_id":"resp_abc","store":true,"stream":true}`)
	req, err := DecodeRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if !req.Stream {
		t.Fatal("stream not decoded")
	}
	if req.Extra["previous_response_id"] != "resp_abc" {
		t.Fatalf("extra=%+v", req.Extra)
	}
	if req.Extra["store"] != true {
		t.Fatalf("extra store=%v", req.Extra["store"])
	}
}

func TestDecodeRequest_InstructionsArray(t *testing.T) {
	body := []byte(`{"model":"m","instructions":[{"type":"input_text","text":"a"},{"type":"input_text","text":"b"}],
		"input":[]}`)
	req, err := DecodeRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.System) != 2 || req.System[0].Text != "a" || req.System[1].Text != "b" {
		t.Fatalf("system=%+v", req.System)
	}
}

func TestDecodeRequest_SystemRoleMessage(t *testing.T) {
	body := []byte(`{"model":"m","input":[{"role":"system","content":[{"type":"input_text","text":"sys"}]},
		{"role":"user","content":[{"type":"input_text","text":"hi"}]}]}`)
	req, err := DecodeRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.System) != 1 || req.System[0].Text != "sys" {
		t.Fatalf("system=%+v", req.System)
	}
}

func TestDecodeRequest_Image(t *testing.T) {
	body := []byte(`{"model":"m","input":[{"role":"user","content":[
		{"type":"input_text","text":"what is this"},
		{"type":"input_image","image_url":"https://x/a.png"}]}]}`)
	req, err := DecodeRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	b := req.Messages[0].Content
	if len(b) != 2 || b[0].Type != "text" || b[1].Type != "image" || b[1].Image.URL != "https://x/a.png" {
		t.Fatalf("blocks=%+v", b)
	}
}

func TestDecodeRequest_UnknownPartType(t *testing.T) {
	body := []byte(`{"model":"m","input":[{"role":"user","content":[{"type":"input_file","file_id":"f1"}]}]}`)
	if _, err := DecodeRequest(body); err == nil {
		t.Fatal("expected error for input_file")
	}
}

func TestDecodeRequest_ToolChoiceRequired(t *testing.T) {
	body := []byte(`{"model":"m","input":[],"tool_choice":"required"}`)
	req, err := DecodeRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if req.ToolChoice == nil || req.ToolChoice.Type != "required" {
		t.Fatalf("tool_choice=%+v", req.ToolChoice)
	}
}
```

- [ ] **Step 3: 运行确认失败**

Run: `go test ./internal/translate/responses/ -run TestDecodeRequest -v`
Expected: FAIL（`undefined: DecodeRequest` 或编译错误）

- [ ] **Step 4: 写 DecodeRequest**

`internal/translate/responses/decode.go`：

```go
package responses

import (
	"encoding/json"
	"fmt"

	"github.com/great-magician-01/any-llm/internal/translate"
)

func DecodeRequest(body []byte) (*translate.Request, error) {
	var known rawRequest
	if err := json.Unmarshal(body, &known); err != nil {
		return nil, fmt.Errorf("responses decode request: %w", err)
	}
	var all map[string]any
	_ = json.Unmarshal(body, &all)

	req := &translate.Request{
		Model:          known.Model,
		MaxTokens:      known.MaxOutputTokens,
		Stream:         known.Stream,
		ToolChoice:     decodeToolChoice(known.ToolChoice),
	}
	req.System = append(req.System, decodeInstructions(known.Instructions)...)
	if known.Store {
		req.Extra = map[string]any{"store": true}
	}
	if known.PreviousResponse != "" {
		if req.Extra == nil {
			req.Extra = map[string]any{}
		}
		req.Extra["previous_response_id"] = known.PreviousResponse
	}
	for _, item := range known.Input {
		if item.Type == "" {
			// 角色消息
			if item.Role == "system" {
				req.System = append(req.System, decodePartsToText(item.Content)...)
				continue
			}
			if item.Role != "user" && item.Role != "assistant" {
				return nil, fmt.Errorf("responses decode request: unknown input role %q", item.Role)
			}
			blocks, err := decodeParts(item.Content)
			if err != nil {
				return nil, err
			}
			req.Messages = append(req.Messages, translate.Message{Role: item.Role, Content: blocks})
			continue
		}
		switch item.Type {
		case "function_call":
			req.Messages = append(req.Messages, translate.Message{
				Role: "assistant",
				Content: []translate.ContentBlock{{
					Type: "tool_use",
					ToolUse: &translate.ToolUse{
						ID:    item.CallID,
						Name:  item.Name,
						Input: json.RawMessage(item.Arguments),
					},
				}},
			})
		case "function_call_output":
			req.Messages = append(req.Messages, translate.Message{
				Role: "user",
				Content: []translate.ContentBlock{{
					Type: "tool_result",
					ToolResult: &translate.ToolResult{
						ToolUseID: item.CallID,
						Content:   []translate.ContentBlock{{Type: "text", Text: item.Output}},
					},
				}},
			})
		default:
			return nil, fmt.Errorf("responses decode request: unknown input item type %q", item.Type)
		}
	}
	for _, t := range known.Tools {
		req.Tools = append(req.Tools, translate.Tool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.Parameters,
		})
	}
	req.Extra = mergeExtra(req.Extra, extractExtra(all))
	return req, nil
}

// decodeInstructions 处理 instructions（字符串或 input_text 数组）。
func decodeInstructions(raw json.RawMessage) []translate.TextBlock {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []translate.TextBlock{{Text: s}}
	}
	var parts []rawPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil
	}
	var out []translate.TextBlock
	for _, p := range parts {
		if p.Type == "input_text" {
			out = append(out, translate.TextBlock{Text: p.Text})
		}
	}
	return out
}

// decodeParts 把消息 content parts 转成 IR 块。未知类型报错（不静默丢数据）。
func decodeParts(parts []rawPart) ([]translate.ContentBlock, error) {
	var blocks []translate.ContentBlock
	for _, p := range parts {
		switch p.Type {
		case "input_text":
			blocks = append(blocks, translate.ContentBlock{Type: "text", Text: p.Text})
		case "input_image":
			blocks = append(blocks, translate.ContentBlock{Type: "image", Image: &translate.Image{URL: p.ImageURL}})
		default:
			return nil, fmt.Errorf("responses decode request: unsupported content part type %q", p.Type)
		}
	}
	return blocks, nil
}

func decodePartsToText(parts []rawPart) []translate.TextBlock {
	var out []translate.TextBlock
	for _, p := range parts {
		if p.Type == "input_text" {
			out = append(out, translate.TextBlock{Text: p.Text})
		}
	}
	return out
}

func decodeToolChoice(raw json.RawMessage) *translate.ToolChoice {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return &translate.ToolChoice{Type: s} // auto / none / required
	}
	var obj struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && obj.Type == "function" {
		return &translate.ToolChoice{Type: "tool", Name: obj.Name}
	}
	return &translate.ToolChoice{Type: "auto"}
}

func mergeExtra(existing, more map[string]any) map[string]any {
	if len(more) == 0 {
		return existing
	}
	if existing == nil {
		return more
	}
	for k, v := range more {
		existing[k] = v
	}
	return existing
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/translate/responses/ -run TestDecodeRequest -v`
Expected: PASS（注意 `TestDecodeRequest_StatefulFields` 里 `store` 在 Extra 而 `previous_response_id` 也在 Extra；`tool_choice` "required" 原样保留在 `ToolChoice.Type`）

- [ ] **Step 6: 写编码失败测试**

`internal/translate/responses/encode_test.go`：

```go
package responses

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/great-magician-01/any-llm/internal/translate"
)

func TestEncodeRequest_Full(t *testing.T) {
	req := &translate.Request{
		Model:     "gpt-4o",
		System:    []translate.TextBlock{{Text: "be good"}},
		MaxTokens: 50,
		Messages: []translate.Message{
			{Role: "user", Content: []translate.ContentBlock{
				{Type: "text", Text: "hi"},
				{Type: "image", Image: &translate.Image{URL: "https://x/a.png"}},
			}},
			{Role: "assistant", Content: []translate.ContentBlock{
				{Type: "text", Text: "hello"},
				{Type: "tool_use", ToolUse: &translate.ToolUse{ID: "call_1", Name: "get_weather", Input: json.RawMessage(`{"city":"SF"}`)}},
			}},
			{Role: "user", Content: []translate.ContentBlock{
				{Type: "tool_result", ToolResult: &translate.ToolResult{ToolUseID: "call_1", Content: []translate.ContentBlock{{Type: "text", Text: "sunny"}}}},
			}},
		},
		Tools: []translate.Tool{{Name: "get_weather", Description: "w", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		ToolChoice: &translate.ToolChoice{Type: "auto"},
		Extra: map[string]any{
			"previous_response_id": "resp_old", // 必须被跳过
			"store":                true,        // 必须被跳过
			"metadata":             "x",         // 透传
		},
	}
	body, err := EncodeRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	if m["instructions"] != "be good" {
		t.Fatalf("instructions=%v", m["instructions"])
	}
	if m["max_output_tokens"] != float64(50) {
		t.Fatalf("max_output_tokens=%v", m["max_output_tokens"])
	}
	if _, ok := m["previous_response_id"]; ok {
		t.Fatalf("previous_response_id must not be forwarded: %v", m)
	}
	if _, ok := m["store"]; ok {
		t.Fatalf("store must not be forwarded: %v", m)
	}
	if m["metadata"] != "x" {
		t.Fatalf("metadata passthrough failed: %v", m)
	}
	in, _ := m["input"].([]any)
	if len(in) != 4 {
		t.Fatalf("input len=%d: %v", len(in), in)
	}
	// 0: user 消息（text+image 两个 part）
	u := in[0].(map[string]any)
	if u["role"] != "user" {
		t.Fatalf("in[0]=%v", u)
	}
	parts, _ := u["content"].([]any)
	if len(parts) != 2 {
		t.Fatalf("user parts len=%d", len(parts))
	}
	p0 := parts[0].(map[string]any)
	if p0["type"] != "input_text" || p0["text"] != "hi" {
		t.Fatalf("part0=%v", p0)
	}
	p1 := parts[1].(map[string]any)
	if p1["type"] != "input_image" || p1["image_url"] != "https://x/a.png" {
		t.Fatalf("part1=%v", p1)
	}
	// 1: assistant 消息（text）
	a := in[1].(map[string]any)
	if a["role"] != "assistant" {
		t.Fatalf("in[1]=%v", a)
	}
	// 2: function_call 顶层 item
	fc := in[2].(map[string]any)
	if fc["type"] != "function_call" || fc["call_id"] != "call_1" || fc["name"] != "get_weather" ||
		fc["arguments"] != `{"city":"SF"}` {
		t.Fatalf("in[2]=%v", fc)
	}
	// 3: function_call_output 顶层 item
	fo := in[3].(map[string]any)
	if fo["type"] != "function_call_output" || fo["call_id"] != "call_1" || fo["output"] != "sunny" {
		t.Fatalf("in[3]=%v", fo)
	}
	tools, _ := m["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools=%v", m["tools"])
	}
	to := tools[0].(map[string]any)
	if to["type"] != "function" || to["name"] != "get_weather" {
		t.Fatalf("tool=%v", to)
	}
}

func TestEncodeRequest_ToolChoiceFunction(t *testing.T) {
	req := &translate.Request{Model: "m", ToolChoice: &translate.ToolChoice{Type: "tool", Name: "get_weather"}}
	body, _ := EncodeRequest(req)
	if !strings.Contains(string(body), `"name":"get_weather"`) {
		t.Fatalf("tool_choice missing name: %s", body)
	}
}

func TestEncodeRequest_ThinkingDropped(t *testing.T) {
	req := &translate.Request{Model: "m", Messages: []translate.Message{
		{Role: "assistant", Content: []translate.ContentBlock{
			{Type: "thinking", Thinking: "hmm"},
			{Type: "text", Text: "hi"},
		}},
	}}
	body, err := EncodeRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "hmm") {
		t.Fatalf("thinking must be dropped: %s", body)
	}
}
```

- [ ] **Step 7: 运行确认失败**

Run: `go test ./internal/translate/responses/ -run TestEncodeRequest -v`
Expected: FAIL（`undefined: EncodeRequest`）

- [ ] **Step 8: 写 EncodeRequest**

`internal/translate/responses/encode.go`：

```go
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
```

- [ ] **Step 9: 跑测试确认通过 + 提交**

Run: `go test ./internal/translate/responses/ -v`
Expected: 所有 TestDecodeRequest_*/TestEncodeRequest_* PASS

```bash
git add internal/translate/responses/types.go internal/translate/responses/decode.go internal/translate/responses/encode.go internal/translate/responses/decode_test.go internal/translate/responses/encode_test.go
git commit -m "feat: add responses request decode/encode"
```

---

### Task 2: responses 包——非流式响应解码/编码（DecodeResponse + EncodeResponse + NewID）

**Files:**
- Modify: `internal/translate/responses/decode.go`（追加 DecodeResponse + types）
- Modify: `internal/translate/responses/encode.go`（追加 EncodeResponse + NewID + randHex）
- Modify: `internal/translate/responses/types.go`（追加 rawResponse/rawOutputItem/rawUsage 等）
- Test: `internal/translate/responses/decode_test.go`、`encode_test.go`

**Interfaces:**
- Consumes: Task 1 的 `rawRequest` 辅助函数；`translate.Response/Usage`
- Produces:
  - `func DecodeResponse(body []byte) (*translate.Response, error)`
  - `func EncodeResponse(resp *translate.Response) ([]byte, error)` — `resp.ID` 为空时用 `NewID()` 兜底
  - `func NewID() string` — `"resp_" + 32 hex`（crypto/rand），网关层生成会话 key 用

- [ ] **Step 1: types.go 追加响应类型**

```go
// Response (non-stream)
type rawResponse struct {
	ID                string             `json:"id"`
	Object            string             `json:"object,omitempty"`
	CreatedAt         int64              `json:"created_at,omitempty"`
	Status            string             `json:"status,omitempty"` // completed | incomplete | failed
	Model             string             `json:"model,omitempty"`
	Output            []rawOutputItem    `json:"output,omitempty"`
	Usage             *rawUsage          `json:"usage,omitempty"`
	IncompleteDetails *rawIncompleteDetails `json:"incomplete_details,omitempty"`
}

type rawIncompleteDetails struct {
	Reason string `json:"reason"`
}

type rawOutputItem struct {
	Type      string           `json:"type"` // message | function_call | reasoning | refusal
	ID        string           `json:"id,omitempty"`
	Status    string           `json:"status,omitempty"`
	Role      string           `json:"role,omitempty"`
	Content   []rawOutputPart  `json:"content,omitempty"`
	CallID    string           `json:"call_id,omitempty"`
	Name      string           `json:"name,omitempty"`
	Arguments string           `json:"arguments,omitempty"`
	Summary   []rawSummaryPart `json:"summary,omitempty"`
}

type rawOutputPart struct {
	Type        string `json:"type"` // output_text | output_refusal | ...
	Text        string `json:"text,omitempty"`
	Annotations []any  `json:"annotations,omitempty"`
}

type rawSummaryPart struct {
	Type string `json:"type"` // summary_text
	Text string `json:"text,omitempty"`
}

type rawUsage struct {
	InputTokens          int                      `json:"input_tokens"`
	OutputTokens         int                      `json:"output_tokens"`
	TotalTokens          int                      `json:"total_tokens"`
	InputTokensDetails   *rawInputTokensDetails   `json:"input_tokens_details,omitempty"`
	OutputTokensDetails  *rawOutputTokensDetails  `json:"output_tokens_details,omitempty"`
}

type rawInputTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type rawOutputTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}
```

- [ ] **Step 2: 写解码失败测试**

`decode_test.go` 追加：

```go
func TestDecodeResponse_Full(t *testing.T) {
	body := []byte(`{
		"id": "resp_1", "object": "response", "created_at": 123, "status": "completed", "model": "gpt-4o",
		"output": [
			{"type": "message", "id": "msg_1", "status": "completed", "role": "assistant",
			 "content": [{"type": "output_text", "text": "Hi", "annotations": []}]},
			{"type": "function_call", "id": "fc_1", "call_id": "call_1", "name": "get_weather", "arguments": "{\"city\":\"SF\"}"},
			{"type": "reasoning", "id": "rs_1", "summary": [{"type": "summary_text", "text": "think it through"}], "content": []}
		],
		"usage": {"input_tokens": 437, "output_tokens": 82, "total_tokens": 519,
			"input_tokens_details": {"cached_tokens": 384},
			"output_tokens_details": {"reasoning_tokens": 26}}
	}`)
	resp, err := DecodeResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.ID != "resp_1" || resp.Model != "gpt-4o" || resp.StopReason != "tool_calls" {
		t.Fatalf("resp=%+v", resp)
	}
	if len(resp.Content) != 3 {
		t.Fatalf("content len=%d", len(resp.Content))
	}
	if resp.Content[0].Type != "text" || resp.Content[0].Text != "Hi" {
		t.Fatalf("content0=%+v", resp.Content[0])
	}
	tu := resp.Content[1]
	if tu.Type != "tool_use" || tu.ToolUse.ID != "call_1" || tu.ToolUse.Name != "get_weather" ||
		string(tu.ToolUse.Input) != `{"city":"SF"}` {
		t.Fatalf("content1=%+v", tu)
	}
	th := resp.Content[2]
	if th.Type != "thinking" || th.Thinking != "think it through" || th.Signature != "rs_1" {
		t.Fatalf("content2=%+v", th)
	}
	u := resp.Usage
	if u.InputTokens != 437 || u.OutputTokens != 82 || u.CacheReadTokens != 384 || u.ReasoningTokens != 26 {
		t.Fatalf("usage=%+v", u)
	}
}

func TestDecodeResponse_StatusIncomplete(t *testing.T) {
	body := []byte(`{"id":"r","status":"incomplete","model":"m",
		"incomplete_details":{"reason":"max_output_tokens"},
		"output":[{"type":"message","id":"m1","role":"assistant","content":[{"type":"output_text","text":"part"}]}]}`)
	resp, err := DecodeResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != "max_tokens" {
		t.Fatalf("stop=%q", resp.StopReason)
	}
	if len(resp.Content) != 1 || resp.Content[0].Text != "part" {
		t.Fatalf("content=%+v", resp.Content)
	}
}

func TestDecodeResponse_EmptyOutput(t *testing.T) {
	body := []byte(`{"id":"r","status":"completed","model":"m","output":[]}`)
	resp, err := DecodeResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != "stop" || len(resp.Content) != 0 {
		t.Fatalf("resp=%+v", resp)
	}
}
```

- [ ] **Step 3: 运行确认失败**

Run: `go test ./internal/translate/responses/ -run TestDecodeResponse -v`
Expected: FAIL（`undefined: DecodeResponse`）

- [ ] **Step 4: 写 DecodeResponse**

`decode.go` 追加：

```go
func DecodeResponse(body []byte) (*translate.Response, error) {
	var rr rawResponse
	if err := json.Unmarshal(body, &rr); err != nil {
		return nil, fmt.Errorf("responses decode response: %w", err)
	}
	resp := &translate.Response{ID: rr.ID, Model: rr.Model}
	hasToolCall := false
	for _, item := range rr.Output {
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				if part.Type == "output_text" {
					resp.Content = append(resp.Content, translate.ContentBlock{Type: "text", Text: part.Text})
				}
				// output_refusal 等其他 part 类型跳过
			}
		case "function_call":
			hasToolCall = true
			resp.Content = append(resp.Content, translate.ContentBlock{
				Type: "tool_use",
				ToolUse: &translate.ToolUse{
					ID:    item.CallID,
					Name:  item.Name,
					Input: json.RawMessage(item.Arguments),
				},
			})
		case "reasoning":
			var summary string
			for _, sp := range item.Summary {
				if sp.Type == "summary_text" {
					summary += sp.Text
				}
			}
			if summary != "" {
				resp.Content = append(resp.Content, translate.ContentBlock{
					Type: "thinking", Thinking: summary, Signature: item.ID,
				})
			}
		}
		// 未知 item 类型跳过
	}
	resp.StopReason = mapStopFromStatus(rr.Status, hasToolCall)
	if rr.Usage != nil {
		resp.Usage.InputTokens = rr.Usage.InputTokens
		resp.Usage.OutputTokens = rr.Usage.OutputTokens
		if rr.Usage.InputTokensDetails != nil {
			resp.Usage.CacheReadTokens = rr.Usage.InputTokensDetails.CachedTokens
		}
		if rr.Usage.OutputTokensDetails != nil {
			resp.Usage.ReasoningTokens = rr.Usage.OutputTokensDetails.ReasoningTokens
		}
	}
	return resp, nil
}

// mapStopFromStatus 把 Responses status 映射到 IR StopReason 词表。
// completed 且含 function_call 时推 tool_calls（与 chat completions 一致）。
func mapStopFromStatus(status string, hasToolCall bool) string {
	switch status {
	case "incomplete":
		return "max_tokens"
	case "completed":
		if hasToolCall {
			return "tool_calls"
		}
		return "stop"
	default: // failed 等：错误由 HTTP/事件层报告
		return "stop"
	}
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/translate/responses/ -run TestDecodeResponse -v`
Expected: PASS

- [ ] **Step 6: 写编码失败测试**

`encode_test.go` 追加：

```go
func TestEncodeResponse_Full(t *testing.T) {
	resp := &translate.Response{
		ID:         "resp_9",
		Model:      "gpt-4o",
		StopReason: "stop",
		Content: []translate.ContentBlock{
			{Type: "text", Text: "Hi"},
			{Type: "thinking", Thinking: "think it through", Signature: "rs_1"},
			{Type: "tool_use", ToolUse: &translate.ToolUse{ID: "call_1", Name: "get_weather", Input: json.RawMessage(`{"city":"SF"}`)}},
		},
		Usage: translate.Usage{
			InputTokens: 437, OutputTokens: 82,
			CacheReadTokens: 384, ReasoningTokens: 26,
		},
	}
	body, err := EncodeResponse(resp)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	if m["id"] != "resp_9" || m["object"] != "response" || m["status"] != "completed" || m["model"] != "gpt-4o" {
		t.Fatalf("resp=%v", m)
	}
	output, _ := m["output"].([]any)
	if len(output) != 3 {
		t.Fatalf("output len=%d: %v", len(output), output)
	}
	msg := output[0].(map[string]any)
	if msg["type"] != "message" || msg["role"] != "assistant" {
		t.Fatalf("output0=%v", msg)
	}
	content, _ := msg["content"].([]any)
	part := content[0].(map[string]any)
	if part["type"] != "output_text" || part["text"] != "Hi" {
		t.Fatalf("part=%v", part)
	}
	rs := output[1].(map[string]any)
	if rs["type"] != "reasoning" {
		t.Fatalf("output1=%v", rs)
	}
	summary, _ := rs["summary"].([]any)
	if summary[0].(map[string]any)["text"] != "think it through" {
		t.Fatalf("summary=%v", summary)
	}
	fc := output[2].(map[string]any)
	if fc["type"] != "function_call" || fc["call_id"] != "call_1" || fc["name"] != "get_weather" ||
		fc["arguments"] != `{"city":"SF"}` {
		t.Fatalf("output2=%v", fc)
	}
	usage, _ := m["usage"].(map[string]any)
	if usage["input_tokens"] != float64(437) || usage["output_tokens"] != float64(82) {
		t.Fatalf("usage=%v", usage)
	}
	det, _ := usage["input_tokens_details"].(map[string]any)
	if det["cached_tokens"] != float64(384) {
		t.Fatalf("details=%v", det)
	}
	od, _ := usage["output_tokens_details"].(map[string]any)
	if od["reasoning_tokens"] != float64(26) {
		t.Fatalf("od=%v", od)
	}
}

func TestEncodeResponse_MaxTokens(t *testing.T) {
	resp := &translate.Response{ID: "r", Model: "m", StopReason: "max_tokens",
		Content: []translate.ContentBlock{{Type: "text", Text: "part"}}}
	body, _ := EncodeResponse(resp)
	var m map[string]any
	_ = json.Unmarshal(body, &m)
	if m["status"] != "incomplete" {
		t.Fatalf("status=%v", m["status"])
	}
	det, _ := m["incomplete_details"].(map[string]any)
	if det["reason"] != "max_output_tokens" {
		t.Fatalf("incomplete_details=%v", m["incomplete_details"])
	}
}

func TestEncodeResponse_GeneratesID(t *testing.T) {
	resp := &translate.Response{Model: "m", StopReason: "stop"}
	body, _ := EncodeResponse(resp)
	var m map[string]any
	_ = json.Unmarshal(body, &m)
	id, _ := m["id"].(string)
	if !strings.HasPrefix(id, "resp_") || len(id) <= 5 {
		t.Fatalf("id=%q", id)
	}
}

func TestEncodeResponse_NoUsageWhenZero(t *testing.T) {
	resp := &translate.Response{ID: "r", Model: "m", StopReason: "stop"}
	body, _ := EncodeResponse(resp)
	if strings.Contains(string(body), "usage") {
		t.Fatalf("usage must be omitted: %s", body)
	}
}
```

- [ ] **Step 7: 运行确认失败**

Run: `go test ./internal/translate/responses/ -run TestEncodeResponse -v`
Expected: FAIL（`undefined: EncodeResponse`）

- [ ] **Step 8: 写 EncodeResponse + NewID**

`encode.go` 追加：

```go
import "crypto/rand"
import "encoding/hex"

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
			"input_tokens": resp.Usage.InputTokens,
			"output_tokens": resp.Usage.OutputTokens,
			"total_tokens": resp.Usage.InputTokens + resp.Usage.OutputTokens,
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
```

（记得在 encode.go 顶部 import 增加 `"crypto/rand"`, `"encoding/hex"`, `"time"`。）

- [ ] **Step 9: 跑测试确认通过 + 提交**

Run: `go test ./internal/translate/responses/ -v`
Expected: 全部 PASS

```bash
git add internal/translate/responses/
git commit -m "feat: add responses response decode/encode and NewID"
```

---

### Task 3: responses 包——StreamDecoder（上游 Responses SSE → IR 事件）

**Files:**
- Create: `internal/translate/responses/stream.go`
- Test: `internal/translate/responses/stream_test.go`

**Interfaces:**
- Consumes: Task 1/2 的 `rawResponse`/`rawOutputItem`/`rawUsage`；`translate.StreamEvent/Delta/ContentBlock`
- Produces: `type StreamDecoder struct` + `func NewStreamDecoder() *StreamDecoder` + `func (d *StreamDecoder) Decode(data []byte) ([]*translate.StreamEvent, error)`（data 是单个 SSE `data:` 载荷，不含前缀）

- [ ] **Step 1: 写解码失败测试**

`internal/translate/responses/stream_test.go`：

```go
package responses

import (
	"testing"

	"github.com/great-magician-01/any-llm/internal/translate"
)

// 文本回复完整序列
func TestStreamDecode_TextSequence(t *testing.T) {
	d := NewStreamDecoder()
	frames := []string{
		`{"type":"response.created","response":{"id":"resp_1","object":"response","created_at":1,"status":"in_progress","model":"m","output":[]}}`,
		`{"type":"response.in_progress","response":{"id":"resp_1"}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg_1","type":"message","status":"in_progress","role":"assistant","content":[]}}`,
		`{"type":"response.content_part.added","item_id":"msg_1","output_index":0,"content_index":0,"part":{"type":"output_text","text":"","annotations":[]}}`,
		`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"Hi"}`,
		`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":" there"}`,
		`{"type":"response.output_text.done","item_id":"msg_1","output_index":0,"content_index":0,"text":"Hi there"}`,
		`{"type":"response.content_part.done","item_id":"msg_1","output_index":0,"content_index":0,"part":{"type":"output_text","text":"Hi there","annotations":[]}}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"Hi there","annotations":[]}]}}`,
		`{"type":"response.completed","response":{"id":"resp_1","object":"response","created_at":1,"status":"completed","model":"m","output":[{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"Hi there","annotations":[]}]}],"usage":{"input_tokens":10,"output_tokens":3,"total_tokens":13,"input_tokens_details":{"cached_tokens":7},"output_tokens_details":{"reasoning_tokens":1}}}}`,
	}
	var evs []*translate.StreamEvent
	for _, f := range frames {
		got, err := d.Decode([]byte(f))
		if err != nil {
			t.Fatal(err)
		}
		evs = append(evs, got...)
	}
	if len(evs) < 3 {
		t.Fatalf("too few events: %d", len(evs))
	}
	if evs[0].Type != "message_start" || evs[0].MessageID != "resp_1" || evs[0].Model != "m" {
		t.Fatalf("ev0=%+v", evs[0])
	}
	if evs[1].Type != "content_block_start" || evs[1].Index != 0 || evs[1].Block.Type != "text" {
		t.Fatalf("ev1=%+v", evs[1])
	}
	var text string
	for _, ev := range evs {
		if ev.Type == "content_block_delta" && ev.Delta != nil && ev.Delta.Type == "text_delta" {
			text += ev.Delta.Text
		}
	}
	if text != "Hi there" {
		t.Fatalf("text=%q", text)
	}
	var stop *translate.StreamEvent
	var last *translate.StreamEvent
	for _, ev := range evs {
		if ev.Type == "message_delta" {
			stop = ev
		}
		last = ev
	}
	if stop == nil || stop.StopReason != "stop" || stop.InputTokens != 10 || stop.OutputTokens != 3 ||
		stop.CacheReadTokens != 7 || stop.ReasoningTokens != 1 {
		t.Fatalf("message_delta=%+v", stop)
	}
	if last.Type != "message_stop" {
		t.Fatalf("last=%+v", last)
	}
}

// 工具调用 + arguments 跨 chunk 分段
func TestStreamDecode_ToolUse(t *testing.T) {
	d := NewStreamDecoder()
	frames := []string{
		`{"type":"response.created","response":{"id":"resp_1","status":"in_progress","model":"m","output":[]}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"get_weather","arguments":""}}`,
		`{"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":0,"delta":"{\"city\":"}`,
		`{"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":0,"delta":"\"SF\"}"}`,
		`{"type":"response.function_call_arguments.done","item_id":"fc_1","output_index":0,"arguments":"{\"city\":\"SF\"}"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"get_weather","arguments":"{\"city\":\"SF\"}"}}`,
		`{"type":"response.completed","response":{"id":"resp_1","status":"completed","model":"m","output":[],"usage":{"input_tokens":5,"output_tokens":1,"total_tokens":6}}}`,
	}
	var evs []*translate.StreamEvent
	for _, f := range frames {
		got, err := d.Decode([]byte(f))
		if err != nil {
			t.Fatal(err)
		}
		evs = append(evs, got...)
	}
	var start, json1, json2, stop *translate.StreamEvent
	for _, ev := range evs {
		switch {
		case ev.Type == "content_block_start":
			start = ev
		case ev.Type == "content_block_delta" && ev.Delta != nil && ev.Delta.Type == "input_json_delta":
			if json1 == nil {
				json1 = ev
			} else {
				json2 = ev
			}
		case ev.Type == "content_block_stop":
			stop = ev
		}
	}
	if start == nil || start.Block.Type != "tool_use" || start.Block.ToolUse.ID != "call_1" ||
		start.Block.ToolUse.Name != "get_weather" {
		t.Fatalf("start=%+v", start)
	}
	if json1 == nil || json1.Delta.PartialJSON != `{"city":` || json2 == nil || json2.Delta.PartialJSON != `"SF"}` {
		t.Fatalf("deltas=%+v %+v", json1, json2)
	}
	if stop == nil || stop.Index != start.Index {
		t.Fatalf("stop=%+v", stop)
	}
}

// arguments 只在 output_item.done 出现（从未发 delta）——补发完整 input_json_delta
func TestStreamDecode_ToolUseArgsOnlyInDone(t *testing.T) {
	d := NewStreamDecoder()
	frames := []string{
		`{"type":"response.created","response":{"id":"resp_1","status":"in_progress","model":"m","output":[]}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"get_weather","arguments":""}}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"get_weather","arguments":"{\"city\":\"SF\"}"}}`,
	}
	var evs []*translate.StreamEvent
	for _, f := range frames {
		got, _ := d.Decode([]byte(f))
		evs = append(evs, got...)
	}
	// 期望：start、stop 前有补发的完整 arguments delta
	var jsonDelta, stop *translate.StreamEvent
	for _, ev := range evs {
		if ev.Type == "content_block_delta" && ev.Delta != nil && ev.Delta.Type == "input_json_delta" {
			jsonDelta = ev
		}
		if ev.Type == "content_block_stop" {
			stop = ev
		}
	}
	if jsonDelta == nil || jsonDelta.Delta.PartialJSON != `{"city":"SF"}` {
		t.Fatalf("jsonDelta=%+v", jsonDelta)
	}
	if stop == nil {
		t.Fatal("no content_block_stop")
	}
}

// 推理 + 文本混合（summary delta 转 thinking_delta，reasoning_text 忽略）
func TestStreamDecode_ReasoningThenText(t *testing.T) {
	d := NewStreamDecoder()
	frames := []string{
		`{"type":"response.created","response":{"id":"resp_1","status":"in_progress","model":"m","output":[]}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"id":"rs_1","type":"reasoning","summary":[],"content":[]}}`,
		`{"type":"response.reasoning_summary_text.delta","item_id":"rs_1","output_index":0,"delta":"think "}`,
		`{"type":"response.reasoning_text.delta","item_id":"rs_1","output_index":0,"delta":"long reasoning, ignored"}`,
		`{"type":"response.reasoning_summary_text.delta","item_id":"rs_1","output_index":0,"delta":"it through"}`,
		`{"type":"response.reasoning_summary_text.done","item_id":"rs_1","output_index":0,"summary":[{"type":"summary_text","text":"think it through"}]}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"id":"rs_1","type":"reasoning","summary":[{"type":"summary_text","text":"think it through"}],"content":[]}}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"id":"msg_1","type":"message","status":"in_progress","role":"assistant","content":[]}}`,
		`{"type":"response.output_text.delta","item_id":"msg_1","output_index":1,"content_index":0,"delta":"Hi"}`,
		`{"type":"response.output_item.done","output_index":1,"item":{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"Hi","annotations":[]}]}}`,
		`{"type":"response.completed","response":{"id":"resp_1","status":"completed","model":"m","output":[]}}`,
	}
	var evs []*translate.StreamEvent
	for _, f := range frames {
		got, _ := d.Decode([]byte(f))
		evs = append(evs, got...)
	}
	// 第一个块是 thinking（index 0），第二个是 text（index 1）
	var starts []*translate.StreamEvent
	var thinkDelta string
	for _, ev := range evs {
		if ev.Type == "content_block_start" {
			starts = append(starts, ev)
		}
		if ev.Type == "content_block_delta" && ev.Delta != nil && ev.Delta.Type == "thinking_delta" {
			thinkDelta += ev.Delta.Thinking
		}
	}
	if len(starts) != 2 || starts[0].Block.Type != "thinking" || starts[1].Block.Type != "text" {
		t.Fatalf("starts=%+v", starts)
	}
	if starts[0].Index != 0 || starts[1].Index != 1 {
		t.Fatalf("indices=%d,%d", starts[0].Index, starts[1].Index)
	}
	if thinkDelta != "think it through" {
		t.Fatalf("thinkDelta=%q", thinkDelta)
	}
}

// 缺失 response.created（防御）：首个事件也发出 message_start
func TestStreamDecode_NoCreated(t *testing.T) {
	d := NewStreamDecoder()
	got, err := d.Decode([]byte(`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","content":[]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Type != "message_start" || got[1].Type != "content_block_start" {
		t.Fatalf("evs=%+v", got)
	}
}

func TestStreamDecode_ErrorEvent(t *testing.T) {
	d := NewStreamDecoder()
	got, err := d.Decode([]byte(`{"type":"response.failed","response":{"id":"r","status":"failed","error":{"code":"x","message":"boom"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Type != "error" {
		t.Fatalf("evs=%+v", got)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/translate/responses/ -run TestStreamDecode -v`
Expected: FAIL（`undefined: NewStreamDecoder`）

- [ ] **Step 3: 写 StreamDecoder**

`internal/translate/responses/stream.go`：

```go
package responses

import (
	"encoding/json"
	"fmt"

	"github.com/great-magician-01/any-llm/internal/translate"
)

// 单个 SSE data 载荷（不含 "data: " 前缀）
type rawStreamEvent struct {
	Type        string          `json:"type"`
	Response    *rawResponse    `json:"response,omitempty"`
	Item        *rawOutputItem  `json:"item,omitempty"`
	ItemID      string          `json:"item_id,omitempty"`
	OutputIndex int             `json:"output_index,omitempty"`
	Delta       string          `json:"delta,omitempty"`
	Arguments   string          `json:"arguments,omitempty"`
}

type StreamDecoder struct {
	started     bool
	msgID       string
	model       string
	nextBlock   int
	slot0Taken  bool
	openItems   map[string]*openItem // item id -> 打开的 IR 块
	inputTokens int
	outputTokens int
	cacheRead   int
	reasoning   int
	stopReason  string
}

type openItem struct {
	index    int
	kind     string // text | thinking | tool_use
	argsSent bool   // function_call 已收到过 arguments delta
}

func NewStreamDecoder() *StreamDecoder {
	return &StreamDecoder{openItems: map[string]*openItem{}, nextBlock: 1}
}

// Decode 消费一个 SSE data 载荷，产出 0..n 个 IR 事件。
func (d *StreamDecoder) Decode(data []byte) ([]*translate.StreamEvent, error) {
	var ev rawStreamEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		return nil, fmt.Errorf("responses stream decode: %w", err)
	}
	switch ev.Type {
	case "response.created", "response.in_progress":
		return d.ensureStarted(ev.Response), nil

	case "response.output_item.added":
		evs := d.ensureStarted(nil)
		if ev.Item == nil {
			return evs, nil
		}
		item := ev.Item
		switch item.Type {
		case "message":
			idx := d.nextBlockIndex()
			d.openItems[item.ID] = &openItem{index: idx, kind: "text"}
			evs = append(evs, &translate.StreamEvent{Type: "content_block_start", Index: idx, Block: &translate.ContentBlock{Type: "text"}})
		case "reasoning":
			idx := d.nextBlockIndex()
			d.openItems[item.ID] = &openItem{index: idx, kind: "thinking"}
			evs = append(evs, &translate.StreamEvent{Type: "content_block_start", Index: idx, Block: &translate.ContentBlock{Type: "thinking"}})
		case "function_call":
			idx := d.nextBlockIndex()
			st := &openItem{index: idx, kind: "tool_use"}
			d.openItems[item.ID] = st
			evs = append(evs, &translate.StreamEvent{
				Type:  "content_block_start",
				Index: idx,
				Block: &translate.ContentBlock{
					Type: "tool_use",
					ToolUse: &translate.ToolUse{ID: item.CallID, Name: item.Name, Input: json.RawMessage("{}")},
				},
			})
			// 上游把 arguments 直接放在 added 事件里（如 DeepSeek 风格）时立即转发
			if item.Arguments != "" {
				st.argsSent = true
				evs = append(evs, &translate.StreamEvent{
					Type: "content_block_delta", Index: idx,
					Delta: &translate.Delta{Type: "input_json_delta", PartialJSON: item.Arguments},
				})
			}
		}
		return evs, nil

	case "response.output_text.delta":
		st := d.openItems[ev.ItemID]
		if st == nil || st.kind != "text" {
			return nil, nil
		}
		return []*translate.StreamEvent{{
			Type: "content_block_delta", Index: st.index,
			Delta: &translate.Delta{Type: "text_delta", Text: ev.Delta},
		}}, nil

	case "response.function_call_arguments.delta":
		st := d.openItems[ev.ItemID]
		if st == nil || st.kind != "tool_use" {
			return nil, nil
		}
		st.argsSent = true
		return []*translate.StreamEvent{{
			Type: "content_block_delta", Index: st.index,
			Delta: &translate.Delta{Type: "input_json_delta", PartialJSON: ev.Delta},
		}}, nil

	case "response.reasoning_summary_text.delta":
		st := d.openItems[ev.ItemID]
		if st == nil || st.kind != "thinking" {
			return nil, nil
		}
		return []*translate.StreamEvent{{
			Type: "content_block_delta", Index: st.index,
			Delta: &translate.Delta{Type: "thinking_delta", Thinking: ev.Delta},
		}}, nil

	case "response.reasoning_text.delta":
		// 只保留 summary，完整思考文本忽略
		return nil, nil

	case "response.output_item.done":
		// 真实 OpenAI SSE 中此事件不带顶层 item_id，id 在 item 对象里
		// （output_item.added/done 都如此；只有 delta 类事件带顶层 item_id）。
		id := ev.ItemID
		if id == "" && ev.Item != nil {
			id = ev.Item.ID
		}
		st := d.openItems[id]
		if st == nil {
			return nil, nil
		}
		var evs []*translate.StreamEvent
		if st.kind == "tool_use" && !st.argsSent && ev.Item != nil && ev.Item.Arguments != "" {
			evs = append(evs, &translate.StreamEvent{
				Type: "content_block_delta", Index: st.index,
				Delta: &translate.Delta{Type: "input_json_delta", PartialJSON: ev.Item.Arguments},
			})
		}
		evs = append(evs, &translate.StreamEvent{Type: "content_block_stop", Index: st.index})
		delete(d.openItems, id)
		return evs, nil

	case "response.content_part.added", "response.content_part.done",
		"response.output_text.done", "response.function_call_arguments.done",
		"response.reasoning_summary_text.done":
		return nil, nil

	case "response.completed":
		if ev.Response != nil {
			if u := ev.Response.Usage; u != nil {
				d.inputTokens = u.InputTokens
				d.outputTokens = u.OutputTokens
				if u.InputTokensDetails != nil {
					d.cacheRead = u.InputTokensDetails.CachedTokens
				}
				if u.OutputTokensDetails != nil {
					d.reasoning = u.OutputTokensDetails.ReasoningTokens
				}
			}
			d.stopReason = mapStopFromStatus(ev.Response.Status, hasFunctionCall(ev.Response))
		}
		if d.stopReason == "" {
			d.stopReason = "stop"
		}
		return []*translate.StreamEvent{
			{
				Type: "message_delta", StopReason: d.stopReason,
				InputTokens: d.inputTokens, OutputTokens: d.outputTokens,
				CacheReadTokens: d.cacheRead, ReasoningTokens: d.reasoning,
			},
			{Type: "message_stop"},
		}, nil

	case "response.failed", "response.errored", "error":
		return []*translate.StreamEvent{{Type: "error"}}, nil
	}
	return nil, nil
}

func (d *StreamDecoder) ensureStarted(resp *rawResponse) []*translate.StreamEvent {
	if d.started {
		return nil
	}
	d.started = true
	if resp != nil {
		d.msgID = resp.ID
		d.model = resp.Model
	}
	return []*translate.StreamEvent{{Type: "message_start", MessageID: d.msgID, Model: d.model}}
}

// nextBlockIndex 与 openai 解码器一致：第一个块占 index 0，后续递增。
func (d *StreamDecoder) nextBlockIndex() int {
	if !d.slot0Taken {
		d.slot0Taken = true
		return 0
	}
	idx := d.nextBlock
	d.nextBlock++
	return idx
}

// hasFunctionCall 检查响应 output 里是否含 function_call（用于推 tool_calls）。
func hasFunctionCall(r *rawResponse) bool {
	if r == nil {
		return false
	}
	for _, item := range r.Output {
		if item.Type == "function_call" {
			return true
		}
	}
	return false
}
```

注意：`mapStopFromStatus` 里对 `hasToolCall` 的判断需要基于整个响应；流式场景下 `response.completed` 的 `output` 数组就是完整输出，所以直接传 `hasFunctionCall(ev.Response)`。若 `ev.Response == nil` 走 `d.stopReason == "" → "stop"` 兜底。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/translate/responses/ -run TestStreamDecode -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/translate/responses/stream.go internal/translate/responses/stream_test.go
git commit -m "feat: add responses stream decoder"
```

---

### Task 4: responses 包——StreamEncoder（IR 事件 → Responses SSE，含 Flush/Content）

**Files:**
- Modify: `internal/translate/responses/stream.go`
- Test: `internal/translate/responses/stream_test.go`

**Interfaces:**
- Consumes: `translate.StreamEvent`（IR 事件序列：message_start → content_block_start/delta/stop → message_delta → message_stop）；Task 2 的 `randHex`
- Produces:
  - `type StreamEncoder struct` + `func NewStreamEncoder(model, id string) *StreamEncoder`（id 由网关生成，是会话 key）
  - `func (e *StreamEncoder) Encode(evt *translate.StreamEvent) ([][]byte, error)` — 与 openai.StreamEncoder 同形状
  - `func (e *StreamEncoder) Flush() [][]byte` — 网关在事件循环结束后调用：未发过 created 则先补发；未发 completed 则补发（累积 items + usage）
  - `func (e *StreamEncoder) Content() []translate.ContentBlock` — 累积的模型输出，网关存会话用

- [ ] **Step 1: 写编码失败测试**

`stream_test.go` 追加：

```go
// 文本回复：完整事件序列断言
func TestStreamEncode_TextSequence(t *testing.T) {
	e := NewStreamEncoder("gpt-4o", "resp_9")
	events := []*translate.StreamEvent{
		{Type: "message_start", MessageID: "resp_9", Model: "gpt-4o", InputTokens: 10, CacheReadTokens: 7},
		{Type: "content_block_start", Index: 0, Block: &translate.ContentBlock{Type: "text"}},
		{Type: "content_block_delta", Index: 0, Delta: &translate.Delta{Type: "text_delta", Text: "Hi"}},
		{Type: "content_block_delta", Index: 0, Delta: &translate.Delta{Type: "text_delta", Text: " there"}},
		{Type: "content_block_stop", Index: 0},
		{Type: "message_delta", StopReason: "stop", OutputTokens: 3, ReasoningTokens: 1},
		{Type: "message_stop"},
	}
	var frames []string
	for _, ev := range events {
		got, err := e.Encode(ev)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range got {
			frames = append(frames, string(f))
		}
	}
	s := joinFrames(frames)
	// 开头必须有 response.created + response.in_progress
	if !strings.Contains(s, `"type":"response.created"`) || !strings.Contains(s, `"type":"response.in_progress"`) {
		t.Fatalf("missing created/in_progress: %s", s)
	}
	// output_item.added 是 message，part 是 output_text
	if !strings.Contains(s, `"item":{"id":"msg_`) || !strings.Contains(s, `"type":"message"`) {
		t.Fatalf("missing output_item.added: %s", s)
	}
	// 两个 text delta
	if strings.Count(s, `"type":"response.output_text.delta"`) != 2 {
		t.Fatalf("delta count: %s", s)
	}
	// stop 时发出 done 三连
	for _, want := range []string{"response.output_text.done", "response.content_part.done", "response.output_item.done"} {
		if !strings.Contains(s, `"type":"`+want+`"`) {
			t.Fatalf("missing %s: %s", want, s)
		}
	}
	// completed 带 usage（input 来自 message_start，output 来自 message_delta）
	if !strings.Contains(s, `"type":"response.completed"`) {
		t.Fatalf("missing completed: %s", s)
	}
	if !strings.Contains(s, `"input_tokens":10`) || !strings.Contains(s, `"output_tokens":3`) {
		t.Fatalf("completed usage: %s", s)
	}
	if !strings.Contains(s, `"cached_tokens":7`) || !strings.Contains(s, `"reasoning_tokens":1`) {
		t.Fatalf("completed details: %s", s)
	}
}

// 工具调用：start 块自带 arguments 片段时先补发 delta
func TestStreamEncode_ToolUseWithInitialArgs(t *testing.T) {
	e := NewStreamEncoder("m", "resp_9")
	events := []*translate.StreamEvent{
		{Type: "message_start", MessageID: "resp_9", Model: "m"},
		{Type: "content_block_start", Index: 0, Block: &translate.ContentBlock{
			Type: "tool_use",
			ToolUse: &translate.ToolUse{ID: "call_1", Name: "get_weather", Input: json.RawMessage(`{"city":"SF"}`)},
		}},
		{Type: "content_block_delta", Index: 0, Delta: &translate.Delta{Type: "input_json_delta", PartialJSON: `"SF"}`}},
		{Type: "content_block_stop", Index: 0},
		{Type: "message_delta", StopReason: "tool_calls"},
		{Type: "message_stop"},
	}
	var frames []string
	for _, ev := range events {
		got, _ := e.Encode(ev)
		for _, f := range got {
			frames = append(frames, string(f))
		}
	}
	s := joinFrames(frames)
	if !strings.Contains(s, `"type":"response.output_item.added"`) ||
		!strings.Contains(s, `"type":"function_call"`) ||
		!strings.Contains(s, `"call_id":"call_1"`) {
		t.Fatalf("missing function_call item: %s", s)
	}
	// start 自带完整 arguments -> 补发第一段 delta
	if !strings.Contains(s, `"type":"response.function_call_arguments.delta"`) {
		t.Fatalf("missing arguments delta: %s", s)
	}
	if strings.Count(s, `"type":"response.function_call_arguments.delta"`) != 2 {
		t.Fatalf("expected 2 arguments deltas: %s", s)
	}
	if !strings.Contains(s, `"arguments":"{\"city\":\"SF\"}"`) {
		t.Fatalf("missing final arguments in done: %s", s)
	}
}

// 缺失 content_block_start：delta 直接来时自动补合成
func TestStreamEncode_SynthesizeMissingStart(t *testing.T) {
	e := NewStreamEncoder("m", "resp_9")
	var frames []string
	for _, ev := range []*translate.StreamEvent{
		{Type: "message_start", Model: "m"},
		{Type: "content_block_delta", Index: 0, Delta: &translate.Delta{Type: "text_delta", Text: "Hi"}},
		{Type: "content_block_stop", Index: 0},
		{Type: "message_delta", StopReason: "stop", OutputTokens: 1},
		{Type: "message_stop"},
	} {
		got, _ := e.Encode(ev)
		for _, f := range got {
			frames = append(frames, string(f))
		}
	}
	s := joinFrames(frames)
	if !strings.Contains(s, `"type":"response.output_item.added"`) || !strings.Contains(s, `"type":"response.output_text.delta"`) {
		t.Fatalf("missing synthesized start: %s", s)
	}
}

// thinking 块 -> reasoning item（summary 流）
func TestStreamEncode_Thinking(t *testing.T) {
	e := NewStreamEncoder("m", "resp_9")
	var frames []string
	for _, ev := range []*translate.StreamEvent{
		{Type: "message_start", Model: "m"},
		{Type: "content_block_start", Index: 0, Block: &translate.ContentBlock{Type: "thinking"}},
		{Type: "content_block_delta", Index: 0, Delta: &translate.Delta{Type: "thinking_delta", Thinking: "hmm"}},
		{Type: "content_block_stop", Index: 0},
		{Type: "content_block_start", Index: 1, Block: &translate.ContentBlock{Type: "text"}},
		{Type: "content_block_delta", Index: 1, Delta: &translate.Delta{Type: "text_delta", Text: "Hi"}},
		{Type: "content_block_stop", Index: 1},
		{Type: "message_delta", StopReason: "stop", OutputTokens: 2},
		{Type: "message_stop"},
	} {
		got, _ := e.Encode(ev)
		for _, f := range got {
			frames = append(frames, string(f))
		}
	}
	s := joinFrames(frames)
	if !strings.Contains(s, `"type":"response.reasoning_summary_text.delta"`) ||
		!strings.Contains(s, `"type":"response.reasoning_summary_text.done"`) {
		t.Fatalf("missing reasoning summary events: %s", s)
	}
	if strings.Contains(s, `"type":"response.reasoning_text.delta"`) {
		t.Fatalf("must not emit reasoning_text events: %s", s)
	}
}

// Flush：上游没发 message_stop 时补发 completed；从未 start 时补发 created+completed
func TestStreamEncode_Flush(t *testing.T) {
	e := NewStreamEncoder("m", "resp_9")
	_, _ = e.Encode(&translate.StreamEvent{Type: "message_start", Model: "m"})
	_, _ = e.Encode(&translate.StreamEvent{Type: "content_block_start", Index: 0, Block: &translate.ContentBlock{Type: "text"}})
	_, _ = e.Encode(&translate.StreamEvent{Type: "content_block_delta", Index: 0, Delta: &translate.Delta{Type: "text_delta", Text: "Hi"}})
	_, _ = e.Encode(&translate.StreamEvent{Type: "content_block_stop", Index: 0})
	_, _ = e.Encode(&translate.StreamEvent{Type: "message_delta", StopReason: "stop", OutputTokens: 1})
	frames := e.Flush()
	s := joinFrames(frames)
	if !strings.Contains(s, `"type":"response.completed"`) || !strings.Contains(s, `"output_tokens":1`) {
		t.Fatalf("flush: %s", s)
	}
	// 再次 Flush 应为空（幂等）
	if len(e.Flush()) != 0 {
		t.Fatal("Flush not idempotent")
	}

	e2 := NewStreamEncoder("m", "resp_2")
	frames2 := e2.Flush()
	s2 := joinFrames(frames2)
	if !strings.Contains(s2, `"type":"response.created"`) || !strings.Contains(s2, `"type":"response.completed"`) {
		t.Fatalf("flush empty stream: %s", s2)
	}
}

// Content：累积的模型输出（text/tool_use/thinking）
func TestStreamEncode_Content(t *testing.T) {
	e := NewStreamEncoder("m", "resp_9")
	for _, ev := range []*translate.StreamEvent{
		{Type: "message_start", Model: "m"},
		{Type: "content_block_start", Index: 0, Block: &translate.ContentBlock{Type: "thinking"}},
		{Type: "content_block_delta", Index: 0, Delta: &translate.Delta{Type: "thinking_delta", Thinking: "hmm"}},
		{Type: "content_block_stop", Index: 0},
		{Type: "content_block_start", Index: 1, Block: &translate.ContentBlock{Type: "text"}},
		{Type: "content_block_delta", Index: 1, Delta: &translate.Delta{Type: "text_delta", Text: "Hi"}},
		{Type: "content_block_stop", Index: 1},
		{Type: "content_block_start", Index: 2, Block: &translate.ContentBlock{
			Type: "tool_use", ToolUse: &translate.ToolUse{ID: "call_1", Name: "get_weather", Input: json.RawMessage(`{"city":"SF"}`)},
		}},
		{Type: "content_block_stop", Index: 2},
		{Type: "message_stop"},
	} {
		_, _ = e.Encode(ev)
	}
	c := e.Content()
	if len(c) != 3 {
		t.Fatalf("content len=%d: %+v", len(c), c)
	}
	if c[0].Type != "thinking" || c[0].Thinking != "hmm" {
		t.Fatalf("c0=%+v", c[0])
	}
	if c[1].Type != "text" || c[1].Text != "Hi" {
		t.Fatalf("c1=%+v", c[1])
	}
	if c[2].Type != "tool_use" || c[2].ToolUse.ID != "call_1" || string(c[2].ToolUse.Input) != `{"city":"SF"}` {
		t.Fatalf("c2=%+v", c[2])
	}
}

func joinFrames(frames []string) string {
	var sb strings.Builder
	for _, f := range frames {
		sb.WriteString(f)
		sb.WriteString("\n")
	}
	return sb.String()
}
```

（记得 import `"strings"`。）

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/translate/responses/ -run TestStreamEncode -v`
Expected: FAIL（`undefined: NewStreamEncoder`）

- [ ] **Step 3: 写 StreamEncoder**

`stream.go` 追加（文件顶部 import 增加 `"sort"`, `"strings"`, `"time"`）：

```go
type StreamEncoder struct {
	model      string
	id         string
	created    int64
	started    bool
	completed  bool
	usageIn    int
	usageOut   int
	cacheRead  int
	reasoning  int
	stopReason string
	blockKind  map[int]string // IR block index -> text | thinking | tool_use
	itemIDs    map[int]string // IR block index -> 输出 item id
	textBuf    map[int]string
	toolArgs   map[int]string
	thinkBuf   map[int]string
	toolMeta   map[int]*translate.ToolUse // IR block index -> call_id/name
	items      map[int]map[string]any     // output_index -> 最终 item（completed 用）
	pendingFrames [][]byte                // ensureItemStarted 合成的 added 事件暂存区
}

func NewStreamEncoder(model, id string) *StreamEncoder {
	return &StreamEncoder{
		model:     model,
		id:        id,
		created:   time.Now().Unix(),
		blockKind: map[int]string{},
		itemIDs:   map[int]string{},
		textBuf:   map[int]string{},
		toolArgs:  map[int]string{},
		thinkBuf:  map[int]string{},
		toolMeta:  map[int]*translate.ToolUse{},
		items:     map[int]map[string]any{},
	}
}

func (e *StreamEncoder) Encode(evt *translate.StreamEvent) ([][]byte, error) {
	var frames [][]byte
	if !e.started && evt.Type != "message_start" {
		e.started = true
		frames = append(frames, e.createdFrames()...)
	}
	switch evt.Type {
	case "message_start":
		e.usageIn = evt.InputTokens
		if evt.CacheReadTokens > 0 {
			e.cacheRead = evt.CacheReadTokens
		}
		if e.started {
			return nil, nil
		}
		e.started = true
		return e.createdFrames(), nil

	case "content_block_start":
		if evt.Block == nil {
			return nil, nil
		}
		idx := evt.Index
		switch evt.Block.Type {
		case "text":
			e.blockKind[idx] = "text"
			itemID := "msg_" + randHex(8)
			e.itemIDs[idx] = itemID
			frames = append(frames,
				sseFrame("response.output_item.added", map[string]any{
					"output_index": idx,
					"item": map[string]any{
						"id": itemID, "type": "message", "status": "in_progress", "role": "assistant", "content": []any{},
					},
				}),
				sseFrame("response.content_part.added", map[string]any{
					"item_id": itemID, "output_index": idx, "content_index": 0,
					"part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}},
				}),
			)
		case "thinking":
			e.blockKind[idx] = "thinking"
			itemID := "rs_" + randHex(8)
			e.itemIDs[idx] = itemID
			frames = append(frames, sseFrame("response.output_item.added", map[string]any{
				"output_index": idx,
				"item":         map[string]any{"id": itemID, "type": "reasoning", "summary": []any{}, "content": []any{}},
			}))
		case "tool_use":
			e.blockKind[idx] = "tool_use"
			itemID := "fc_" + randHex(8)
			e.itemIDs[idx] = itemID
			e.toolMeta[idx] = evt.Block.ToolUse
			frames = append(frames, sseFrame("response.output_item.added", map[string]any{
				"output_index": idx,
				"item": map[string]any{
					"id": itemID, "type": "function_call",
					"call_id": evt.Block.ToolUse.ID, "name": evt.Block.ToolUse.Name, "arguments": "",
				},
			}))
			// Anthropic 上游的 start 块自带完整 input：立即转发，避免 arguments 截断
			if input := string(evt.Block.ToolUse.Input); input != "" && input != "{}" {
				e.toolArgs[idx] = input
				frames = append(frames, sseFrame("response.function_call_arguments.delta", map[string]any{
					"item_id": itemID, "output_index": idx, "delta": input,
				}))
			}
		}
		return frames, nil

	case "content_block_delta":
		if evt.Delta == nil {
			return nil, nil
		}
		idx := evt.Index
		switch evt.Delta.Type {
		case "text_delta":
			e.ensureItemStarted(idx, "text")
			e.textBuf[idx] += evt.Delta.Text
			return [][]byte{sseFrame("response.output_text.delta", map[string]any{
				"item_id": e.itemIDs[idx], "output_index": idx, "content_index": 0, "delta": evt.Delta.Text,
			})}, nil
		case "input_json_delta":
			e.ensureItemStarted(idx, "tool_use")
			e.toolArgs[idx] += evt.Delta.PartialJSON
			return [][]byte{sseFrame("response.function_call_arguments.delta", map[string]any{
				"item_id": e.itemIDs[idx], "output_index": idx, "delta": evt.Delta.PartialJSON,
			})}, nil
		case "thinking_delta":
			e.ensureItemStarted(idx, "thinking")
			e.thinkBuf[idx] += evt.Delta.Thinking
			return [][]byte{sseFrame("response.reasoning_summary_text.delta", map[string]any{
				"item_id": e.itemIDs[idx], "output_index": idx, "delta": evt.Delta.Thinking,
			})}, nil
		case "signature_delta":
			// Responses 无签名概念
			return nil, nil
		}
		return nil, nil

	case "content_block_stop":
		idx := evt.Index
		switch e.blockKind[idx] {
		case "text":
			text := e.textBuf[idx]
			itemID := e.itemIDs[idx]
			frames = append(frames,
				sseFrame("response.output_text.done", map[string]any{
					"item_id": itemID, "output_index": idx, "content_index": 0, "text": text,
				}),
				sseFrame("response.content_part.done", map[string]any{
					"item_id": itemID, "output_index": idx, "content_index": 0,
					"part": map[string]any{"type": "output_text", "text": text, "annotations": []any{}},
				}),
			)
			item := map[string]any{
				"type": "message", "id": itemID, "status": "completed", "role": "assistant",
				"content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}},
			}
			e.items[idx] = item
			frames = append(frames, sseFrame("response.output_item.done", map[string]any{"output_index": idx, "item": item}))
		case "thinking":
			itemID := e.itemIDs[idx]
			summary := []any{map[string]any{"type": "summary_text", "text": e.thinkBuf[idx]}}
			frames = append(frames, sseFrame("response.reasoning_summary_text.done", map[string]any{
				"item_id": itemID, "output_index": idx, "summary": summary,
			}))
			item := map[string]any{"type": "reasoning", "id": itemID, "summary": summary, "content": []any{}}
			e.items[idx] = item
			frames = append(frames, sseFrame("response.output_item.done", map[string]any{"output_index": idx, "item": item}))
		case "tool_use":
			itemID := e.itemIDs[idx]
			args := e.toolArgs[idx]
			frames = append(frames, sseFrame("response.function_call_arguments.done", map[string]any{
				"item_id": itemID, "output_index": idx, "arguments": args,
			}))
			tm := e.toolMeta[idx]
			callID, name := "", ""
			if tm != nil {
				callID, name = tm.ID, tm.Name
			}
			item := map[string]any{
				"type": "function_call", "id": itemID, "call_id": callID, "name": name, "arguments": args,
			}
			e.items[idx] = item
			frames = append(frames, sseFrame("response.output_item.done", map[string]any{"output_index": idx, "item": item}))
		}
		return frames, nil

	case "message_delta":
		e.usageOut = evt.OutputTokens
		if evt.CacheReadTokens > 0 {
			e.cacheRead = evt.CacheReadTokens
		}
		if evt.ReasoningTokens > 0 {
			e.reasoning = evt.ReasoningTokens
		}
		if evt.StopReason != "" {
			e.stopReason = evt.StopReason
		}
		return nil, nil

	case "message_stop":
		if e.completed {
			return nil, nil
		}
		e.completed = true
		return e.completedFrames(), nil
	}
	return nil, nil
}

// ensureItemStarted 为缺失 content_block_start 的块补合成 added（+part）事件。
func (e *StreamEncoder) ensureItemStarted(idx int, kind string) {
	if _, ok := e.blockKind[idx]; ok {
		return
	}
	e.blockKind[idx] = kind
	switch kind {
	case "text":
		itemID := "msg_" + randHex(8)
		e.itemIDs[idx] = itemID
		out, _ := e.Encode(&translate.StreamEvent{Type: "content_block_start", Index: idx, Block: &translate.ContentBlock{Type: "text"}})
		for _, f := range out {
			e.pendingFrames = append(e.pendingFrames, f)
		}
	case "thinking":
		out, _ := e.Encode(&translate.StreamEvent{Type: "content_block_start", Index: idx, Block: &translate.ContentBlock{Type: "thinking"}})
		for _, f := range out {
			e.pendingFrames = append(e.pendingFrames, f)
		}
	case "tool_use":
		out, _ := e.Encode(&translate.StreamEvent{Type: "content_block_start", Index: idx, Block: &translate.ContentBlock{Type: "tool_use", ToolUse: &translate.ToolUse{}}})
		for _, f := range out {
			e.pendingFrames = append(e.pendingFrames, f)
		}
	}
}

// pendingFrames 是合成事件暂存区，Encode 返回时统一前置。
func (e *StreamEncoder) createdFrames() [][]byte {
	resp := map[string]any{
		"id": e.id, "object": "response", "created_at": e.created,
		"status": "in_progress", "model": e.model, "output": []any{},
	}
	return [][]byte{
		sseFrame("response.created", map[string]any{"response": resp}),
		sseFrame("response.in_progress", map[string]any{"response": resp}),
	}
}

func (e *StreamEncoder) completedFrames() [][]byte {
	status := "completed"
	if e.stopReason == "max_tokens" {
		status = "incomplete"
	}
	output := make([]any, 0, len(e.items))
	var idxs []int
	for idx := range e.items {
		idxs = append(idxs, idx)
	}
	sort.Ints(idxs)
	for _, idx := range idxs {
		output = append(output, e.items[idx])
	}
	resp := map[string]any{
		"id": e.id, "object": "response", "created_at": e.created,
		"status": status, "model": e.model, "output": output,
	}
	if status == "incomplete" {
		resp["incomplete_details"] = map[string]any{"reason": "max_output_tokens"}
	}
	if e.usageIn > 0 || e.usageOut > 0 {
		usage := map[string]any{
			"input_tokens": e.usageIn, "output_tokens": e.usageOut,
			"total_tokens": e.usageIn + e.usageOut,
		}
		if e.cacheRead > 0 {
			usage["input_tokens_details"] = map[string]any{"cached_tokens": e.cacheRead}
		}
		if e.reasoning > 0 {
			usage["output_tokens_details"] = map[string]any{"reasoning_tokens": e.reasoning}
		}
		resp["usage"] = usage
	}
	return [][]byte{sseFrame("response.completed", map[string]any{"response": resp})}
}

// Flush 由网关在事件循环结束后调用。幂等。
func (e *StreamEncoder) Flush() [][]byte {
	if !e.started {
		e.started = true
		frames := e.createdFrames()
		e.completed = true
		return append(frames, e.completedFrames()...)
	}
	if e.completed {
		return nil
	}
	e.completed = true
	return e.completedFrames()
}

// Content 返回累积的模型输出（顺序 = IR block 顺序），网关存会话用。
func (e *StreamEncoder) Content() []translate.ContentBlock {
	var idxs []int
	for idx := range e.blockKind {
		idxs = append(idxs, idx)
	}
	sort.Ints(idxs)
	var out []translate.ContentBlock
	for _, idx := range idxs {
		switch e.blockKind[idx] {
		case "text":
			out = append(out, translate.ContentBlock{Type: "text", Text: e.textBuf[idx]})
		case "thinking":
			out = append(out, translate.ContentBlock{Type: "thinking", Thinking: e.thinkBuf[idx], Signature: e.itemIDs[idx]})
		case "tool_use":
			tm := e.toolMeta[idx]
			callID, name := "", ""
			if tm != nil {
				callID, name = tm.ID, tm.Name
			}
			out = append(out, translate.ContentBlock{Type: "tool_use", ToolUse: &translate.ToolUse{
				ID: callID, Name: name, Input: json.RawMessage(e.toolArgs[idx]),
			}})
		}
	}
	return out
}

// sseFrame 组装 Responses SSE 帧（event: + data: 双行，SDK 从 data 的 type 字段解析）。
func sseFrame(evType string, payload any) []byte {
	b, _ := json.Marshal(payload)
	return []byte("event: " + evType + "\ndata: " + string(b) + "\n\n")
}
```

**重要修正**：`ensureItemStarted` 里不能递归调 `Encode` 并把输出拼到返回切片（`Encode` 的返回值已经组装好）。改为：`StreamEncoder` 增加字段 `pendingFrames [][]byte`，`Encode` 在每个分支 return 前把 `pendingFrames` 清空并前置。在 `Encode` 开头：

```go
	// 合成帧前置：ensureItemStarted 产生的 added 事件必须先于 delta 输出
	if len(e.pendingFrames) > 0 {
		frames = append(frames, e.pendingFrames...)
		e.pendingFrames = nil
	}
```

放在每个 return 之前的统一出口不可行（switch 内多个 return），所以在 `Encode` 入口对每个 case 处理前处理。具体做法：把上面的前置逻辑放在 `Encode` 函数体最开头（`frames` 初始化之后、`switch` 之前），并让 `ensureItemStarted` 只往 `pendingFrames` 里塞帧。这样 delta 分支返回时 pendingFrames 已在 `frames` 里（在 entry 处被前置）。注意 entry 处 `e.started` 合成的 created 帧也走 `frames`，顺序 OK。

修正后的结构（替换上面 Encode 的开头）：

```go
func (e *StreamEncoder) Encode(evt *translate.StreamEvent) ([][]byte, error) {
	var frames [][]byte
	if !e.started && evt.Type != "message_start" {
		e.started = true
		frames = append(frames, e.createdFrames()...)
	}
	if len(e.pendingFrames) > 0 {
		frames = append(frames, e.pendingFrames...)
		e.pendingFrames = nil
	}
	switch evt.Type {
	...
```

`ensureItemStarted` 保持往 `e.pendingFrames` 塞帧（它内部调 `e.Encode(content_block_start)` 会递归——递归调用里 pendingFrames 已被 entry 清空，但递归返回的帧需要塞回 pendingFrames；为避免递归混乱，**不递归**，直接内联构造帧）：

```go
func (e *StreamEncoder) ensureItemStarted(idx int, kind string) {
	if _, ok := e.blockKind[idx]; ok {
		return
	}
	e.blockKind[idx] = kind
	itemID := "msg_" + randHex(8)
	switch kind {
	case "text":
		e.itemIDs[idx] = itemID
		e.pendingFrames = append(e.pendingFrames,
			sseFrame("response.output_item.added", map[string]any{
				"output_index": idx,
				"item": map[string]any{"id": itemID, "type": "message", "status": "in_progress", "role": "assistant", "content": []any{}},
			}),
			sseFrame("response.content_part.added", map[string]any{
				"item_id": itemID, "output_index": idx, "content_index": 0,
				"part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}},
			}),
		)
	case "thinking":
		e.itemIDs[idx] = itemID
		e.pendingFrames = append(e.pendingFrames, sseFrame("response.output_item.added", map[string]any{
			"output_index": idx,
			"item":         map[string]any{"id": "rs_" + randHex(8), "type": "reasoning", "summary": []any{}, "content": []any{}},
		}))
	case "tool_use":
		e.itemIDs[idx] = itemID
		e.pendingFrames = append(e.pendingFrames, sseFrame("response.output_item.added", map[string]any{
			"output_index": idx,
			"item":         map[string]any{"id": "fc_" + randHex(8), "type": "function_call", "call_id": "", "name": "", "arguments": ""},
		}))
	}
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/translate/responses/ -run TestStreamEncode -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/translate/responses/stream.go internal/translate/responses/stream_test.go
git commit -m "feat: add responses stream encoder with flush and content"
```

---

### Task 5: cross 测试（Responses ↔ IR ↔ openai/anthropic）

**Files:**
- Modify: `internal/translate/cross_test.go`（追加用例）
- Test: 同文件

**Interfaces:**
- Consumes: Task 1-4 的 DecodeRequest/EncodeRequest/DecodeResponse/EncodeResponse；现有 `assertRequestsMatch` 辅助

- [ ] **Step 1: 写 cross 测试**

`cross_test.go` 追加（文件 import 增加 `"github.com/great-magician-01/any-llm/internal/translate/responses"`）：

```go
// Responses 请求 -> IR -> OpenAI chat completions -> IR，语义一致
func TestCrossRequest_ResponsesToOpenAI(t *testing.T) {
	src := []byte(`{
		"model":"gpt-4o",
		"instructions":"be good",
		"input":[
			{"role":"user","content":[{"type":"input_text","text":"hi"}]},
			{"role":"assistant","content":[{"type":"input_text","text":"sure"}]},
			{"type":"function_call","call_id":"call_1","name":"get_weather","arguments":"{\"city\":\"SF\"}"},
			{"type":"function_call_output","call_id":"call_1","output":"sunny"}
		],
		"tools":[{"type":"function","name":"get_weather","description":"w","parameters":{"type":"object"}}],
		"tool_choice":"auto","max_output_tokens":50
	}`)
	ir1, err := responses.DecodeRequest(src)
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

// OpenAI chat completions -> IR -> Responses -> IR，语义一致
func TestCrossRequest_OpenAIToResponses(t *testing.T) {
	src := []byte(`{
		"model":"gpt-4o",
		"messages":[
			{"role":"system","content":"be good"},
			{"role":"user","content":"hi"},
			{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"SF\"}"}}]},
			{"role":"tool","tool_call_id":"call_1","content":"sunny"}
		],
		"tools":[{"type":"function","function":{"name":"get_weather","description":"w","parameters":{"type":"object"}}}],
		"tool_choice":"auto","max_tokens":50
	}`)
	ir1, err := openai.DecodeRequest(src)
	if err != nil {
		t.Fatal(err)
	}
	rspBytes, err := responses.EncodeRequest(ir1)
	if err != nil {
		t.Fatal(err)
	}
	ir2, err := responses.DecodeRequest(rspBytes)
	if err != nil {
		t.Fatal(err)
	}
	assertRequestsMatch(t, ir1, ir2)
}

// Responses 请求 -> IR -> Anthropic -> IR，语义一致
func TestCrossRequest_ResponsesToAnthropic(t *testing.T) {
	src := []byte(`{
		"model":"claude-3-5",
		"instructions":"be good",
		"input":[
			{"role":"user","content":[{"type":"input_text","text":"hi"}]},
			{"role":"assistant","content":[{"type":"input_text","text":"sure"}]},
			{"type":"function_call","call_id":"call_1","name":"get_weather","arguments":"{\"city\":\"SF\"}"},
			{"type":"function_call_output","call_id":"call_1","output":"sunny"}
		],
		"tools":[{"type":"function","name":"get_weather","description":"w","parameters":{"type":"object"}}],
		"tool_choice":"auto","max_output_tokens":50
	}`)
	ir1, err := responses.DecodeRequest(src)
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

// Responses 响应 -> IR -> Anthropic -> IR（含 thinking 与 usage）
func TestCrossResponse_ResponsesToAnthropic(t *testing.T) {
	src := []byte(`{
		"id":"resp_1","object":"response","created_at":1,"status":"completed","model":"claude-3-5",
		"output":[
			{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"hmm"}],"content":[]},
			{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"Hi"}]},
			{"type":"function_call","id":"fc_1","call_id":"call_1","name":"get_weather","arguments":"{\"city\":\"SF\"}"}
		],
		"usage":{"input_tokens":10,"output_tokens":8,"total_tokens":18,
			"input_tokens_details":{"cached_tokens":5},
			"output_tokens_details":{"reasoning_tokens":2}}
	}`)
	ir1, err := responses.DecodeResponse(src)
	if err != nil {
		t.Fatal(err)
	}
	if ir1.StopReason != "tool_calls" || ir1.Usage.CacheReadTokens != 5 || ir1.Usage.ReasoningTokens != 2 {
		t.Fatalf("ir1=%+v usage=%+v", ir1, ir1.Usage)
	}
	antBytes, err := anthropic.EncodeResponse(ir1)
	if err != nil {
		t.Fatal(err)
	}
	ir2, err := anthropic.DecodeResponse(antBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(ir2.Content) != 3 {
		t.Fatalf("content len=%d", len(ir2.Content))
	}
	if ir2.Content[0].Type != "thinking" || ir2.Content[0].Thinking != "hmm" {
		t.Fatalf("c0=%+v", ir2.Content[0])
	}
	if ir2.Content[1].Type != "text" || ir2.Content[1].Text != "Hi" {
		t.Fatalf("c1=%+v", ir2.Content[1])
	}
	if ir2.Content[2].Type != "tool_use" || ir2.Content[2].ToolUse.Name != "get_weather" {
		t.Fatalf("c2=%+v", ir2.Content[2])
	}
	if ir2.Usage.InputTokens != 10 || ir2.Usage.OutputTokens != 8 || ir2.Usage.CacheReadTokens != 5 {
		t.Fatalf("usage=%+v", ir2.Usage)
	}
}

// Anthropic 响应 -> IR -> Responses，usage/thinking 透传（JSON 断言）
func TestCrossResponse_AnthropicToResponses(t *testing.T) {
	src := []byte(`{
		"id":"msg_1","model":"claude-3-5",
		"content":[{"type":"thinking","thinking":"hmm"},{"type":"text","text":"Hi"}],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":59,"output_tokens":71,"cache_read_input_tokens":384}
	}`)
	ir1, err := anthropic.DecodeResponse(src)
	if err != nil {
		t.Fatal(err)
	}
	rspBytes, err := responses.EncodeResponse(ir1)
	if err != nil {
		t.Fatal(err)
	}
	s := string(rspBytes)
	for _, want := range []string{
		`"input_tokens":59`, `"output_tokens":71`, `"total_tokens":130`,
		`"cached_tokens":384`, `"summary"`, `"hmm"`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %s in %s", want, s)
		}
	}
}
```

注意 `TestCrossRequest_*` 中 System 都是**单块**（instructions 字符串 ↔ 单条 system 消息），`assertRequestsMatch` 按块数逐项比较才成立；不要在这个用例里放多条 system 消息。

- [ ] **Step 2: 运行确认通过**

Run: `go test ./internal/translate/ -run TestCross -v`
Expected: PASS

- [ ] **Step 3: 提交**

```bash
git add internal/translate/cross_test.go
git commit -m "test: add cross-format tests for responses"
```

---

### Task 6: DB 迁移——去掉 CHECK 约束 + 新增 response_sessions 表

**Files:**
- Modify: `internal/db/migrations.go`
- Modify: `internal/db/db.go:36-43`（OpenSQLite 调新迁移）、`db.go:86-93`（OpenPG 调新迁移）
- Test: `internal/db/db_test.go`（追加迁移测试）

**Interfaces:**
- Consumes: `DialectOf`（db.go）；`migrateExtraCols` 的调用顺序约定
- Produces: `func dropUpstreamFormatCheck(d *sql.DB) error`（包内私有，两个 Open 函数在跑完主迁移后、`migrateExtraCols` 前调用）

- [ ] **Step 1: 写失败测试**

`internal/db/db_test.go` 追加：

```go
// 旧库（含 CHECK 约束）迁移后：约束移除、数据完好、会话表已建
func TestOpenSQLite_DropsFormatCheckAndKeepsData(t *testing.T) {
	oldSchema := `
CREATE TABLE IF NOT EXISTS upstreams (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    base_url TEXT NOT NULL,
    api_key TEXT NOT NULL,
    format TEXT NOT NULL CHECK(format IN ('openai','anthropic')),
    daily_token_limit INTEGER NOT NULL DEFAULT 0,
    monthly_token_limit INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);`
	path := filepath.Join(t.TempDir(), "old.db")
	d, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(oldSchema); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO upstreams (name, base_url, api_key, format) VALUES ('u1','http://x','k1','openai')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO upstreams (name, base_url, api_key, format) VALUES ('u2','http://y','k2','anthropic')`); err != nil {
		t.Fatal(err)
	}
	d.Close()

	got, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer got.Close()

	// 约束已移除：可插入 responses
	if _, err := got.Exec(`INSERT INTO upstreams (name, base_url, api_key, format) VALUES ('u3','http://z','k3','responses')`); err != nil {
		t.Fatalf("insert responses format failed: %v", err)
	}
	// 旧数据完好
	var n int
	if err := got.QueryRow(`SELECT COUNT(*) FROM upstreams WHERE name IN ('u1','u2')`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("old rows: n=%d err=%v", n, err)
	}
	var id1 int64
	if err := got.QueryRow(`SELECT id FROM upstreams WHERE name='u1'`).Scan(&id1); err != nil || id1 != 1 {
		t.Fatalf("row id preserved: id=%d err=%v", id1, err)
	}
	// 会话表已建
	var tname string
	if err := got.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='response_sessions'`).Scan(&tname); err != nil || tname != "response_sessions" {
		t.Fatalf("response_sessions missing: %q err=%v", tname, err)
	}
}

// 新库本来就没 CHECK，迁移幂等
func TestOpenSQLite_FreshDBHasNoCheck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh.db")
	d, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec(`INSERT INTO upstreams (name, base_url, api_key, format) VALUES ('u','http://z','k','responses')`); err != nil {
		t.Fatalf("fresh db rejected responses format: %v", err)
	}
	// 表结构里没有 CHECK
	var sqlText string
	if err := d.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='upstreams'`).Scan(&sqlText); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sqlText, "CHECK") {
		t.Fatalf("upstreams still has CHECK: %s", sqlText)
	}
}
```

（db_test.go 需要 import：`database/sql`、`path/filepath`、`strings`、`testing`；`sqlite` driver 已通过 `_ "modernc.org/sqlite"` 注册在包内。）

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/db/ -run TestOpenSQLite_ -v`
Expected: FAIL（插入 'responses' 被 CHECK 拒绝 / response_sessions 不存在）

- [ ] **Step 3: 写迁移代码**

`migrations.go` 改动：

1. `migrationSQLite` / `migrationPG` 的 `upstreams` 定义去掉 `CHECK(format IN ('openai','anthropic'))`：

```sql
    format TEXT NOT NULL,
```

2. 两个脚本末尾追加会话表（SQLite 用 DATETIME，PG 用 TIMESTAMP(0)）：

```sql
CREATE TABLE IF NOT EXISTS response_sessions (
    id TEXT PRIMARY KEY,
    messages TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_resp_sessions_used ON response_sessions(last_used_at);
```
（PG 版对应 TIMESTAMP(0)。）

3. 追加函数：

```go
// dropUpstreamFormatCheck 移除旧库 upstreams 表上的 format CHECK 约束（新库
// 的 CREATE TABLE 已不带约束，无需处理）。校验改为应用层（webapi）。
// 必须在 PRAGMA foreign_keys=ON 之前调用：SQLite 在 foreign_keys=OFF 时
// ALTER TABLE RENAME 不会改写其他表的 REFERENCES 子句，重建后引用依然有效。
func dropUpstreamFormatCheck(d *sql.DB) error {
	switch DialectOf(d) {
	case DialectPostgres:
		_, err := d.Exec(`ALTER TABLE upstreams DROP CONSTRAINT IF EXISTS upstreams_format_check`)
		return err
	default:
		var sqlText string
		if err := d.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='upstreams'`).Scan(&sqlText); err != nil {
			// 表不存在（新库尚未建）等异常：交给主迁移处理
			return nil
		}
		if !strings.Contains(sqlText, "CHECK(format IN") {
			return nil
		}
		// 备份 -> 重建 -> 还原 -> 清理，整体一个事务；任何一步失败回滚，旧表仍在。
		tx, err := d.Begin()
		if err != nil {
			return fmt.Errorf("begin rebuild upstreams: %w", err)
		}
		defer tx.Rollback()
		steps := []string{
			`ALTER TABLE upstreams RENAME TO upstreams_bak`,
			`CREATE TABLE upstreams (
			    id INTEGER PRIMARY KEY AUTOINCREMENT,
			    name TEXT NOT NULL UNIQUE,
			    base_url TEXT NOT NULL,
			    api_key TEXT NOT NULL,
			    format TEXT NOT NULL,
			    daily_token_limit INTEGER NOT NULL DEFAULT 0,
			    monthly_token_limit INTEGER NOT NULL DEFAULT 0,
			    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
			)`,
			`INSERT INTO upstreams (id, name, base_url, api_key, format, daily_token_limit, monthly_token_limit, created_at, updated_at)
			 SELECT id, name, base_url, api_key, format, daily_token_limit, monthly_token_limit, created_at, updated_at FROM upstreams_bak`,
			`DROP TABLE upstreams_bak`,
		}
		for _, s := range steps {
			if _, err := tx.Exec(s); err != nil {
				return fmt.Errorf("rebuild upstreams step failed: %w", err)
			}
		}
		return tx.Commit()
	}
}
```

`db.go` 改动：两个 Open 函数在 `d.Exec(migrationSQLite/PG)` 之后、`migrateExtraCols` 之前加：

```go
	if err := dropUpstreamFormatCheck(d); err != nil {
		d.Close()
		return nil, fmt.Errorf("drop upstream format check: %w", err)
	}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/db/ -v`
Expected: 全部 PASS（含新两个用例）

- [ ] **Step 5: 提交**

```bash
git add internal/db/migrations.go internal/db/db.go internal/db/db_test.go
git commit -m "feat: drop upstream format CHECK, add response_sessions table"
```

---

### Task 7: 网关接入——路由 + 非流式/流式编码

**Files:**
- Modify: `internal/gateway/router.go:31-34`（加路由）
- Modify: `internal/gateway/handler_openai.go`（`decodeInbound`、`handleNonStream`、`handleStream`）
- Test: `internal/gateway/router_test.go`、`internal/gateway/handler_openai_test.go`

**Interfaces:**
- Consumes: Task 1-4 的 `responses.DecodeRequest/EncodeResponse/NewStreamEncoder/NewID`
- Produces: 无新公共 API（responses 分支挂进现有函数）

- [ ] **Step 1: 写失败测试**

`router_test.go` 追加（沿用现有测试的 Gateway 构造方式，若现有测试无辅助构造，就新建一个测试 DB + Gateway）：

```go
// /v1/responses 走 responses 入站格式：无 key 401、错误形状与 openai 一致
func TestResponsesRoute(t *testing.T) {
	gw, _ := setupGateway(t) // router_test.go 的现有辅助：(*Gateway, *sql.DB)
	rec := httptest.NewRecorder()
	gw.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"x/y"}`)))
	if rec.Code != 401 {
		t.Fatalf("status=%d want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"error"`) {
		t.Fatalf("error shape: %s", rec.Body.String())
	}
}
```

`handler_openai_test.go` 追加非流式链路测试（mock 上游是 openai chat completions）：

```go
// responses 入站 -> openai 上游：非流式全链路
func TestResponsesNonStreamToOpenAIUpstream(t *testing.T) {
	// mock 上游：断言收到的请求是 chat completions 形状且模型正确，返回文本
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"c1","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"Hi"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
	}))
	defer srv.Close()

	g, d := setupGateway(t) // router_test.go 现有辅助
	uid, err := model.CreateUpstream(d, &model.Upstream{Name: "mock", BaseURL: srv.URL, APIKey: "sk-x", Format: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	model.AddModel(d, uid, "gpt-4o", false, 0, 0)
	k, _ := model.CreateExtKey(d, "test", 0, 0)
	g.client = upstream.NewClient(http.DefaultClient)

	rec := httptest.NewRecorder()
	body := `{"model":"mock/gpt-4o","input":[{"role":"user","content":[{"type":"input_text","text":"hi"}]}]}`
	req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	g.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	// 客户端拿到 Responses 形状
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["object"] != "response" || m["status"] != "completed" {
		t.Fatalf("response=%v", m)
	}
	// 上游拿到 chat completions 形状（模型名被替换为真实模型）
	msgs, _ := gotBody["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("upstream messages=%v", gotBody)
	}
	if gotBody["model"] != "gpt-4o" {
		t.Fatalf("upstream model=%v", gotBody["model"])
	}
}
```

（`newTestGateway`、key 创建、`keyValue` 的写法沿用 `handler_openai_test.go` 里已有测试的现有代码；若现有测试没有 `newTestGateway` 辅助，就在测试文件里加一个，返回带 sqlite 内存库 + writer=nil 的 Gateway。）

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/gateway/ -run 'TestResponses' -v`
Expected: FAIL（404 / undefined）

- [ ] **Step 3: 接入网关**

`router.go`：

```go
	case r.URL.Path == "/v1/responses" && r.Method == "POST":
		g.handleCompletion(w, r, "responses")
```

`handler_openai.go` 三处：

```go
func decodeInbound(body []byte, inFormat string) (*translate.Request, error) {
	switch inFormat {
	case "anthropic":
		return anthropic.DecodeRequest(body)
	case "responses":
		return responses.DecodeRequest(body)
	default:
		return openai.DecodeRequest(body)
	}
}
```

`handleNonStream` 编码 switch：

```go
	switch inFormat {
	case "anthropic":
		out, err = anthropic.EncodeResponse(result.Response)
	case "responses":
		out, err = responses.EncodeResponse(result.Response)
	default:
		out, err = openai.EncodeResponse(result.Response)
	}
```

`handleStream` encoder 选择：

```go
	var encoder interface {
		Encode(evt *translate.StreamEvent) ([][]byte, error)
	}
	switch inFormat {
	case "anthropic":
		encoder = nil
	case "responses":
		encoder = responses.NewStreamEncoder(realModel, responses.NewID())
	default:
		encoder = openai.NewStreamEncoder(realModel)
	}
```

以及流事件循环结束后（`done:` 处、`recordUsage` 前）调用 Flush：

```go
done:
	// 让 responses 编码器补发 response.completed（上游若没发 message_stop）
	if !clientGonePost {
		if enc, ok := encoder.(interface{ Flush() [][]byte }); ok {
			if frames := enc.Flush(); len(frames) > 0 {
				for _, f := range frames {
					w.Write(f)
				}
				flusher.Flush()
			}
		}
	}
```

import 增加 `"github.com/great-magician-01/any-llm/internal/translate/responses"`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/gateway/ -run 'TestResponses' -v` → PASS；再 `go test ./internal/gateway/` → 全绿

- [ ] **Step 5: 提交**

```bash
git add internal/gateway/router.go internal/gateway/handler_openai.go internal/gateway/router_test.go internal/gateway/handler_openai_test.go
git commit -m "feat: wire responses format into gateway routes and handlers"
```

---

### Task 8: upstream.Client 接入——responses 上游格式

**Files:**
- Modify: `internal/upstream/client.go`（Call switch、非流式解码 switch、streamLoop switch）
- Modify: `internal/upstream/fetch.go`（header switch）
- Test: `internal/upstream/client_test.go`

**Interfaces:**
- Consumes: Task 1-3 的 `responses.EncodeRequest/DecodeResponse/NewStreamDecoder`
- Produces: 无新公共 API

- [ ] **Step 1: 写失败测试**

`client_test.go` 追加（沿用现有 httptest mock 模式）：

```go
// responses 上游：非流式（请求是 responses 形状，解码 responses 响应含 usage）
func TestCallResponsesNonStream(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"resp_1","object":"response","created_at":1,"status":"completed","model":"m",
			"output":[{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"Hi"}]}],
			"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15,
				"input_tokens_details":{"cached_tokens":8},"output_tokens_details":{"reasoning_tokens":2}}}`)
	}))
	defer srv.Close()

	c := NewClient(srv.Client())
	u := &model.Upstream{Name: "r", BaseURL: srv.URL, APIKey: "sk-test", Format: "responses"}
	res, err := c.Call(context.Background(), u, &translate.Request{Model: "m", Stream: false}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/responses" {
		t.Fatalf("path=%q", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("auth=%q", gotAuth)
	}
	if gotBody["instructions"] != nil { // 无 system 时不应发 instructions
		t.Fatalf("body=%v", gotBody)
	}
	if res.Response == nil || res.Response.Content[0].Text != "Hi" {
		t.Fatalf("resp=%+v", res.Response)
	}
	u2 := res.Usage()
	if u2.InputTokens != 10 || u2.OutputTokens != 5 || u2.CacheReadTokens != 8 || u2.ReasoningTokens != 2 {
		t.Fatalf("usage=%+v", u2)
	}
}

// responses 上游：流式（SSE 事件序列 -> IR 事件）
func TestCallResponsesStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `event: response.created`+"\n"+`data: {"type":"response.created","response":{"id":"resp_1","status":"in_progress","model":"m","output":[]}}`+"\n\n")
		fmt.Fprint(w, `event: response.output_item.added`+"\n"+`data: {"type":"response.output_item.added","output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","content":[]}}`+"\n\n")
		fmt.Fprint(w, `event: response.output_text.delta`+"\n"+`data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"Hi"}`+"\n\n")
		fmt.Fprint(w, `event: response.output_item.done`+"\n"+`data: {"type":"response.output_item.done","output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"Hi","annotations":[]}]}}`+"\n\n")
		fmt.Fprint(w, `event: response.completed`+"\n"+`data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","model":"m","output":[],"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}}`+"\n\n")
	}))
	defer srv.Close()

	c := NewClient(srv.Client())
	u := &model.Upstream{Name: "r", BaseURL: srv.URL, APIKey: "sk-test", Format: "responses"}
	res, err := c.Call(context.Background(), u, &translate.Request{Model: "m", Stream: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	var usage translate.Usage
	for ev := range res.Stream {
		types = append(types, ev.Type)
		if ev.Type == "message_delta" {
			usage = translate.Usage{InputTokens: ev.InputTokens, OutputTokens: ev.OutputTokens,
				CacheReadTokens: ev.CacheReadTokens, ReasoningTokens: ev.ReasoningTokens}
		}
	}
	joined := strings.Join(types, ",")
	for _, want := range []string{"message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %s in %s", want, joined)
		}
	}
	if usage.InputTokens != 10 || usage.OutputTokens != 5 {
		t.Fatalf("usage=%+v", usage)
	}
	if err := res.StreamErr(); err != nil {
		t.Fatalf("stream err: %v", err)
	}
}

// FetchModels 对 responses 上游走 Bearer + /models
func TestFetchModelsResponses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("path=%q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("auth=%q", r.Header.Get("Authorization"))
		}
		fmt.Fprint(w, `{"object":"list","data":[{"id":"m1"}]}`)
	}))
	defer srv.Close()
	got, err := FetchModels(context.Background(), srv.Client(), &model.Upstream{BaseURL: srv.URL, APIKey: "sk-test", Format: "responses"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "m1" {
		t.Fatalf("models=%v", got)
	}
}
```

（client_test.go 需要 import：`context`、`encoding/json`、`fmt`、`net/http`、`net/http/httptest`、`strings`、`testing`、translate 包——按现有文件的 import 补齐。）

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/upstream/ -run 'TestCallResponses|TestFetchModelsResponses' -v`
Expected: FAIL（responses 分支缺失 → unknown upstream format 错误）

- [ ] **Step 3: 接入 client.go / fetch.go**

`client.go` `Call` switch 加：

```go
	case "responses":
		body, err = responses.EncodeRequest(irReq)
		if err != nil {
			return nil, fmt.Errorf("encode responses request: %w", err)
		}
		// 上游为 responses 格式时不做 include_usage 注入：usage 随 response.completed 返回
		path = "/responses"
		contentType = "application/json"
		reqHeaders = map[string]string{"Authorization": "Bearer " + u.APIKey}
```

非流式解码 switch 加：

```go
		case "responses":
			irResp, err = responses.DecodeResponse(respBody)
```

`streamLoop`：创建 decoder（在 `oaiDec` 旁边）：

```go
	var rspDec *responses.StreamDecoder
	if format == "responses" {
		rspDec = responses.NewStreamDecoder()
	}
```

每行 switch 加：

```go
		case "responses":
			events, err := rspDec.Decode([]byte(data))
			if err != nil {
				logger.Warn("stream decode error", "format", "responses", "err", err, "data", truncateUpstream(data, 256))
				continue
			}
			for _, ev := range events {
				if ev.Type == "message_delta" {
					result.setUsage(translate.Usage{
						InputTokens:     ev.InputTokens,
						OutputTokens:    ev.OutputTokens,
						CacheReadTokens: ev.CacheReadTokens,
						ReasoningTokens: ev.ReasoningTokens,
					})
				}
				select {
				case ch <- ev:
				case <-ctx.Done():
					return
				}
			}
```

import 增加 `"github.com/great-magician-01/any-llm/internal/translate/responses"`。

`fetch.go` header switch：

```go
	switch u.Format {
	case "openai", "responses":
		req.Header.Set("Authorization", "Bearer "+u.APIKey)
	case "anthropic":
		...
```

（URL 分支不用改：default 已覆盖 `/models`。）

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/upstream/ -v`
Expected: 全部 PASS

- [ ] **Step 5: 提交**

```bash
git add internal/upstream/client.go internal/upstream/fetch.go internal/upstream/client_test.go
git commit -m "feat: support responses format upstream"
```

---

### Task 9: 会话存储（SessionStore + dispatch 合并 + 保存）

**Files:**
- Create: `internal/gateway/session.go`
- Modify: `internal/gateway/handler_openai.go`（dispatch 合并/生成 id、handleNonStream/handleStream 保存）
- Modify: `internal/gateway/router.go`（Gateway 结构体加 sessions 字段、New 初始化）
- Test: `internal/gateway/session_test.go`、`internal/gateway/handler_openai_test.go`（工具循环集成）

**Interfaces:**
- Consumes: Task 6 的 `response_sessions` 表；Task 2 的 `responses.NewID()`；Task 4 的 encoder `Content()`；现有 `model.CreateExtKey`/`CreateUpstream`/`GetUpstreamByName`
- Produces:
  - `type sessionCtx struct { respID string; prev []translate.Message; input []translate.Message }`（dispatch 内私有）
  - `type SessionStore struct` + `func NewSessionStore(db *sql.DB, ttl time.Duration) *SessionStore`
  - `func (s *SessionStore) Get(id string) ([]translate.Message, bool, error)`
  - `func (s *SessionStore) Put(id string, msgs []translate.Message) error`
  - `func (g *Gateway) saveSession(sess *sessionCtx, content []translate.ContentBlock)`（私有）

- [ ] **Step 1: 写 SessionStore 失败测试**

`internal/gateway/session_test.go`：

```go
package gateway

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/great-magician-01/any-llm/internal/db"
	"github.com/great-magician-01/any-llm/internal/translate"
)

func newSessionStore(t *testing.T, ttl time.Duration) *SessionStore {
	t.Helper()
	d, err := db.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return NewSessionStore(d, ttl)
}

func TestSessionStore_PutGet(t *testing.T) {
	s := newSessionStore(t, time.Hour)
	msgs := []translate.Message{{Role: "user", Content: []translate.ContentBlock{{Type: "text", Text: "hi"}}}}
	if err := s.Put("resp_1", msgs); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Get("resp_1")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if len(got) != 1 || got[0].Content[0].Text != "hi" {
		t.Fatalf("got=%+v", got)
	}
	// 覆盖更新
	msgs2 := append(msgs, translate.Message{Role: "assistant", Content: []translate.ContentBlock{{Type: "text", Text: "yo"}}})
	if err := s.Put("resp_1", msgs2); err != nil {
		t.Fatal(err)
	}
	got2, ok, _ := s.Get("resp_1")
	if !ok || len(got2) != 2 {
		t.Fatalf("got2=%+v ok=%v", got2, ok)
	}
}

func TestSessionStore_Miss(t *testing.T) {
	s := newSessionStore(t, time.Hour)
	if _, ok, err := s.Get("resp_unknown"); err != nil || ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestSessionStore_Expiry(t *testing.T) {
	s := newSessionStore(t, time.Millisecond)
	if err := s.Put("resp_1", []translate.Message{{Role: "user", Content: []translate.ContentBlock{{Type: "text", Text: "hi"}}}}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, ok, err := s.Get("resp_1"); err != nil || ok {
		t.Fatalf("expired session still found: ok=%v err=%v", ok, err)
	}
}

func TestSessionStore_JSONRoundTrip(t *testing.T) {
	s := newSessionStore(t, time.Hour)
	msgs := []translate.Message{
		{Role: "user", Content: []translate.ContentBlock{
			{Type: "tool_result", ToolResult: &translate.ToolResult{ToolUseID: "call_1", Content: []translate.ContentBlock{{Type: "text", Text: "sunny"}}}},
		}},
		{Role: "assistant", Content: []translate.ContentBlock{
			{Type: "tool_use", ToolUse: &translate.ToolUse{ID: "call_1", Name: "get_weather", Input: json.RawMessage(`{"city":"SF"}`)}},
		}},
	}
	if err := s.Put("resp_1", msgs); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Get("resp_1")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if got[0].Content[0].ToolResult.ToolUseID != "call_1" || got[1].Content[0].ToolUse.Input[0] != '{' {
		t.Fatalf("round trip broken: %+v", got)
	}
}

func TestSessionStore_SweepOnPut(t *testing.T) {
	s := newSessionStore(t, time.Millisecond)
	_ = s.Put("resp_old", []translate.Message{})
	time.Sleep(5 * time.Millisecond)
	// 第二次 Put 触发清扫，过期行应被删除
	_ = s.Put("resp_new", []translate.Message{{Role: "user", Content: []translate.ContentBlock{{Type: "text", Text: "x"}}}})
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM response_sessions`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("sweep failed: %d rows remain", n)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/gateway/ -run TestSessionStore -v`
Expected: FAIL（`undefined: NewSessionStore`）

- [ ] **Step 3: 写 SessionStore**

`internal/gateway/session.go`：

```go
package gateway

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/great-magician-01/any-llm/internal/db"
	"github.com/great-magician-01/any-llm/internal/logger"
	"github.com/great-magician-01/any-llm/internal/translate"
)

// sessionTTL 是会话空闲过期时间：超过后 previous_response_id 返回 400。
const sessionTTL = 24 * time.Hour

// SessionStore 在 response_sessions 表里维护 Responses 有状态会话。
// 客户端用 store + previous_response_id 延续对话，历史由网关累积存储，
// 转发给上游的调用始终是无状态、带全量历史的。
type SessionStore struct {
	db  *sql.DB
	ttl time.Duration
}

func NewSessionStore(db *sql.DB, ttl time.Duration) *SessionStore {
	return &SessionStore{db: db, ttl: ttl}
}

// Get 返回累积会话消息。已过期（空闲超过 ttl）视为未命中并删除。
func (s *SessionStore) Get(id string) ([]translate.Message, bool, error) {
	var msgsJSON string
	var lastUsed time.Time
	err := s.db.QueryRow(
		db.Rebind(s.db, `SELECT messages, last_used_at FROM response_sessions WHERE id = ?`), id,
	).Scan(&msgsJSON, &lastUsed)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("session get: %w", err)
	}
	if time.Since(lastUsed) > s.ttl {
		_, _ = s.db.Exec(db.Rebind(s.db, `DELETE FROM response_sessions WHERE id = ?`), id)
		return nil, false, nil
	}
	if _, err := s.db.Exec(db.Rebind(s.db, `UPDATE response_sessions SET last_used_at = CURRENT_TIMESTAMP WHERE id = ?`), id); err != nil {
		logger.Warn("session touch failed", "id", id, "err", err)
	}
	var msgs []translate.Message
	if err := json.Unmarshal([]byte(msgsJSON), &msgs); err != nil {
		return nil, false, fmt.Errorf("session decode: %w", err)
	}
	return msgs, true, nil
}

// Put 保存（或覆盖）会话，并惰性清扫过期会话。
func (s *SessionStore) Put(id string, msgs []translate.Message) error {
	data, err := json.Marshal(msgs)
	if err != nil {
		return fmt.Errorf("session encode: %w", err)
	}
	_, err = s.db.Exec(
		db.Rebind(s.db, `INSERT INTO response_sessions (id, messages, created_at, last_used_at)
		 VALUES (?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		 ON CONFLICT(id) DO UPDATE SET messages = excluded.messages, last_used_at = CURRENT_TIMESTAMP`),
		id, string(data),
	)
	if err != nil {
		return fmt.Errorf("session put: %w", err)
	}
	if _, err := s.db.Exec(db.Rebind(s.db, `DELETE FROM response_sessions WHERE last_used_at < ?`),
		time.Now().Add(-s.ttl).Format("2006-01-02 15:04:05")); err != nil {
		logger.Warn("session sweep failed", "err", err)
	}
	return nil
}
```

注意：SQLite 的 `datetime('now')` 与 Go 时间格式的兼容——`CURRENT_TIMESTAMP` 存的是 `YYYY-MM-DD HH:MM:SS` UTC；`Get` 里 `Scan(&lastUsed)` 需要 driver 能解析；`last_used_at < ?` 传同样格式字符串。如果 `Scan` 到 `time.Time` 报错（modernc 对 DATETIME 的解析），改为 `Scan` 成字符串再 `time.Parse("2006-01-02 15:04:05", ...)`，并按 UTC 比较。**以实际跑通为准**：跑测试时若 `Scan(&lastUsed)` 失败，改成字符串扫描 + `time.Parse`（SQLite 存的是 UTC 时间）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/gateway/ -run TestSessionStore -v`
Expected: PASS（若 `Scan` 时间类型报错，按上面注记修正为字符串解析）

- [ ] **Step 5: dispatch 合并 + 保存逻辑（先写集成失败测试）**

`handler_openai_test.go` 追加工具循环集成测试：

```go
// 有状态两轮工具循环：第一轮 assistant 返回 function_call，
// 第二轮 previous_response_id 续接 + function_call_output，
// 上游必须收到包含完整历史的请求。
func TestResponsesStatefulToolLoop(t *testing.T) {
	var upstreamCalls []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		upstreamCalls = append(upstreamCalls, body)
		w.Header().Set("Content-Type", "application/json")
		if len(upstreamCalls) == 1 {
			// 第一轮：只回工具调用
			fmt.Fprint(w, `{"id":"c1","model":"m","choices":[{"index":0,"message":{"role":"assistant",
				"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"SF\"}"}}]},
				"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
		} else {
			fmt.Fprint(w, `{"id":"c2","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}],
				"usage":{"prompt_tokens":20,"completion_tokens":3,"total_tokens":23}}`)
		}
	}))
	defer srv.Close()

	g, d := setupGateway(t)
	uid, err := model.CreateUpstream(d, &model.Upstream{Name: "mock", BaseURL: srv.URL, APIKey: "sk-x", Format: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	model.AddModel(d, uid, "m", false, 0, 0)
	k, _ := model.CreateExtKey(d, "test", 0, 0)
	g.client = upstream.NewClient(http.DefaultClient)
	gw := g

	// 第一轮：纯文本输入，store 续接
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(
		`{"model":"mock/m","input":[{"role":"user","content":[{"type":"input_text","text":"weather in SF?"}]}],"store":true}`))
	req1.Header.Set("Authorization", "Bearer "+k.Key)
	gw.ServeHTTP(rec1, req1)
	if rec1.Code != 200 {
		t.Fatalf("turn1 status=%d body=%s", rec1.Code, rec1.Body.String())
	}
	var resp1 map[string]any
	_ = json.Unmarshal(rec1.Body.Bytes(), &resp1)
	id1, _ := resp1["id"].(string)
	if !strings.HasPrefix(id1, "resp_") {
		t.Fatalf("turn1 id=%q", id1)
	}
	output, _ := resp1["output"].([]any)
	fc := output[0].(map[string]any)
	if fc["type"] != "function_call" || fc["call_id"] != "call_1" {
		t.Fatalf("turn1 output=%v", output)
	}

	// 第二轮：previous_response_id + function_call_output（只发新内容）
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(fmt.Sprintf(
		`{"model":"mock/m","previous_response_id":"%s","input":[{"type":"function_call_output","call_id":"call_1","output":"sunny"}]}`, id1)))
	req2.Header.Set("Authorization", "Bearer "+k.Key)
	gw.ServeHTTP(rec2, req2)
	if rec2.Code != 200 {
		t.Fatalf("turn2 status=%d body=%s", rec2.Code, rec2.Body.String())
	}

	// 上游第二轮请求必须包含完整历史：user 问题 + assistant 工具调用 + user 工具结果
	if len(upstreamCalls) != 2 {
		t.Fatalf("upstream calls=%d", len(upstreamCalls))
	}
	msgs, _ := upstreamCalls[1]["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("turn2 upstream messages len=%d: %v", len(msgs), msgs)
	}
	m0 := msgs[0].(map[string]any)
	if m0["role"] != "user" {
		t.Fatalf("m0=%v", m0)
	}
	m1 := msgs[1].(map[string]any)
	tcs, _ := m1["tool_calls"].([]any)
	if m1["role"] != "assistant" || len(tcs) != 1 {
		t.Fatalf("m1=%v", m1)
	}
	m2 := msgs[2].(map[string]any)
	if m2["role"] != "tool" || m2["tool_call_id"] != "call_1" {
		t.Fatalf("m2=%v", m2)
	}
}

// 未知 previous_response_id -> 400 invalid_previous_response_id
func TestResponsesUnknownPreviousID(t *testing.T) {
	g, d := setupGateway(t)
	_, err := model.CreateUpstream(d, &model.Upstream{Name: "mock", BaseURL: "http://127.0.0.1:1", APIKey: "sk-x", Format: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	k, _ := model.CreateExtKey(d, "test", 0, 0)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(
		`{"model":"mock/m","previous_response_id":"resp_nope","input":[{"role":"user","content":[{"type":"input_text","text":"hi"}]}]}`))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	g.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid_previous_response_id") {
		t.Fatalf("error type: %s", rec.Body.String())
	}
}
```

（`model.ExtKey` 结构字段确认：`Key string`、`Enabled bool`、`Label string`——按 `internal/model/types.go` 实际字段名写；`CreateExtKey` 签名按 model 包实际定义。测试里 key 用不冲突的字面值。）

- [ ] **Step 6: 运行确认失败**

Run: `go test ./internal/gateway/ -run 'TestResponsesStateful|TestResponsesUnknown' -v`
Expected: FAIL（会话逻辑不存在 → 第二轮历史缺失或 unknown id 未被拦截）

- [ ] **Step 7: 实现 dispatch 合并 + 保存**

`router.go`：Gateway 结构体加字段、New 初始化：

```go
type Gateway struct {
	db       *sql.DB
	writer   *db.Writer
	client   *upstream.Client
	sessions *SessionStore
}

func New(db *sql.DB, writer *db.Writer, client *upstream.Client) *Gateway {
	return &Gateway{db: db, writer: writer, client: client, sessions: NewSessionStore(db, sessionTTL)}
}
```

`handler_openai.go`：

1. `sessionCtx` 类型 + dispatch 里 responses 分支（在 `decodeInbound` 之后、`irReq.Stream` 判断之前）：

```go
	var sess *sessionCtx
	if inFormat == "responses" {
		sess = &sessionCtx{respID: responses.NewID(), input: irReq.Messages}
		if pid, _ := irReq.Extra["previous_response_id"].(string); pid != "" {
			hist, ok, err := g.sessions.Get(pid)
			if err != nil {
				WriteError(w, 500, inFormat, "session lookup failed: "+err.Error(), "internal_error")
				g.recordUsage(key, u, realModel, inFormat, translate.Usage{}, false, "error")
				return
			}
			if !ok {
				WriteError(w, 400, inFormat, "unknown previous_response_id: "+pid, "invalid_previous_response_id")
				g.recordUsage(key, u, realModel, inFormat, translate.Usage{}, false, "error")
				return
			}
			sess.prev = hist
			// 注意拷贝：避免 append 复写 hist 底层数组
			merged := make([]translate.Message, 0, len(hist)+len(irReq.Messages))
			merged = append(merged, hist...)
			merged = append(merged, irReq.Messages...)
			irReq.Messages = merged
		}
		// 会话字段不转发给上游
		delete(irReq.Extra, "previous_response_id")
		delete(irReq.Extra, "store")
	}
```

2. 非流式：`client.Call` 成功后、`handleNonStream` 前：

```go
	if sess != nil {
		result.Response.ID = sess.respID
	}
	g.handleNonStream(w, inFormat, result, key, u, realModel, irReq.Stream)
```

3. `handleNonStream` 签名加 `sess *sessionCtx` 参数，编码成功后保存：

```go
func (g *Gateway) handleNonStream(w http.ResponseWriter, inFormat string, result *upstream.Result, key *model.ExtKey, u *model.Upstream, realModel string, stream bool, sess *sessionCtx) {
	...
	w.Header().Set("Content-Type", "application/json")
	w.Write(out)
	if sess != nil {
		g.saveSession(sess, result.Response.Content)
	}
	usage := result.Usage()
	...
}
```

4. 流式：`handleStream` 签名加 `sess *sessionCtx`，encoder 用 `sess.respID`；`done:` 处 Flush 后保存：

```go
	case "responses":
		encoder = responses.NewStreamEncoder(realModel, sess.respID)
```

`done:` 处（在现有 Flush type assertion 块里加 Content 保存）：

```go
done:
	if !clientGonePost {
		if enc, ok := encoder.(interface {
			Flush() [][]byte
			Content() []translate.ContentBlock
		}); ok {
			if frames := enc.Flush(); len(frames) > 0 {
				for _, f := range frames {
					w.Write(f)
				}
				flusher.Flush()
			}
			if sess != nil {
				g.saveSession(sess, enc.Content())
			}
		}
	}
```

5. 保存辅助：

```go
// saveSession 累积会话：旧历史 + 本轮输入 + 本轮模型输出。
// 只在调用成功后调用；失败时不存，客户端带同一 previous_response_id 重试不会重复。
func (g *Gateway) saveSession(sess *sessionCtx, content []translate.ContentBlock) {
	msgs := make([]translate.Message, 0, len(sess.prev)+len(sess.input)+1)
	msgs = append(msgs, sess.prev...)
	msgs = append(msgs, sess.input...)
	msgs = append(msgs, translate.Message{Role: "assistant", Content: content})
	if err := g.sessions.Put(sess.respID, msgs); err != nil {
		logger.Warn("session save failed", "id", sess.respID, "err", err)
	}
}

type sessionCtx struct {
	respID string                 // 返回给客户端的响应 id，也是会话 key
	prev   []translate.Message    // previous_response_id 命中的旧历史
	input  []translate.Message    // 本轮请求的 input（未合并前的）
}
```

注意：`handleNonStream`/`handleStream` 的调用点在 dispatch（两处 `g.handleNonStream(...)`、一处 `g.handleStream(...)`）都要带上 `sess` 参数；Task 7 加的流式 Flush 块与这里的 Flush+Content 块合并。

- [ ] **Step 8: 跑测试确认通过**

Run: `go test ./internal/gateway/ -v`
Expected: 全部 PASS（含新集成测试）

- [ ] **Step 9: 提交**

```bash
git add internal/gateway/session.go internal/gateway/session_test.go internal/gateway/handler_openai.go internal/gateway/router.go internal/gateway/handler_openai_test.go
git commit -m "feat: add DB session store for responses stateful mode"
```

---

### Task 10: 应用层校验 + 后台表单

**Files:**
- Modify: `internal/webapi/upstreams.go:42-44`
- Modify: `web/src/views/Upstreams.vue:319-321`
- Test: `internal/webapi/upstreams_test.go`（追加 responses 用例）

- [ ] **Step 1: 写失败测试**

`upstreams_test.go` 追加（沿用现有测试的请求构造方式）：

```go
func TestCreateUpstream_ResponsesFormat(t *testing.T) {
	// 沿用现有 TestCreateUpstream 的 setup（DB + handler）
	body, _ := json.Marshal(map[string]any{"name": "r1", "base_url": "https://api.openai.com", "api_key": "sk-xxx", "format": "responses"})
	rec := doCreateUpstream(t, body) // 复用现有测试辅助；若没有则照现有用例内联
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreateUpstream_InvalidFormat(t *testing.T) {
	body, _ := json.Marshal(map[string]any{"name": "r2", "base_url": "https://x", "api_key": "k", "format": "yaml"})
	rec := doCreateUpstream(t, body)
	if rec.Code != 400 {
		t.Fatalf("status=%d", rec.Code)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/webapi/ -run TestCreateUpstream_Responses -v`
Expected: FAIL（`format must be openai or anthropic`）

- [ ] **Step 3: 改校验**

`internal/webapi/upstreams.go:42`：

```go
	if req.Format != "openai" && req.Format != "anthropic" && req.Format != "responses" {
		logger.Warn("admin: create upstream invalid format", "format", req.Format)
		writeJSON(w, 400, map[string]any{"error": "format must be openai, anthropic or responses"})
		return
	}
```

- [ ] **Step 4: 改前端表单**

`web/src/views/Upstreams.vue`（约 319 行，n-radio-group 内）：

```html
              <n-radio value="responses">Responses</n-radio>
```

（放在 anthropic 之后。）

- [ ] **Step 5: 跑测试 + 前端构建**

Run: `go test ./internal/webapi/ -v` → PASS
Run: `cd web && npm run build` → 成功（vue-tsc + vite）

- [ ] **Step 6: 提交**

```bash
git add internal/webapi/upstreams.go internal/webapi/upstreams_test.go web/src/views/Upstreams.vue web/dist
git commit -m "feat: allow responses upstream format in admin API and UI"
```

（web/dist 是嵌入式前端产物，与现有提交模式一致——若 repo 里 web/dist 被 .gitignore 排除，则不 add。）

---

### Task 11: 收尾——全量验证

**Files:** 无新增

- [ ] **Step 1: 全量测试**

Run: `go test ./...`
Expected: 所有包 PASS

Run: `go vet ./...`
Expected: 无输出

- [ ] **Step 2: 前端构建 + 编译产物**

Run: `cd web && npm run build`
Run: `cd .. && go build -o any-llm.exe ./cmd/any-llm/`
Expected: 构建成功

- [ ] **Step 3: 手工冒烟（可选，入站方向实测 DeepSeek）**

启动 `./any-llm.exe`，然后用户自己执行（不要代打 API key）：

```bash
curl -s http://127.0.0.1:8080/v1/responses \
  -H "Authorization: Bearer <网关key>" \
  -H "Content-Type: application/json" \
  -d '{"model":"deepseek/deepseek-v4-flash","input":[{"role":"user","content":[{"type":"input_text","text":"hi"}]}],"stream":true}'
```

验证：收到 `response.created` → … → `response.completed` 的事件序列，completed 里带 usage（含 cached_tokens/reasoning_tokens，若命中缓存）。再测两轮工具循环（见 Task 9 集成测试的 curl 版）与 `previous_response_id` 续接。

- [ ] **Step 4: 检查 git 状态并总结**

Run: `git status` → 无意外文件；`git log --oneline -8` 确认 11 个任务提交齐全。向用户总结：新增格式、改动文件、已知限制（TTL 24h、store 语义放宽、input_file 不支持、reasoning_text 丢弃）。

---

## Self-Review

**1. Spec coverage:**
- 第一节（translate 包 4 文件、映射表、thinking、id 生成）→ Task 1-4 ✓
- 第二节（路由、三处 handler、errors 零改动、client/fetch）→ Task 7-8 ✓
- 第三节（去 CHECK + 备份重建还原 + 会话表 + 应用层校验 + 表单）→ Task 6、10 ✓
- 第四节（SessionStore 流程、失败不存、TTL、清扫、400 错误）→ Task 9 ✓
- 第五节（包单测、cross、网关/上游、会话、DB 迁移、限制说明）→ 各任务测试 + Task 11 冒烟 ✓
- 已知限制（input_file 400、reasoning_text 丢弃、store 放宽、并发读写交错）→ Task 1（未知 part 报错）、Task 3（reasoning_text 忽略）、Task 9（无锁、文档注明）✓

**2. Placeholder scan:** 无 TBD/TODO；所有步骤含具体代码或可执行命令。Task 7/9 测试里 `newTestGateway`、`doCreateUpstream` 标注"复用现有测试辅助/内联"，实现者需先看现有测试文件再决定——这是对既有代码的依赖说明而非占位符。

**3. Type consistency:** `NewStreamEncoder(model, id)` 在 Task 4 定义、Task 7/9 使用一致；`EncodeResponse` 在 Task 2 定义（`resp.ID` 兜底 `NewID()`）、Task 7 使用一致；`NewID()` Task 2 定义、Task 9 dispatch 使用一致；`SessionStore.Get/Put` Task 9 内定义内使用一致；`mapStopFromStatus` 在 Task 2（非流式）与 Task 3（流式）共用，签名一致。
