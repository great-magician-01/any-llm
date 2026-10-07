package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/any-llm/internal/translate"
)

// extractConversationID 的规则优先级与回退。规则表见 convid.go 头注释。
func TestExtractConversationID(t *testing.T) {
	mkReq := func(headers map[string]string) *http.Request {
		r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader("{}"))
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		return r
	}

	ccIR := &translate.Request{Extra: map[string]any{
		"metadata": map[string]any{"user_id": "user_abc_account__session_5f36c286-1111-2222-3333-444455556666"},
	}}

	t.Run("dsh 专用头优先", func(t *testing.T) {
		r := mkReq(map[string]string{
			"x-deepseek-harness-session-id": "sess-1",
			"x-session-id":                  "other",
		})
		got := extractConversationID(r, ccIR, "", nil)
		if got.sessionID != "dsh:sess-1" {
			t.Fatalf("sessionID=%q", got.sessionID)
		}
	})

	t.Run("亲和头按序取第一个非空", func(t *testing.T) {
		r := mkReq(map[string]string{"session_id": "codex-sess"})
		if got := extractConversationID(r, ccIR, "", nil); got.sessionID != "hdr:codex-sess" {
			t.Fatalf("sessionID=%q", got.sessionID)
		}
		r = mkReq(map[string]string{"x-session-affinity": "aff-1"})
		if got := extractConversationID(r, ccIR, "", nil); got.sessionID != "hdr:aff-1" {
			t.Fatalf("sessionID=%q", got.sessionID)
		}
	})

	t.Run("responses 链", func(t *testing.T) {
		sess := &sessionCtx{respID: "resp_new"}
		got := extractConversationID(httptest.NewRequest("POST", "/v1/responses", nil), nil, "resp_old", sess)
		if got.sessionID != "" || got.prevRespID != "resp_old" || got.ownRespID != "resp_new" {
			t.Fatalf("got=%+v", got)
		}
		if got.zero() {
			t.Fatal("responses chain must be non-zero")
		}
		// responses 首轮：仅 ownRespID
		got = extractConversationID(httptest.NewRequest("POST", "/v1/responses", nil), nil, "", sess)
		if got.prevRespID != "" || got.ownRespID != "resp_new" {
			t.Fatalf("first turn: %+v", got)
		}
	})

	t.Run("claude-code metadata", func(t *testing.T) {
		got := extractConversationID(httptest.NewRequest("POST", "/v1/messages", nil), ccIR, "", nil)
		if got.sessionID != "cc:5f36c286-1111-2222-3333-444455556666" {
			t.Fatalf("sessionID=%q", got.sessionID)
		}
	})

	t.Run("metadata 无 session 段不回退", func(t *testing.T) {
		ir := &translate.Request{Extra: map[string]any{"metadata": map[string]any{"user_id": "user_abc"}}}
		got := extractConversationID(httptest.NewRequest("POST", "/v1/messages", nil), ir, "", nil)
		if !got.zero() {
			t.Fatalf("got=%+v, want zero", got)
		}
	})

	t.Run("通用自定义头兜底", func(t *testing.T) {
		r := mkReq(map[string]string{"x-conversation-id": "mine"})
		if got := extractConversationID(r, nil, "", nil); got.sessionID != "hdr:mine" {
			t.Fatalf("sessionID=%q", got.sessionID)
		}
	})

	t.Run("compact 头", func(t *testing.T) {
		r := mkReq(map[string]string{
			"x-deepseek-harness-session-id": "s",
			"x-deepseek-harness-compact":    "1",
		})
		if got := extractConversationID(r, nil, "", nil); !got.compact {
			t.Fatal("compact not detected")
		}
	})

	t.Run("无任何标识 → zero", func(t *testing.T) {
		got := extractConversationID(httptest.NewRequest("POST", "/v1/chat/completions", nil), &translate.Request{}, "", nil)
		if !got.zero() {
			t.Fatalf("got=%+v, want zero", got)
		}
	})

	t.Run("超长 id 截断", func(t *testing.T) {
		long := strings.Repeat("x", 500)
		r := mkReq(map[string]string{"x-conversation-id": long})
		got := extractConversationID(r, nil, "", nil)
		if len(got.sessionID) != sessionIDMaxLen {
			t.Fatalf("len=%d, want %d", len(got.sessionID), sessionIDMaxLen)
		}
	})
}

// sessionTurn 组装：无标识 → nil；OK 轮带内容；错误轮不带内容；
// responses 格式走增量（DeltaMessages），无状态格式走全量（FullMessages）。
func TestConvCtxSessionTurn(t *testing.T) {
	mkCtx := func(inFormat string, conv convInfo) *convCtx {
		return &convCtx{
			createdAt: time.Now(),
			model:     "m",
			inFormat:  inFormat,
			conv:      conv,
			reqIRJSON: []byte(`{"Messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`),
		}
	}
	resp := &translate.Response{Content: []translate.ContentBlock{{Type: "text", Text: "hello"}}}

	if got := mkCtx("openai", convInfo{}).sessionTurn("ok", translate.Usage{}, resp); got != nil {
		t.Fatalf("zero conv must yield nil turn: %+v", got)
	}

	turn := mkCtx("openai", convInfo{sessionID: "cc:x"}).sessionTurn("ok", translate.Usage{InputTokens: 3, OutputTokens: 2}, resp)
	if turn == nil || !turn.OK || turn.FullMessages == nil || turn.DeltaMessages != nil || turn.Assistant == nil {
		t.Fatalf("stateless ok turn: %+v", turn)
	}
	if turn.PromptTokens != 3 || turn.CompletionTokens != 2 {
		t.Fatalf("tokens: %+v", turn)
	}

	turn = mkCtx("responses", convInfo{ownRespID: "resp_1"}).sessionTurn("ok", translate.Usage{}, resp)
	if turn == nil || turn.DeltaMessages == nil || turn.FullMessages != nil {
		t.Fatalf("responses ok turn must use delta: %+v", turn)
	}

	turn = mkCtx("openai", convInfo{sessionID: "cc:x"}).sessionTurn("error", translate.Usage{}, nil)
	if turn == nil || turn.OK || turn.FullMessages != nil || turn.Assistant != nil {
		t.Fatalf("error turn must not carry content: %+v", turn)
	}
}
