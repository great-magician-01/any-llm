package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/great-magician-01/any-llm/internal/model"
	"github.com/great-magician-01/any-llm/internal/upstream"
)

func TestCompletion_NonStreamOpenAI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"Hello!"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`))
	}))
	defer srv.Close()

	g, d := setupGateway(t)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "oai", BaseURL: srv.URL, APIKey: "sk-test", Format: "openai"})
	model.AddModel(d, uid, "gpt-4o", false, 0, 0)
	k, _ := model.CreateExtKey(d, "test", 0, 0)
	g.client = upstream.NewClient(http.DefaultClient)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"oai/gpt-4o","messages":[{"role":"user","content":"hi"}],"max_tokens":50}`))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Hello!") {
		t.Fatalf("body=%s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "usage") {
		t.Fatalf("no usage in response: %s", w.Body.String())
	}

	records, total, _ := model.UsageRecordsList(d, 1, 10)
	if total != 1 {
		t.Fatalf("usage records=%d", total)
	}
	if records[0].TotalTokens != 15 {
		t.Fatalf("recorded tokens=%d", records[0].TotalTokens)
	}
}

func TestCompletion_StreamOpenAI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		w.Write([]byte("data: {\"id\":\"c1\",\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Hi\"}}]}\n\n"))
		f.Flush()
		w.Write([]byte("data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\n"))
		f.Flush()
		w.Write([]byte("data: [DONE]\n\n"))
		f.Flush()
	}))
	defer srv.Close()

	g, d := setupGateway(t)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "oai", BaseURL: srv.URL, APIKey: "sk-test", Format: "openai"})
	model.AddModel(d, uid, "gpt-4o", false, 0, 0)
	k, _ := model.CreateExtKey(d, "test", 0, 0)
	g.client = upstream.NewClient(http.DefaultClient)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"oai/gpt-4o","messages":[{"role":"user","content":"hi"}],"max_tokens":50,"stream":true}`))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status=%d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "data: ") {
		t.Fatalf("no SSE data: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "[DONE]") {
		t.Fatalf("no [DONE]: %s", w.Body.String())
	}
	records, total, _ := model.UsageRecordsList(d, 1, 10)
	if total != 1 {
		t.Fatalf("usage records=%d", total)
	}
	if records[0].TotalTokens != 5 {
		t.Fatalf("recorded tokens=%d", records[0].TotalTokens)
	}
	if !records[0].Stream {
		t.Fatal("stream flag not set")
	}
}

func TestCompletion_CrossFormat_AnthropicInOpenAIUp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"cross-format works"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":4,"total_tokens":11}}`))
	}))
	defer srv.Close()

	g, d := setupGateway(t)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "oai", BaseURL: srv.URL, APIKey: "sk-test", Format: "openai"})
	model.AddModel(d, uid, "gpt-4o", false, 0, 0)
	k, _ := model.CreateExtKey(d, "test", 0, 0)
	g.client = upstream.NewClient(http.DefaultClient)

	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"oai/gpt-4o","max_tokens":50,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("x-api-key", k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "cross-format works") {
		t.Fatalf("body=%s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"type":"message"`) {
		t.Fatalf("not anthropic format: %s", w.Body.String())
	}
	records, _, _ := model.UsageRecordsList(d, 1, 10)
	if records[0].InFormat != "anthropic" || records[0].UpFormat != "openai" {
		t.Fatalf("formats=%+v", records[0])
	}
}

func TestCompletion_UpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		w.Write([]byte(`{"error":{"message":"rate limited","type":"rate_limit_error"}}`))
	}))
	defer srv.Close()

	g, d := setupGateway(t)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "oai", BaseURL: srv.URL, APIKey: "sk-test", Format: "openai"})
	model.AddModel(d, uid, "gpt-4o", false, 0, 0)
	k, _ := model.CreateExtKey(d, "test", 0, 0)
	g.client = upstream.NewClient(http.DefaultClient)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"oai/gpt-4o","messages":[{"role":"user","content":"hi"}],"max_tokens":50}`))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)

	if w.Code != 429 {
		t.Fatalf("status=%d want 429", w.Code)
	}
	if !strings.Contains(w.Body.String(), "rate limited") {
		t.Fatalf("body=%s", w.Body.String())
	}

	records, _, _ := model.UsageRecordsList(d, 1, 10)
	if len(records) != 1 || records[0].Status != "error" {
		t.Fatalf("records=%+v", records)
	}
}

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

// 流式请求被上游以非流式 JSON 应答（Content-Type: application/json）：
// responses 客户端必须拿到 responses 形状（object:response）且响应 id 与会话
// key 一致（resp_ 前缀），这样该 id 才能作为 previous_response_id 续接。
func TestResponsesStreamFallbackNonStreamJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"c1","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"Hi"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
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

	// 第一轮：流式 + store，上游用非流式 JSON 应答
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(
		`{"model":"mock/m","stream":true,"store":true,"input":[{"role":"user","content":[{"type":"input_text","text":"hi"}]}]}`))
	req1.Header.Set("Authorization", "Bearer "+k.Key)
	g.ServeHTTP(rec1, req1)
	if rec1.Code != 200 {
		t.Fatalf("turn1 status=%d body=%s", rec1.Code, rec1.Body.String())
	}
	body1 := rec1.Body.String()
	if !strings.Contains(body1, `"object":"response"`) {
		t.Fatalf("turn1 not responses-shaped: %s", body1)
	}
	// 从 SSE data 载荷里取响应 id
	id1 := ""
	for _, line := range strings.Split(body1, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev struct {
			Response *struct {
				ID string `json:"id"`
			} `json:"response"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
			continue
		}
		if ev.Response != nil && ev.Response.ID != "" {
			id1 = ev.Response.ID
			break
		}
	}
	if !strings.HasPrefix(id1, "resp_") {
		t.Fatalf("turn1 id=%q (want session-stamped resp_ id)", id1)
	}

	// 第二轮：用第一轮的 id 续接（previous_response_id），必须 200 而非 400
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(fmt.Sprintf(
		`{"model":"mock/m","previous_response_id":"%s","input":[{"role":"user","content":[{"type":"input_text","text":"more"}]}]}`, id1)))
	req2.Header.Set("Authorization", "Bearer "+k.Key)
	g.ServeHTTP(rec2, req2)
	if rec2.Code != 200 {
		t.Fatalf("turn2 status=%d body=%s (previous_response_id=%s)", rec2.Code, rec2.Body.String(), id1)
	}
}

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

	// 会话字段不得转发给上游（两轮都检查）
	for i, body := range upstreamCalls {
		if _, ok := body["previous_response_id"]; ok {
			t.Fatalf("round %d: previous_response_id forwarded upstream: %v", i+1, body)
		}
		if _, ok := body["store"]; ok {
			t.Fatalf("round %d: store forwarded upstream: %v", i+1, body)
		}
	}
}

// 调用失败不得写会话：非流式上游 500、流式中途断开（unexpected EOF）都不存。
func TestResponsesFailedCallDoesNotSave(t *testing.T) {
	// 非流式：上游 500
	srv500 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		fmt.Fprint(w, `{"error":{"message":"boom","type":"internal_error"}}`)
	}))
	defer srv500.Close()

	g, d := setupGateway(t)
	uid, err := model.CreateUpstream(d, &model.Upstream{Name: "mock", BaseURL: srv500.URL, APIKey: "sk-x", Format: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	model.AddModel(d, uid, "m", false, 0, 0)
	k, _ := model.CreateExtKey(d, "test", 0, 0)
	g.client = upstream.NewClient(http.DefaultClient)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(
		`{"model":"mock/m","store":true,"input":[{"role":"user","content":[{"type":"input_text","text":"hi"}]}]}`))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	g.ServeHTTP(rec, req)
	if rec.Code != 500 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var n int
	if err := g.sessions.db.QueryRow(`SELECT COUNT(*) FROM response_sessions`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("failed call saved %d sessions", n)
	}
	// 同一 previous_response_id 续接应 400：失败调用没留下任何会话
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(
		`{"model":"mock/m","previous_response_id":"resp_never","input":[{"role":"user","content":[{"type":"input_text","text":"hi"}]}]}`))
	req2.Header.Set("Authorization", "Bearer "+k.Key)
	g.ServeHTTP(rec2, req2)
	if rec2.Code != 400 || !strings.Contains(rec2.Body.String(), "invalid_previous_response_id") {
		t.Fatalf("follow-up status=%d body=%s", rec2.Code, rec2.Body.String())
	}

	// 流式：上游发一段文本后中途断开（声明 Content-Length 但未写完就关连接 → 客户端 unexpected EOF）
	srvAbort := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("no hijacker")
			return
		}
		conn, buf, err := hj.Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		fmt.Fprintf(buf, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nContent-Length: 10000\r\n\r\n")
		fmt.Fprintf(buf, "data: {\"id\":\"c1\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"partial\"}}]}\n\n")
		buf.Flush()
	}))
	defer srvAbort.Close()

	g2, d2 := setupGateway(t)
	uid2, err := model.CreateUpstream(d2, &model.Upstream{Name: "mock2", BaseURL: srvAbort.URL, APIKey: "sk-x", Format: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	model.AddModel(d2, uid2, "m", false, 0, 0)
	k2, _ := model.CreateExtKey(d2, "test", 0, 0)
	g2.client = upstream.NewClient(http.DefaultClient)

	recS := httptest.NewRecorder()
	reqS := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(
		`{"model":"mock2/m","stream":true,"store":true,"input":[{"role":"user","content":[{"type":"input_text","text":"hi"}]}]}`))
	reqS.Header.Set("Authorization", "Bearer "+k2.Key)
	g2.ServeHTTP(recS, reqS)
	var n2 int
	if err := g2.sessions.db.QueryRow(`SELECT COUNT(*) FROM response_sessions`).Scan(&n2); err != nil {
		t.Fatal(err)
	}
	if n2 != 0 {
		t.Fatalf("aborted stream saved %d sessions", n2)
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

// TestCompletion_UpstreamError_AnthropicOut verifies that when the client uses
// the Anthropic format and the upstream returns an error, the gateway extracts
// the human-readable message (rather than nesting the raw JSON body) and maps
// the status code to an Anthropic-appropriate error type.
func TestCompletion_UpstreamError_AnthropicOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"type":"error","error":{"type":"AuthError","message":"Invalid API key."}}`))
	}))
	defer srv.Close()

	g, d := setupGateway(t)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "oai", BaseURL: srv.URL, APIKey: "sk-test", Format: "openai"})
	model.AddModel(d, uid, "gpt-4o", false, 0, 0)
	k, _ := model.CreateExtKey(d, "test", 0, 0)
	g.client = upstream.NewClient(http.DefaultClient)

	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"oai/gpt-4o","max_tokens":50,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("x-api-key", k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)

	if w.Code != 401 {
		t.Fatalf("status=%d want 401", w.Code)
	}
	var resp struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, w.Body.String())
	}
	if resp.Type != "error" {
		t.Fatalf("type=%q want error", resp.Type)
	}
	if resp.Error.Type != "authentication_error" {
		t.Fatalf("error.type=%q want authentication_error", resp.Error.Type)
	}
	if resp.Error.Message != "Invalid API key." {
		t.Fatalf("error.message=%q want \"Invalid API key.\"", resp.Error.Message)
	}
	// Ensure the raw JSON body is NOT embedded as a string inside message.
	if strings.HasPrefix(resp.Error.Message, "{") {
		t.Fatalf("message should be plain text, got: %s", resp.Error.Message)
	}

	records, _, _ := model.UsageRecordsList(d, 1, 10)
	if len(records) != 1 || records[0].Status != "error" {
		t.Fatalf("records=%+v", records)
	}
}
