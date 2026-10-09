package gateway

import (
	"net/http"
	"strings"

	"github.com/great-magician-01/any-llm/internal/translate"
)

// 会话 id 提取（会话聚合视图的唯一归组依据）。设计见
// docs/superpowers/specs/2026-10-07-conversation-sessions.md。
//
// 有序规则，先中先得；全部未命中返回零值，该请求不进会话表（行为与现状
// 一致）。id 一律带来源前缀防碰撞：
//
//	dsh:   x-deepseek-harness-session-id（dsh llm-deepseek 路径，源码确认）
//	hdr:   会话亲和头（x-session-id / session_id / x-session-affinity，
//	       codex 与 pi-ai 的 session-affinity 惯例）与通用 x-conversation-id
//	resp:  responses 会话链（previous_response_id → last_response_id 定位，
//	       在 store 写入侧解析，见 store.UpsertSessionTurn）
//	cc:    Anthropic metadata.user_id 的 session_ 段（claude-code 惯例
//	       user_xxx_account__session_<uuid>）

// convInfo 是一次请求提取到的会话标识。
type convInfo struct {
	sessionID  string // 已带前缀；responses 链时为空（走 prevRespID 解析）
	prevRespID string // responses 续接的 previous_response_id
	ownRespID  string // responses 本轮 respID
	compact    bool   // x-deepseek-harness-compact: 1（显式历史压缩信号）
}

// zero 表示没有任何会话标识：该请求不进会话表。
func (c convInfo) zero() bool {
	return c.sessionID == "" && c.prevRespID == "" && c.ownRespID == ""
}

// sessionIDMaxLen 限制会话 id 长度（session_id 列 VARCHAR(255)，含前缀）。
const sessionIDMaxLen = 200

// extractConversationID 按有序规则提取会话标识。prevRespID 是 dispatch 在从
// Extra 删除 previous_response_id 之前捕获的值；sess 非空即 responses 格式。
func extractConversationID(r *http.Request, irReq *translate.Request, prevRespID string, sess *sessionCtx) convInfo {
	out := convInfo{compact: r.Header.Get("x-deepseek-harness-compact") == "1"}
	if v := headerID(r, "x-deepseek-harness-session-id"); v != "" {
		out.sessionID = "dsh:" + v
	} else if v := firstHeaderID(r, "x-session-id", "session_id", "x-session-affinity"); v != "" {
		out.sessionID = "hdr:" + v
	} else if sess != nil {
		out.prevRespID = prevRespID
		out.ownRespID = sess.respID
	} else if v := claudeCodeSessionID(irReq); v != "" {
		out.sessionID = "cc:" + v
	} else if v := headerID(r, "x-conversation-id"); v != "" {
		out.sessionID = "hdr:" + v
	}
	if len(out.sessionID) > sessionIDMaxLen {
		// 头值可能含多字节字符，按字节切会切断 rune 并写入非法 UTF-8。
		out.sessionID = cutUTF8(out.sessionID, sessionIDMaxLen)
	}
	return out
}

func headerID(r *http.Request, name string) string {
	return strings.TrimSpace(r.Header.Get(name))
}

func firstHeaderID(r *http.Request, names ...string) string {
	for _, n := range names {
		if v := headerID(r, n); v != "" {
			return v
		}
	}
	return ""
}

// claudeCodeSessionID 从 Anthropic 请求的 metadata.user_id 提取 claude-code
// 会话 uuid（"…session_<uuid>" 的最后一段）。metadata 不在 Anthropic 的已知
// 键表里，解码时原样落在 Extra。
func claudeCodeSessionID(irReq *translate.Request) string {
	if irReq == nil {
		return ""
	}
	md, ok := irReq.Extra["metadata"].(map[string]any)
	if !ok {
		return ""
	}
	uid, _ := md["user_id"].(string)
	i := strings.LastIndex(uid, "session_")
	if i < 0 {
		return ""
	}
	return strings.TrimSpace(uid[i+len("session_"):])
}
