package upstream

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/any-llm/internal/store"
	"github.com/great-magician-01/any-llm/internal/translate"
)

// ---------------------------------------------------------------------------
// 本文件补 upstream.Client.Call 的错误/边界路径。业务规则写在每个用例上方。
// helper 统一加 biz 前缀，避免与包内既有 helper（cloneHeader/registerTestHost
// /setupAPI 等）重名。
// ---------------------------------------------------------------------------

func bizUpstream(baseURL, format, apiKey string) *store.Upstream {
	return &store.Upstream{Name: "biz", BaseURL: baseURL, APIKey: apiKey, Format: format}
}

func bizTextReq(stream bool) *translate.Request {
	return &translate.Request{
		Model:     "m",
		Stream:    stream,
		MaxTokens: 16,
		Messages:  []translate.Message{{Role: "user", Content: []translate.ContentBlock{{Type: "text", Text: "hi"}}}},
	}
}

const bizTruncMarker = "...(truncated)"

// 业务规则：上游非 2xx 必须映射成 *UpstreamError（不是裸 error），原始状态码
// 与格式透传；Message() 从上游 body 的 error.message 提取（OpenAI / Anthropic
// 两种错误外形都认），body 不是可解析 JSON 时回退成原文；Error() 里带状态码。
// 网关据此把上游状态码映射成客户端格式的错误类型，所以状态码透传是硬约束。
func TestCallBiz_UpstreamErrorStatusAndMessage(t *testing.T) {
	cases := []struct {
		name     string
		format   string
		status   int
		body     string
		wantMsg  string
		wantType string
	}{
		{
			name: "openai 429", format: "openai", status: 429,
			body:     `{"error":{"message":"rate limited","type":"rate_limit_error"}}`,
			wantMsg:  "rate limited",
			wantType: "rate_limit_error",
		},
		{
			name: "anthropic 401", format: "anthropic", status: 401,
			body:     `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`,
			wantMsg:  "invalid x-api-key",
			wantType: "authentication_error",
		},
		{
			name: "responses 400 raw body fallback", format: "responses", status: 400,
			body:     `boom`,
			wantMsg:  "boom",
			wantType: "",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer srv.Close()

			c := NewClient(srv.Client())
			res, err := c.Call(context.Background(), bizUpstream(srv.URL, tc.format, "sk-up"), bizTextReq(false), nil)
			if err == nil {
				t.Fatal("expected error for non-2xx upstream")
			}
			if res != nil {
				t.Fatalf("result must be nil on error, got %+v", res)
			}
			var ue *UpstreamError
			if !errors.As(err, &ue) {
				t.Fatalf("error type = %T (%v), want *UpstreamError", err, err)
			}
			if ue.StatusCode != tc.status {
				t.Fatalf("status passthrough = %d, want %d", ue.StatusCode, tc.status)
			}
			if ue.Format != tc.format {
				t.Fatalf("format = %q, want %q", ue.Format, tc.format)
			}
			if got := ue.Message(); got != tc.wantMsg {
				t.Fatalf("Message() = %q, want %q", got, tc.wantMsg)
			}
			if got := ue.ErrorType(); got != tc.wantType {
				t.Fatalf("ErrorType() = %q, want %q", got, tc.wantType)
			}
			if string(ue.Body) != tc.body {
				t.Fatalf("Body = %q, want %q", ue.Body, tc.body)
			}
			if !strings.Contains(err.Error(), strconv.Itoa(tc.status)) {
				t.Fatalf("Error() = %q, must carry status %d", err.Error(), tc.status)
			}
		})
	}
}

// 业务规则：上游错误 body 写日志前必须按 512 字节截断并加 "...(truncated)"
// 标记——一个超大厂商错误页（HTML/网关模板）不能整段进日志。恰好等于上限不截断，
// 超一个字节即截断。
func TestBizTruncateUpstream512(t *testing.T) {
	if got := truncateUpstream("short", 512); got != "short" {
		t.Fatalf("short body = %q, want unchanged", got)
	}
	exact := strings.Repeat("y", 512)
	if got := truncateUpstream(exact, 512); got != exact {
		t.Fatalf("body exactly at the cap must not be truncated: len=%d", len(got))
	}
	big := strings.Repeat("x", 4096)
	got := truncateUpstream(big, 512)
	if len(got) != 512+len(bizTruncMarker) {
		t.Fatalf("truncated length = %d, want %d", len(got), 512+len(bizTruncMarker))
	}
	if got[:512] != big[:512] || !strings.HasSuffix(got, bizTruncMarker) {
		t.Fatalf("truncated body malformed: %q", got[:40])
	}
	if got := truncateUpstream(strings.Repeat("z", 513), 512); !strings.HasSuffix(got, bizTruncMarker) {
		t.Fatalf("513-byte body must be truncated: %q", got[len(got)-20:])
	}
}

// 业务规则：上游返回超长错误 body 时 Call 不得 panic、不得丢掉状态码，返回的
// *UpstreamError 仍携带完整 body（网关要按状态码映射错误类型）。
//
// 注意（已记入交付报告）：512 字节截断只作用于日志行（truncateUpstream），
// UpstreamError.Body / Message() 不做截断，会被 admin/gateway 原样回给调用方。
// 本用例钉住现状，超长 body 下的不 panic 与状态码透传。
func TestCallBiz_HugeErrorBodyNoPanic(t *testing.T) {
	huge := strings.Repeat("E", 300000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		io.WriteString(w, huge)
	}))
	defer srv.Close()

	c := NewClient(srv.Client())
	_, err := c.Call(context.Background(), bizUpstream(srv.URL, "openai", "sk-up"), bizTextReq(false), nil)
	var ue *UpstreamError
	if !errors.As(err, &ue) {
		t.Fatalf("error = %v (%T), want *UpstreamError", err, err)
	}
	if ue.StatusCode != 503 {
		t.Fatalf("status = %d, want 503", ue.StatusCode)
	}
	if len(ue.Body) != len(huge) {
		t.Fatalf("body len = %d, want %d (current contract: raw body preserved)", len(ue.Body), len(huge))
	}
	// 日志侧使用的截断函数在超长 body 上必须仍然安全且有界。
	if got := truncateUpstream(string(ue.Body), 512); len(got) > 512+len(bizTruncMarker) {
		t.Fatalf("log-side truncation unbounded: len=%d", len(got))
	}
}

// 业务规则：SSE 解析必须容忍真实厂商帧格式——注释行（":" 开头）、非 data 字段
// （event:/id:/retry:）、空 payload（"data:" / "data: "）、无空格的 "data:xxx"、
// 以及跨 Write 拆分的半包（必须重组完整行）；非法 JSON 行只跳过自己，不能中断
// 后续事件。
func TestCallBiz_SSEParsingBoundaries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("response writer is not a Flusher")
			return
		}
		write := func(s string) {
			io.WriteString(w, s)
			flusher.Flush()
		}
		// 注释行 / 其它字段 / 空 payload —— 全部应被跳过
		write(": keep-alive comment\n\n")
		write("event: message\n\n")
		write("id: 42\n\n")
		write("retry: 1000\n\n")
		write("data: \n\n")
		write("data:\n\n")
		write("no-colon-line\n\n")
		// 无空格的 data: 也要认，并产生 message_start + 文本 "A"
		write(`data:{"id":"c1","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"A"}}]}` + "\n\n")
		// 非法 JSON：跳过本行，不能影响后面的 "B"
		write("data: {this is not json}\n\n")
		write("data: {\"id\":\"c1\",\"choices\":\"bad-shape\"}\n\n")
		// 半包：同一行分两次 Write 发出（中间 Flush），解析器必须重组
		write(`data: {"id":"c1","choices":[{"index":0,"delta":{"content":"B`)
		write(`"}}]}` + "\n\n")
		write("data: [DONE]\n\n")
	}))
	defer srv.Close()

	c := NewClient(srv.Client())
	res, err := c.Call(context.Background(), bizUpstream(srv.URL, "openai", "sk-up"), bizTextReq(true), nil)
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	var started, stopped bool
	for ev := range res.Stream {
		switch ev.Type {
		case "message_start":
			started = true
			if ev.MessageID != "c1" {
				t.Fatalf("message_start id = %q, want c1", ev.MessageID)
			}
		case "content_block_delta":
			if ev.Delta != nil && ev.Delta.Type == "text_delta" {
				texts = append(texts, ev.Delta.Text)
			}
		case "message_stop":
			stopped = true
		}
	}
	if !started {
		t.Fatal("message_start event lost across comment/field/empty-payload lines")
	}
	if !stopped {
		t.Fatal("message_stop lost after invalid JSON line")
	}
	if got := strings.Join(texts, ""); got != "AB" {
		t.Fatalf("text deltas = %q (raw=%v), want \"AB\" (invalid JSON must not break later events, half packet must reassemble)", got, texts)
	}
	if err := res.StreamErr(); err != nil {
		t.Fatalf("stream err = %v", err)
	}
}

// bizWaitGoroutines 等待 goroutine 数回落到 base 附近（不引入 goleak 依赖）。
func bizWaitGoroutines(t *testing.T, base int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		n := runtime.NumGoroutine()
		if n <= base+2 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutine leak: baseline=%d now=%d", base, n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// 业务规则：客户端取消 ctx 后，流式 Call 的 goroutine（streamLoop）必须尽快退出、
// 关闭下游事件通道，且不泄漏 goroutine——网关把客户端 ctx 直接串到上游调用，
// 客户端断连后上游连接不能被一直挂着。
func TestCallBiz_ContextCancelStreamNoLeak(t *testing.T) {
	hold := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		io.WriteString(w, `data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"A"}}]}`+"\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		// 一直挂住，直到客户端断开或测试结束
		select {
		case <-r.Context().Done():
		case <-hold:
		}
	}))
	defer func() {
		close(hold)
		srv.Close()
	}()

	c := NewClient(srv.Client())
	base := runtime.NumGoroutine()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	res, err := c.Call(ctx, bizUpstream(srv.URL, "openai", "sk-up"), bizTextReq(true), nil)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case _, ok := <-res.Stream:
		if !ok {
			t.Fatal("stream closed before first event")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no event arrived")
	}

	cancel()
	closed := make(chan struct{})
	go func() {
		for range res.Stream {
		}
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("stream channel not closed promptly after ctx cancel")
	}
	bizWaitGoroutines(t, base)
}

// 业务规则：非流式调用遇到客户端超时/已取消的 ctx 必须尽快返回错误（不能把调用方
// 挂死），也不泄漏 goroutine。上游 HTTP 客户端在网关里故意不设超时（SSE 需要），
// 所以调用方传入的 ctx/Client 超时是唯一的兜底。
func TestCallBiz_TimeoutAndCancelReturnPromptly(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(30 * time.Second):
		case <-r.Context().Done():
		case <-release:
			w.WriteHeader(200)
			io.WriteString(w, `{"id":"c1","model":"m","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
		}
	}))
	defer func() {
		close(release)
		srv.Close()
	}()

	t.Run("client timeout", func(t *testing.T) {
		base := runtime.NumGoroutine()
		c := NewClient(&http.Client{Timeout: 150 * time.Millisecond})
		start := time.Now()
		_, err := c.Call(context.Background(), bizUpstream(srv.URL, "openai", "sk-up"), bizTextReq(false), nil)
		if err == nil {
			t.Fatal("expected timeout error")
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Fatalf("client timeout not honored, took %v", d)
		}
		if !strings.Contains(err.Error(), "call upstream") {
			t.Fatalf("error = %v, want wrapped 'call upstream'", err)
		}
		bizWaitGoroutines(t, base)
	})

	t.Run("already cancelled ctx", func(t *testing.T) {
		base := runtime.NumGoroutine()
		c := NewClient(srv.Client())
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		start := time.Now()
		_, err := c.Call(ctx, bizUpstream(srv.URL, "openai", "sk-up"), bizTextReq(false), nil)
		if err == nil {
			t.Fatal("expected error for cancelled ctx")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled in chain", err)
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Fatalf("cancelled ctx not honored, took %v", d)
		}
		bizWaitGoroutines(t, base)
	})
}

// 业务规则：转发 inbound 头时，hop-by-hop 头（Connection/Keep-Alive/
// Transfer-Encoding/Upgrade/Te/Trailers/Proxy-*）与网关自管头（认证、
// Content-Type/Length、Host、Accept-Encoding）一律丢弃；业务头
// （anthropic-beta、trace id）透传且保留全部重复值；拷贝不与源切片共享底层数组
// （调用方改动源 Header 不能污染已构造的上游请求）。
func TestBizCopyForwardableHeaders(t *testing.T) {
	managed := []string{
		"Connection", "Keep-Alive", "Transfer-Encoding", "Upgrade", "Te", "Trailers",
		"Proxy-Authenticate", "Proxy-Authorization",
		"Authorization", "X-Api-Key", "Content-Type", "Content-Length", "Host", "Accept-Encoding",
	}
	src := http.Header{
		"Connection":          {"keep-alive"},
		"Keep-Alive":          {"timeout=5"},
		"Transfer-Encoding":   {"chunked"},
		"Upgrade":             {"websocket"},
		"Te":                  {"trailers"},
		"Trailers":            {"X-Foo"},
		"Proxy-Authenticate":  {"Basic realm=x"},
		"Proxy-Authorization": {"Basic abc"},
		"Authorization":       {"Bearer all-sk-client"},
		"X-Api-Key":           {"client-key"},
		"Content-Type":        {"text/plain"},
		"Content-Length":      {"12"},
		"Host":                {"evil.example.com"},
		"Accept-Encoding":     {"br"},
		"Anthropic-Beta":      {"tools-2024-05-16", "pdfs-2024-09-25"},
		"X-Request-Id":        {"abc-123"},
	}
	dst := http.Header{}
	copyForwardableHeaders(dst, src)

	for _, k := range managed {
		if v, ok := dst[k]; ok {
			t.Errorf("managed header %s forwarded: %v", k, v)
		}
	}
	if got := dst["Anthropic-Beta"]; len(got) != 2 || got[0] != "tools-2024-05-16" || got[1] != "pdfs-2024-09-25" {
		t.Fatalf("Anthropic-Beta = %v, want both values preserved", got)
	}
	if got := dst.Get("X-Request-Id"); got != "abc-123" {
		t.Fatalf("X-Request-Id = %q, want abc-123", got)
	}

	// 深拷贝：改动源不能影响已拷贝结果
	src["Anthropic-Beta"][0] = "mutated"
	if got := dst["Anthropic-Beta"][0]; got != "tools-2024-05-16" {
		t.Fatalf("destination aliases source slice: %q", got)
	}

	// 非规范大小写形式的受管头同样丢弃（isManagedHeader 先做 canonical）
	dst2 := http.Header{}
	copyForwardableHeaders(dst2, http.Header{"authorization": {"x"}, "connection": {"close"}, "x-api-key": {"y"}})
	if len(dst2) != 0 {
		t.Fatalf("lowercase managed headers leaked: %v", dst2)
	}
}

// 业务规则：认证与版本头的优先级——上游配置的 Authorization/x-api-key 覆盖调用方
// 自带的同名头（Set 而非 append，绝不能出现两个值）；anthropic 请求缺
// anthropic-version 时补默认值、调用方给了就沿用（不重复）；anthropic-beta 这类
// 业务头必须原样透传；连接级头不能出现在上游请求里。
func TestCallBiz_HeaderPrecedenceEndToEnd(t *testing.T) {
	clientHeaders := http.Header{
		"Authorization":     {"Bearer all-sk-client"},
		"X-Api-Key":         {"client-key"},
		"Anthropic-Version": {"2024-10-22"},
		"Anthropic-Beta":    {"tools-2024-05-16"},
		"Connection":        {"keep-alive"},
		"Keep-Alive":        {"timeout=5"},
		"Upgrade":           {"h2c"},
		"X-Request-Id":      {"trace-1"},
	}

	t.Run("openai upstream", func(t *testing.T) {
		var got http.Header
		var gotHost string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = cloneHeader(r.Header)
			gotHost = r.Host
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"id":"c1","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
		}))
		defer srv.Close()

		c := NewClient(srv.Client())
		if _, err := c.Call(context.Background(), bizUpstream(srv.URL, "openai", "sk-up"), bizTextReq(false), clientHeaders); err != nil {
			t.Fatal(err)
		}
		if v := got.Values("Authorization"); len(v) != 1 || v[0] != "Bearer sk-up" {
			t.Fatalf("Authorization = %v, want exactly [Bearer sk-up]", v)
		}
		if v := got.Values("X-Api-Key"); len(v) != 0 {
			t.Fatalf("openai upstream must not receive x-api-key, got %v", v)
		}
		if v := got.Get("Anthropic-Beta"); v != "tools-2024-05-16" {
			t.Fatalf("Anthropic-Beta = %q, want passthrough", v)
		}
		if v := got.Get("X-Request-Id"); v != "trace-1" {
			t.Fatalf("X-Request-Id = %q, want passthrough", v)
		}
		for _, k := range []string{"Connection", "Keep-Alive", "Upgrade"} {
			if v := got.Get(k); v != "" {
				t.Errorf("connection-level header %s leaked upstream: %q", k, v)
			}
		}
		// Host 永远是上游自己的主机，绝不能是客户端声称的值
		if !strings.Contains(gotHost, strings.TrimPrefix(srv.URL, "http://")) {
			t.Fatalf("upstream Host = %q, want the upstream's own host", gotHost)
		}
	})

	t.Run("anthropic upstream overrides auth and defaults version", func(t *testing.T) {
		var got http.Header
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = cloneHeader(r.Header)
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"id":"msg_1","model":"claude","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
		}))
		defer srv.Close()

		c := NewClient(srv.Client())
		u := bizUpstream(srv.URL, "anthropic", "sk-ant-up")
		if _, err := c.Call(context.Background(), u, bizTextReq(false), clientHeaders); err != nil {
			t.Fatal(err)
		}
		if v := got.Values("X-Api-Key"); len(v) != 1 || v[0] != "sk-ant-up" {
			t.Fatalf("X-Api-Key = %v, want exactly [sk-ant-up]", v)
		}
		if v := got.Values("Authorization"); len(v) != 0 {
			t.Fatalf("anthropic upstream must not receive Authorization, got %v", v)
		}
		// 调用方显式给了版本 → 沿用（单一值）
		if v := got.Values("Anthropic-Version"); len(v) != 1 || v[0] != "2024-10-22" {
			t.Fatalf("Anthropic-Version = %v, want exactly [2024-10-22]", v)
		}
		if v := got.Get("Anthropic-Beta"); v != "tools-2024-05-16" {
			t.Fatalf("Anthropic-Beta = %q, want passthrough", v)
		}

		// 调用方没给版本 → 补默认值 2023-06-01
		got = nil
		if _, err := c.Call(context.Background(), u, bizTextReq(false), http.Header{"X-Api-Key": {"all-sk-client"}}); err != nil {
			t.Fatal(err)
		}
		if v := got.Values("Anthropic-Version"); len(v) != 1 || v[0] != "2023-06-01" {
			t.Fatalf("Anthropic-Version = %v, want default [2023-06-01]", v)
		}
	})
}

// 业务规则：Anthropic 上游流式 usage 的跨事件聚合——上游 message_start 携带
// input_tokens / cache_read_input_tokens / cache_creation_input_tokens，而
// message_delta 只带 output_tokens 时，透传给下游的 message_delta 事件必须补上
// message_start 里的 input/cache 值。网关的跨格式 usage 聚合（OpenAI 出去时的
// usage chunk、Result.Usage()）完全依赖它，缺了 input_tokens 下游就只看到输出。
func TestCallBiz_AnthropicStreamUsagePropagation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		write := func(s string) {
			io.WriteString(w, s)
			if flusher != nil {
				flusher.Flush()
			}
		}
		write("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"model\":\"claude\",\"usage\":{\"input_tokens\":100,\"cache_read_input_tokens\":40,\"cache_creation_input_tokens\":7,\"output_tokens\":1}}}\n\n")
		write("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		write("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hi\"}}\n\n")
		write("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		write("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":25}}\n\n")
		write("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()

	c := NewClient(srv.Client())
	res, err := c.Call(context.Background(), bizUpstream(srv.URL, "anthropic", "sk-ant"), bizTextReq(true), nil)
	if err != nil {
		t.Fatal(err)
	}
	var sawDelta bool
	var delta *translate.StreamEvent
	for ev := range res.Stream {
		if ev.Type == "message_delta" {
			sawDelta = true
			delta = ev
		}
	}
	if !sawDelta || delta == nil {
		t.Fatal("no message_delta event forwarded")
	}
	if delta.OutputTokens != 25 {
		t.Fatalf("message_delta output_tokens = %d, want 25", delta.OutputTokens)
	}
	if delta.InputTokens != 100 {
		t.Fatalf("message_delta input_tokens = %d, want 100 propagated from message_start", delta.InputTokens)
	}
	if delta.CacheReadTokens != 40 {
		t.Fatalf("message_delta cache_read_input_tokens = %d, want 40", delta.CacheReadTokens)
	}
	if delta.CacheCreationTokens != 7 {
		t.Fatalf("message_delta cache_creation_input_tokens = %d, want 7", delta.CacheCreationTokens)
	}
	// Result.Usage() 是网关落 usage_records 的来源，必须与事件看到的一致：
	// message_delta 只带 output_tokens，input/cache 都由 message_start 回填。
	// （回归位：曾经 setUsage 先用 ev 的零值覆盖，cache_read/creation 记成 0，
	// 而客户端从事件算出来的 usage 又是对的——两边不一致。）
	usage := res.Usage()
	if usage.InputTokens != 100 || usage.OutputTokens != 25 {
		t.Fatalf("Result.Usage() = %+v, want input=100 output=25", usage)
	}
	if usage.CacheReadTokens != 40 {
		t.Errorf("Result.Usage().CacheReadTokens = %d, want 40 (usage_records.cache_read_tokens 会丢)", usage.CacheReadTokens)
	}
	if usage.CacheCreationTokens != 7 {
		t.Errorf("Result.Usage().CacheCreationTokens = %d, want 7 (usage_records.cache_creation_tokens 会丢)", usage.CacheCreationTokens)
	}
}

// 业务规则：injectStreamOptions 必须把 stream_options:{include_usage:true} 写进
// 合法 JSON 请求体（含覆盖上游/调用方已给的旧值），而 body 不是 JSON 对象（非法
// JSON、数组、空）时必须原样回退——绝不能 panic，也绝不能把 body 弄丢。
func TestBizInjectStreamOptions(t *testing.T) {
	t.Run("valid object gets include_usage", func(t *testing.T) {
		out := injectStreamOptions([]byte(`{"model":"m","stream":true,"messages":[{"role":"user","content":"hi"}],"stream_options":{"include_usage":false}}`))
		var m map[string]any
		if err := json.Unmarshal(out, &m); err != nil {
			t.Fatalf("output is not valid JSON: %v (%s)", err, out)
		}
		so, ok := m["stream_options"].(map[string]any)
		if !ok || so["include_usage"] != true {
			t.Fatalf("stream_options = %v, want {include_usage:true}", m["stream_options"])
		}
		if m["model"] != "m" || m["stream"] != true {
			t.Fatalf("other request fields were lost: %v", m)
		}
		if _, ok := m["messages"]; !ok {
			t.Fatalf("messages field lost: %v", m)
		}
	})

	t.Run("no choices still safe", func(t *testing.T) {
		body := []byte(`{"model":"m"}`)
		out := injectStreamOptions(body)
		var m map[string]any
		if err := json.Unmarshal(out, &m); err != nil {
			t.Fatalf("output is not valid JSON: %v (%s)", err, out)
		}
		if m["model"] != "m" {
			t.Fatalf("model lost: %v", m)
		}
	})

	t.Run("non-json body returned unchanged", func(t *testing.T) {
		for _, body := range []string{`not json at all`, `[1,2,3]`, ``, `"just a string"`} {
			if got := string(injectStreamOptions([]byte(body))); got != body {
				t.Errorf("body %q: got %q, want unchanged", body, got)
			}
		}
	})
}

// 业务规则：流式请求的 body 形态——openai 上游注入 stream_options.include_usage
// （AGENTS.md：Always injects stream_options into OpenAI upstream requests）；
// responses 上游刻意不注入（usage 随 response.completed 返回，见 client.go 注释）。
// 审计要求「responses 也注入」与实现/文档不符，此用例按现状钉住并记入报告。
func TestCallBiz_StreamOptionsInjectionPerFormat(t *testing.T) {
	cases := []struct {
		format     string
		sse        string
		wantInject bool
	}{
		{
			format: "openai",
			sse: "data: {\"id\":\"c1\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"A\"}}]}\n\n" +
				"data: [DONE]\n\n",
			wantInject: true,
		},
		{
			format: "responses",
			sse: "event: response.completed\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"status\":\"completed\",\"model\":\"m\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n",
			wantInject: false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.format, func(t *testing.T) {
			var rawBody []byte
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rawBody, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, tc.sse)
			}))
			defer srv.Close()

			c := NewClient(srv.Client())
			res, err := c.Call(context.Background(), bizUpstream(srv.URL, tc.format, "sk-up"), bizTextReq(true), nil)
			if err != nil {
				t.Fatal(err)
			}
			for range res.Stream { // 让 streamLoop 正常收尾，避免泄漏
			}

			var m map[string]any
			if err := json.Unmarshal(rawBody, &m); err != nil {
				t.Fatalf("upstream body is not JSON: %v (%s)", err, rawBody)
			}
			so, hasSO := m["stream_options"].(map[string]any)
			if tc.wantInject {
				if !hasSO || so["include_usage"] != true {
					t.Fatalf("%s upstream body missing include_usage: %s", tc.format, rawBody)
				}
			} else if hasSO {
				t.Fatalf("%s upstream body must not carry stream_options: %s", tc.format, rawBody)
			}
			if m["stream"] != true {
				t.Fatalf("stream flag lost in %s body: %s", tc.format, rawBody)
			}
		})
	}
}

// 业务规则：不认识的 format 必须在发请求前直接报错（不构造非法 URL、不空跑）。
func TestCallBiz_UnknownFormat(t *testing.T) {
	c := NewClient(http.DefaultClient)
	_, err := c.Call(context.Background(), bizUpstream("http://127.0.0.1:1", "gemini", "k"), bizTextReq(false), nil)
	if err == nil {
		t.Fatal("expected error for unknown format")
	}
	if !strings.Contains(err.Error(), "unknown upstream format") {
		t.Fatalf("error = %v, want unknown upstream format", err)
	}
}
