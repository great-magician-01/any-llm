package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/great-magician-01/any-llm/internal/store"
	"github.com/great-magician-01/any-llm/internal/upstream"
)

// 本文件钉住「上游状态码 → 客户端错误类型」的映射表与错误体外形。客户端 SDK
// 按 error.type 决定是否重试（rate_limit/overloaded 会退避重试，invalid_request
// 直接失败），映射错了会表现为"重试风暴"或"该重试的不重试"。此前只有 401/429
// 被端到端触及，其余状态码的映射无人守。

// TestMapErrorType_StatusToClientType 逐格覆盖映射表（含未列出的状态码兜底）。
func TestMapErrorType_StatusToClientType(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{400, "invalid_request_error"},
		{401, "authentication_error"},
		{403, "permission_error"},
		{404, "not_found_error"},
		{413, "request_too_large"},
		{429, "rate_limit_error"},
		{529, "overloaded_error"}, // Anthropic 特有的"过载"
		{500, "api_error"},
		{503, "api_error"},
		{418, "api_error"}, // 未列出的 4xx 兜底
	}
	for _, c := range cases {
		if got := mapErrorType("anthropic", c.status, ""); got != c.want {
			t.Errorf("mapErrorType(anthropic, %d) = %q, want %q", c.status, got, c.want)
		}
	}
}

// TestMapErrorType_OpenAIAndResponsesShareTheOpenAIShape：非 Anthropic 出站用
// 同一张表（responses 与 openai 都走 default 分支），5xx 映射成 server_error，
// 且 **413 在 openai 侧没有专门类型**（与 anthropic 的 request_too_large 不对称，
// 这是现状，改动时要知道会同时影响两种格式）。
func TestMapErrorType_OpenAIAndResponsesShareTheOpenAIShape(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{400, "invalid_request_error"},
		{401, "authentication_error"},
		{403, "permission_error"},
		{404, "not_found_error"},
		{413, "api_error"}, // openai 侧没有 request_too_large
		{429, "rate_limit_error"},
		{500, "server_error"},
		{502, "server_error"},
		{529, "server_error"}, // anthropic 的 529 在 openai 侧没有专门类型
		{418, "api_error"},
	}
	for _, format := range []string{"openai", "responses"} {
		for _, c := range cases {
			if got := mapErrorType(format, c.status, ""); got != c.want {
				t.Errorf("mapErrorType(%s, %d) = %q, want %q", format, c.status, got, c.want)
			}
		}
	}
}

// TestWriteError_ShapePerFormat 覆盖错误体外形：Anthropic 客户端要求顶层
// type=error 且错误类型嵌在 error.type；OpenAI/Responses 只有 error.type。
// 外形错了 SDK 会解析不出错误类型（表现为"未知错误"）。
func TestWriteError_ShapePerFormat(t *testing.T) {
	t.Run("anthropic", func(t *testing.T) {
		w := httptest.NewRecorder()
		WriteError(w, 429, "anthropic", "slow down", "rate_limit_error")

		if w.Code != 429 {
			t.Errorf("status=%d want 429", w.Code)
		}
		if ct := w.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type=%q want application/json", ct)
		}
		var body struct {
			Type  string `json:"type"`
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("body=%q: %v", w.Body.String(), err)
		}
		if body.Type != "error" {
			t.Errorf("top-level type=%q want error", body.Type)
		}
		if body.Error.Type != "rate_limit_error" || body.Error.Message != "slow down" {
			t.Errorf("error=%+v", body.Error)
		}
	})

	for _, format := range []string{"openai", "responses"} {
		t.Run(format, func(t *testing.T) {
			w := httptest.NewRecorder()
			WriteError(w, 400, format, "bad request", "invalid_request_error")

			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("body=%q: %v", w.Body.String(), err)
			}
			if _, ok := body["type"]; ok {
				t.Errorf("openai/responses error body must not carry a top-level type: %v", body)
			}
			errObj, ok := body["error"].(map[string]any)
			if !ok {
				t.Fatalf("error object missing: %v", body)
			}
			if errObj["type"] != "invalid_request_error" || errObj["message"] != "bad request" {
				t.Errorf("error=%v", errObj)
			}
		})
	}
}

// TestCompletion_Upstream5xxMapsToServerError 端到端验证映射真的接在请求链路上：
// 上游 503 → openai 客户端拿到 server_error（客户端据此决定重试），且 usage
// 记录如实标记 error。
func TestCompletion_Upstream5xxMapsToServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		_, _ = w.Write([]byte(`{"error":{"message":"upstream down","type":"server_error"}}`))
	}))
	defer srv.Close()

	g, d := setupGateway(t)
	uid, _ := store.CreateUpstream(d, &store.Upstream{Name: "oai", BaseURL: srv.URL, APIKey: "sk", Format: "openai"})
	store.AddModel(d, uid, store.UpstreamModel{ModelName: "gpt-4o"})
	k, _ := store.CreateExtKey(d, "err-map", "", 0, 0, nil)
	g.client = upstream.NewClient(http.DefaultClient)

	req := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"oai/gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)

	if w.Code != 503 {
		t.Fatalf("status=%d want 503 (上游状态码透传)", w.Code)
	}
	msg, typ := authErr(t, w.Body.String())
	if typ != "server_error" {
		t.Errorf("error.type=%q want server_error", typ)
	}
	if !strings.Contains(msg, "upstream down") {
		t.Errorf("error.message=%q want the upstream message", msg)
	}

	records, _, _ := store.UsageRecordsList(d, 1, 10)
	if len(records) != 1 || records[0].Status != "error" {
		t.Fatalf("records=%+v want one with status=error", records)
	}
}
