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

// 本文件钉住 ext-key 鉴权的对外契约：**从哪个头取 key**（Authorization 优先、
// 非 Bearer 方案回退到 x-api-key）以及**三种 401 的区分**（没带 / 格式不对 /
// 库里查不到或已禁用）。客户端与运维靠这三种 message 判断是自己漏配还是 key 废了，
// 混成一种会让排障无从下手。

func authHdrSetupUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c1","object":"chat.completion","model":"gpt-4o",` +
			`"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],` +
			`"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
}

// authErr 解析 openai 格式的错误体。
func authErr(t *testing.T, body string) (msg, typ string) {
	t.Helper()
	var resp struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("error body is not the expected JSON shape: %q: %v", body, err)
	}
	return resp.Error.Message, resp.Error.Type
}

// TestGatewayAuth_HeaderPrecedenceAndErrorKinds 覆盖 /v1/chat/completions：
// 两种取 key 的方式、两者同时存在时的优先级、非 Bearer 方案的回退，以及
// 三种 401 各自的 message/type。
func TestGatewayAuth_HeaderPrecedenceAndErrorKinds(t *testing.T) {
	up := authHdrSetupUpstream(t)
	defer up.Close()

	g, d := setupGateway(t)
	uid, _ := store.CreateUpstream(d, &store.Upstream{Name: "oai", BaseURL: up.URL, APIKey: "sk-up", Format: "openai"})
	store.AddModel(d, uid, store.UpstreamModel{ModelName: "gpt-4o"})
	k, _ := store.CreateExtKey(d, "auth-matrix", "", 0, 0, nil)
	g.client = upstream.NewClient(http.DefaultClient)

	// 格式合法（all-sk- 前缀）但库里没有的 key；IsValidKeyFormat 只校验前缀，
	// 所以它会走到 DB 查询并得到「invalid API key」。
	unknownWellFormed := "all-sk-" + strings.Repeat("a", 32)

	cases := []struct {
		name        string
		authHeader  string
		apiKeyHdr   string
		wantCode    int
		wantMessage string
		wantType    string
	}{
		{
			name: "Authorization Bearer 有效", authHeader: "Bearer " + k.Key,
			wantCode: 200,
		},
		{
			name: "x-api-key 有效", apiKeyHdr: k.Key,
			wantCode: 200,
		},
		{
			// 两个头都在时以 Authorization 为准：x-api-key 里放的是有效 key，
			// 但仍然必须被 Authorization 里的坏 key 拒掉。
			name: "两者都在时 Authorization 优先", authHeader: "Bearer " + unknownWellFormed, apiKeyHdr: k.Key,
			wantCode: 401, wantMessage: "invalid API key", wantType: "authentication_error",
		},
		{
			// Authorization 不是 Bearer 方案（例如 Basic）时不参与取 key，
			// 回退到 x-api-key。
			name: "非 Bearer 方案回退到 x-api-key", authHeader: "Basic dXNlcjpwYXNz", apiKeyHdr: k.Key,
			wantCode: 200,
		},
		{
			name: "两个头都没带", wantCode: 401,
			wantMessage: "missing API key", wantType: "authentication_error",
		},
		{
			name: "Bearer 后为空串", authHeader: "Bearer ",
			wantCode: 401, wantMessage: "missing API key", wantType: "authentication_error",
		},
		{
			name: "没有 all-sk- 前缀", authHeader: "Bearer sk-proj-abcdef",
			wantCode: 401, wantMessage: "invalid API key format", wantType: "authentication_error",
		},
		{
			name: "格式合法但库里不存在", authHeader: "Bearer " + unknownWellFormed,
			wantCode: 401, wantMessage: "invalid API key", wantType: "authentication_error",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/v1/chat/completions",
				strings.NewReader(`{"model":"oai/gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
			if c.authHeader != "" {
				req.Header.Set("Authorization", c.authHeader)
			}
			if c.apiKeyHdr != "" {
				req.Header.Set("x-api-key", c.apiKeyHdr)
			}
			w := httptest.NewRecorder()
			g.ServeHTTP(w, req)

			if w.Code != c.wantCode {
				t.Fatalf("status=%d want %d (body=%s)", w.Code, c.wantCode, w.Body.String())
			}
			if c.wantMessage == "" {
				return
			}
			msg, typ := authErr(t, w.Body.String())
			if msg != c.wantMessage || typ != c.wantType {
				t.Errorf("error=%q/%q want %q/%q", msg, typ, c.wantMessage, c.wantType)
			}
		})
	}
}

// TestGatewayAuth_ModelsEndpointUsesSameExtraction 覆盖 /v1/models：它与完成
// 端点同级、同样强制 ext key，并且支持 x-api-key（这条此前只有 Anthropic 的
// /v1/messages 用过 x-api-key，/v1/models 没测）。
func TestGatewayAuth_ModelsEndpointUsesSameExtraction(t *testing.T) {
	g, d := setupGateway(t)
	uid, _ := store.CreateUpstream(d, &store.Upstream{Name: "oai", BaseURL: "https://example.invalid", APIKey: "sk", Format: "openai"})
	store.AddModel(d, uid, store.UpstreamModel{ModelName: "gpt-4o"})
	k, _ := store.CreateExtKey(d, "models-auth", "", 0, 0, nil)

	cases := []struct {
		name        string
		authHeader  string
		apiKeyHdr   string
		wantCode    int
		wantMessage string
	}{
		{name: "x-api-key 可用", apiKeyHdr: k.Key, wantCode: 200},
		{name: "Bearer 可用", authHeader: "Bearer " + k.Key, wantCode: 200},
		{name: "缺 key", wantCode: 401, wantMessage: "missing API key"},
		{name: "前缀不对", authHeader: "Bearer nope", wantCode: 401, wantMessage: "invalid API key format"},
		{name: "前缀对但不存在", authHeader: "Bearer all-sk-" + strings.Repeat("z", 32), wantCode: 401, wantMessage: "invalid API key"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/v1/models", nil)
			if c.authHeader != "" {
				req.Header.Set("Authorization", c.authHeader)
			}
			if c.apiKeyHdr != "" {
				req.Header.Set("x-api-key", c.apiKeyHdr)
			}
			w := httptest.NewRecorder()
			g.ServeHTTP(w, req)

			if w.Code != c.wantCode {
				t.Fatalf("status=%d want %d (body=%s)", w.Code, c.wantCode, w.Body.String())
			}
			if c.wantMessage != "" {
				if msg, _ := authErr(t, w.Body.String()); msg != c.wantMessage {
					t.Errorf("message=%q want %q", msg, c.wantMessage)
				}
			}
		})
	}
}

// TestGatewayAuth_DisabledKeyIsIndistinguishable：已禁用的 key 必须与「不存在
// 的 key」返回完全一样的 401（不泄漏「这个 key 存在但被禁用了」的信息）。
func TestGatewayAuth_DisabledKeyIsIndistinguishable(t *testing.T) {
	up := authHdrSetupUpstream(t)
	defer up.Close()

	g, d := setupGateway(t)
	uid, _ := store.CreateUpstream(d, &store.Upstream{Name: "oai", BaseURL: up.URL, APIKey: "sk-up", Format: "openai"})
	store.AddModel(d, uid, store.UpstreamModel{ModelName: "gpt-4o"})
	k, _ := store.CreateExtKey(d, "disabled-key", "", 0, 0, nil)
	g.client = upstream.NewClient(http.DefaultClient)

	// 通过 store 更新为禁用（enabled=false）。
	if err := store.UpdateExtKey(d, k.ID, k.Label, "", false, 0, 0, nil); err != nil {
		t.Fatalf("disable key: %v", err)
	}

	req := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"oai/gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)

	if w.Code != 401 {
		t.Fatalf("disabled key status=%d want 401 (body=%s)", w.Code, w.Body.String())
	}
	msg, typ := authErr(t, w.Body.String())
	if msg != "invalid API key" || typ != "authentication_error" {
		t.Errorf("disabled key error=%q/%q want the same as an unknown key", msg, typ)
	}
}
