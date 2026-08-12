package gateway

import (
	"encoding/json"
	"sort"

	"github.com/great-magician-01/any-llm/internal/translate"
)

// streamRecorder 把 IR 流事件累积成有序的 ContentBlock，供对话归档。
// 格式无关（消费统一的 IR StreamEvent），照搬 responses 编码器的 map 累积
// 思路，但额外捕获思维链的真实签名（signature_delta）——responses 编码器的
// Content() 把 Signature 存成了合成的 itemID，不能用于归档。
type streamRecorder struct {
	msgID      string
	stopReason string
	blockKind  map[int]string             // index -> text | thinking | tool_use
	textBuf    map[int]string             // text 块累积文本
	thinkBuf   map[int]string             // thinking 块累积文本
	sigBuf     map[int]string             // thinking 块真实签名
	toolArgs   map[int]string             // tool_use 块累积的 input JSON
	toolMeta   map[int]*translate.ToolUse // tool_use 块的 ID/Name
}

func newStreamRecorder() *streamRecorder {
	return &streamRecorder{
		blockKind: make(map[int]string),
		textBuf:   make(map[int]string),
		thinkBuf:  make(map[int]string),
		sigBuf:    make(map[int]string),
		toolArgs:  make(map[int]string),
		toolMeta:  make(map[int]*translate.ToolUse),
	}
}

// Add 消费一个 IR 流事件。content_block_delta 对缺失 content_block_start 的
// 块做惰性开块（容忍 DeepSeek 这类省略 start 的上游）。
func (s *streamRecorder) Add(ev *translate.StreamEvent) {
	if ev == nil {
		return
	}
	switch ev.Type {
	case "message_start":
		if ev.MessageID != "" {
			s.msgID = ev.MessageID
		}
	case "message_delta":
		if ev.StopReason != "" {
			s.stopReason = ev.StopReason
		}
	case "content_block_start":
		if ev.Block == nil {
			return
		}
		idx := ev.Index
		switch ev.Block.Type {
		case "thinking":
			s.blockKind[idx] = "thinking"
			if ev.Block.Thinking != "" {
				s.thinkBuf[idx] += ev.Block.Thinking
			}
			if ev.Block.Signature != "" {
				s.sigBuf[idx] = ev.Block.Signature
			}
		case "tool_use":
			s.blockKind[idx] = "tool_use"
			if ev.Block.ToolUse != nil {
				s.toolMeta[idx] = ev.Block.ToolUse
				// 起始块可能已带完整 input；非空且非 "{}" 时预填。
				if in := ev.Block.ToolUse.Input; len(in) > 0 && string(in) != "{}" {
					s.toolArgs[idx] = string(in)
				}
			}
		default: // text 及其余（含 hosted server 块）
			if ev.Block.Type == "text" || ev.Block.Extra == nil {
				s.blockKind[idx] = "text"
				if ev.Block.Text != "" {
					s.textBuf[idx] += ev.Block.Text
				}
				return
			}
			// server_tool_use / web_search_tool_result 等未知块：保留真实
			// type，内容（Extra）序列化后归档。
			s.blockKind[idx] = ev.Block.Type
			if b, err := json.Marshal(ev.Block.Extra); err == nil {
				s.textBuf[idx] += string(b)
			}
		}
	case "content_block_delta":
		if ev.Delta == nil {
			return
		}
		idx := ev.Index
		switch ev.Delta.Type {
		case "text_delta":
			s.ensureKind(idx, "text")
			s.textBuf[idx] += ev.Delta.Text
		case "input_json_delta":
			s.ensureKind(idx, "tool_use")
			s.toolArgs[idx] += ev.Delta.PartialJSON
		case "thinking_delta":
			s.ensureKind(idx, "thinking")
			s.thinkBuf[idx] += ev.Delta.Thinking
		case "signature_delta":
			s.ensureKind(idx, "thinking")
			s.sigBuf[idx] = ev.Delta.Signature
		}
	}
}

// ensureKind 在块尚未开块时按 delta 类型推断并记录其 kind。
func (s *streamRecorder) ensureKind(idx int, kind string) {
	if _, ok := s.blockKind[idx]; !ok {
		s.blockKind[idx] = kind
	}
}

// Content 按块 index 升序产出累积的 ContentBlock。
func (s *streamRecorder) Content() []translate.ContentBlock {
	idxs := make([]int, 0, len(s.blockKind))
	for idx := range s.blockKind {
		idxs = append(idxs, idx)
	}
	sort.Ints(idxs)
	out := make([]translate.ContentBlock, 0, len(idxs))
	for _, idx := range idxs {
		switch s.blockKind[idx] {
		case "thinking":
			out = append(out, translate.ContentBlock{
				Type:      "thinking",
				Thinking:  s.thinkBuf[idx],
				Signature: s.sigBuf[idx],
			})
		case "tool_use":
			tu := &translate.ToolUse{Input: json.RawMessage(s.toolArgs[idx])}
			if tm := s.toolMeta[idx]; tm != nil {
				tu.ID = tm.ID
				tu.Name = tm.Name
			}
			out = append(out, translate.ContentBlock{Type: "tool_use", ToolUse: tu})
		default:
			// 未知块类型（server 块）：还原 Extra 供归档 IR 展示。
			if kind := s.blockKind[idx]; kind != "text" {
				var extra map[string]any
				_ = json.Unmarshal([]byte(s.textBuf[idx]), &extra)
				out = append(out, translate.ContentBlock{Type: kind, Extra: extra})
				continue
			}
			out = append(out, translate.ContentBlock{Type: "text", Text: s.textBuf[idx]})
		}
	}
	return out
}
