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
	Type      string    `json:"type,omitempty"` // "function_call" | "function_call_output"（角色消息无此字段）
	Role      string    `json:"role,omitempty"` // "user" | "assistant" | "system"
	Content   []rawPart `json:"content,omitempty"`
	CallID    string    `json:"call_id,omitempty"`
	Name      string    `json:"name,omitempty"`
	Arguments string    `json:"arguments,omitempty"` // JSON 字符串
	Output    string    `json:"output,omitempty"`    // function_call_output 的结果字符串
}

type rawPart struct {
	Type     string `json:"type"` // "input_text" | "input_image" | 其他未知类型
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"` // 图片 URL 或 data URL
}

type rawTool struct {
	Type        string          `json:"type"` // 恒为 "function"
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// 已知 key 表：用于 extractExtra（store/previous_response_id 单独处理，text 忽略）。
var knownRequestKeys = map[string]bool{
	"model": true, "instructions": true, "input": true, "tools": true,
	"tool_choice": true, "max_output_tokens": true, "stream": true,
	"store": true, "previous_response_id": true, "text": true,
}

// Response (non-stream)
type rawResponse struct {
	ID                string                `json:"id"`
	Object            string                `json:"object,omitempty"`
	CreatedAt         int64                 `json:"created_at,omitempty"`
	Status            string                `json:"status,omitempty"` // completed | incomplete | failed
	Model             string                `json:"model,omitempty"`
	Output            []rawOutputItem       `json:"output,omitempty"`
	Usage             *rawUsage             `json:"usage,omitempty"`
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
	InputTokens         int                     `json:"input_tokens"`
	OutputTokens        int                     `json:"output_tokens"`
	TotalTokens         int                     `json:"total_tokens"`
	InputTokensDetails  *rawInputTokensDetails  `json:"input_tokens_details,omitempty"`
	OutputTokensDetails *rawOutputTokensDetails `json:"output_tokens_details,omitempty"`
}

type rawInputTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type rawOutputTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
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
