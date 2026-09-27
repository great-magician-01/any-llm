package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/great-magician-01/any-llm/internal/store"
	"github.com/great-magician-01/any-llm/internal/upstream"
)

// ---------------------------------------------------------------------------
// 本文件的 helper 统一加 sp 前缀，避免与同包既有 helper 重名。
// ---------------------------------------------------------------------------

// spSSEFrame 是响应体里的一帧：event 名（openai 出站为空）与 data 载荷。
// 以 ":" 开头的注释行（openai keep-alive）记为 Event="(comment)"。
type spSSEFrame struct {
	Event string
	Data  string
}

// spParseSSE 把 SSE 响应体切成帧序列，用于断言帧内容与顺序。
func spParseSSE(body string) []spSSEFrame {
	var out []spSSEFrame
	for _, block := range strings.Split(body, "\n\n") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		var f spSSEFrame
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "event: "):
				f.Event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				f.Data = strings.TrimPrefix(line, "data: ")
			case strings.HasPrefix(line, ":"):
				f.Event = "(comment)"
				f.Data = strings.TrimSpace(strings.TrimPrefix(line, ":"))
			}
		}
		out = append(out, f)
	}
	return out
}

// spStartTypes 返回 index -> 该 index 首次出现的 content_block_start 的块类型。
func spStartTypes(frames []spSSEFrame) map[int]string {
	out := map[int]string{}
	for _, f := range frames {
		if f.Event != "content_block_start" {
			continue
		}
		var p struct {
			Index        int `json:"index"`
			ContentBlock struct {
				Type string `json:"type"`
			} `json:"content_block"`
		}
		if err := json.Unmarshal([]byte(f.Data), &p); err != nil {
			continue
		}
		if _, seen := out[p.Index]; !seen {
			out[p.Index] = p.ContentBlock.Type
		}
	}
	return out
}

// spSSEUpstream 起一个 text/event-stream 假上游，按 frames 顺序写出并 flush。
func spSSEUpstream(t *testing.T, frames ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 读完请求体再应答：服务端若带着未读的请求体关闭连接，内核会发 RST，
		// 已写出的响应字节可能被丢弃（满载并行时表现为偶发的响应截断）。
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		for _, fr := range frames {
			w.Write([]byte(fr))
			f.Flush()
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// spFailUpstream 与既有 failUpstreamServer 同形，但先读完请求体再回错误状态，
// 避免上面那种 RST 截断把「上游 500/429」变成传输层错误（错误类型映射会变）。
func spFailUpstream(t *testing.T, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(`{"error":{"message":"boom","type":"server_error"}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// ---------------------------------------------------------------------------
// 1. keep-alive ping
// ---------------------------------------------------------------------------

// spKeepaliveCase 描述一次 keep-alive 观测：客户端入口与期望的 ping 帧形状。
type spKeepaliveCase struct {
	path     string
	body     string
	viaXAPI  bool
	pingEv   string // 期望的 ping 帧名（openai 是注释行 -> "(comment)"）
	pingBody string // ping 帧内容特征（openai 为注释文本）
}

// spFirstWriteRecorder 在网关第一次向客户端写出字节时关闭 ch。流式请求在 200
// 头 flush 之后、上游返回之前只可能写 keep-alive，所以「第一次写」= ping。
// 用信号而不是「睡 N 毫秒再看结果」来观测：测试不依赖时序余量，满载并行也不 flake。
type spFirstWriteRecorder struct {
	*httptest.ResponseRecorder
	once sync.Once
	ch   chan struct{}
}

func (w *spFirstWriteRecorder) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.ch) })
	return w.ResponseRecorder.Write(p)
}

// spAssertKeepalive 断言「上游迟迟不返回时客户端先收到 keep-alive，之后内容照常
// 到达」。假上游一直挂到测试放行；测试先等网关的第一次写（= ping），收到后才放行
// 上游——ping 与内容帧的先后顺序是确定性的，不靠 sleep 余量。
func spAssertKeepalive(t *testing.T, tc spKeepaliveCase) {
	t.Helper()
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseUpstream := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseUpstream() // 任何失败路径都要放行，否则 httptest.Server.Close 会挂住

	upstreamHit := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body) // 先读完请求体，避免 RST 截断响应
		close(upstreamHit)
		<-release
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		w.Write([]byte("data: {\"id\":\"c1\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Hi\"}}]}\n\n"))
		f.Flush()
		w.Write([]byte("data: [DONE]\n\n"))
		f.Flush()
	}))
	defer slow.Close()

	g, d := setupGateway(t)
	uid, _ := store.CreateUpstream(d, &store.Upstream{Name: "slow", BaseURL: slow.URL, APIKey: "sk", Format: "openai"})
	store.AddModel(d, uid, store.UpstreamModel{ModelName: "m"})
	k, _ := store.CreateExtKey(d, "test", "", 0, 0, nil)
	g.client = upstream.NewClient(http.DefaultClient)

	req := httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
	if tc.viaXAPI {
		req.Header.Set("x-api-key", k.Key)
	} else {
		req.Header.Set("Authorization", "Bearer "+k.Key)
	}
	w := &spFirstWriteRecorder{ResponseRecorder: httptest.NewRecorder(), ch: make(chan struct{})}
	done := make(chan struct{})
	start := time.Now()
	go func() {
		g.ServeHTTP(w, req)
		close(done)
	}()

	select {
	case <-upstreamHit:
	case <-time.After(10 * time.Second):
		t.Fatal("upstream was never called")
	}
	// 500ms ticker：只要求「最终会打 ping」，给它 10s 余量，不要求多快
	select {
	case <-w.ch:
	case <-time.After(10 * time.Second):
		t.Fatal("no keep-alive was written while the upstream was pending")
	}
	pingAt := time.Since(start)
	// ping 只能来自 ticker：不可能早于 500ms 的周期（留 100ms 计时余量）
	if pingAt < 400*time.Millisecond {
		t.Fatalf("keep-alive arrived after %v, before the 500ms ticker could fire", pingAt)
	}
	// 放行上游，让流正常收尾
	releaseUpstream()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("ServeHTTP did not return after the upstream answered")
	}
	elapsed := time.Since(start)

	if w.Code != 200 {
		t.Fatalf("status=%d want 200, body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("content-type=%q want SSE", ct)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("test took %v, want <3s", elapsed)
	}
	frames := spParseSSE(w.Body.String())
	if len(frames) == 0 {
		t.Fatal("empty stream body")
	}
	if tc.pingEv == "(comment)" {
		// openai keep-alive 是注释行，且没有独立的空行分隔（": kp\n" 直接
		// 接在下一帧前），因此按前缀断言：ping 必须是客户端收到的第一批字节。
		if !strings.HasPrefix(w.Body.String(), ": kp\n") {
			t.Fatalf("body must start with the keep-alive comment, got %q", w.Body.String())
		}
	} else {
		first := frames[0]
		if first.Event != tc.pingEv || !strings.Contains(first.Data, tc.pingBody) {
			t.Fatalf("first frame = %+v, want keep-alive %q containing %q (full body=%q)", first, tc.pingEv, tc.pingBody, w.Body.String())
		}
	}
	// ping 必须早于任何内容帧
	if tc.pingEv == "(comment)" {
		if d := strings.Index(w.Body.String(), "data: "); d < 0 {
			t.Fatalf("no content frames after keep-alive: %q", w.Body.String())
		}
	} else {
		p, d := strings.Index(w.Body.String(), "event: ping"), strings.Index(w.Body.String(), "event: content_block")
		if p < 0 || d < 0 || p > d {
			t.Fatalf("keep-alive must precede the first content frame: %q", w.Body.String())
		}
	}
	// ping 之后内容照常到达：keep-alive 只是填充，不改变正常转发
	body := w.Body.String()
	if tc.viaXAPI {
		if !strings.Contains(body, `"text":"Hi"`) || !strings.Contains(body, "event: message_stop") {
			t.Fatalf("content missing after ping: %s", body)
		}
	} else if !strings.Contains(body, `"content":"Hi"`) || !strings.Contains(body, "[DONE]") {
		t.Fatalf("content missing after ping: %s", body)
	}
}

// 业务规则（handler_openai.go 的 500ms ticker + writePing）：流式请求的 200 头
// 在上游返回之前就已经 flush，此后到首个内容帧之间的空窗必须由 keep-alive 填满
// ——否则客户端/中间代理会在首字节前超时断开（长思考的上游尤其明显）。
// anthropic 出站必须发规范的 `event: ping` 事件（SDK 认识它）。
func TestStreamKeepalivePingAnthropicOut(t *testing.T) {
	spAssertKeepalive(t, spKeepaliveCase{
		path:     "/v1/messages",
		body:     `{"model":"slow/m","max_tokens":50,"messages":[{"role":"user","content":"hi"}],"stream":true}`,
		viaXAPI:  true,
		pingEv:   "ping",
		pingBody: `"type":"ping"`,
	})
}

// 业务规则同上，openai 出站侧：keep-alive 用 `: kp` 注释行（SSE 注释被解析器
// 忽略，不污染客户端状态机），且必须是客户端收到的第一批字节。
func TestStreamKeepalivePingOpenAIOut(t *testing.T) {
	spAssertKeepalive(t, spKeepaliveCase{
		path:     "/v1/chat/completions",
		body:     `{"model":"slow/m","messages":[{"role":"user","content":"hi"}],"stream":true}`,
		pingEv:   "(comment)",
		pingBody: "kp",
	})
}

// ---------------------------------------------------------------------------
// 2. 缺失 content_block_start 的合成（handler_openai.go:439-464）
// ---------------------------------------------------------------------------

// 业务规则：Anthropic 客户端收到「未知 index 的 content_block_delta」会直接
// abort（流协议要求每个 index 先有 content_block_start）。上游格式的差异不该
// 泄漏给客户端，所以网关必须按 delta 类型补出 content_block_start：
// text_delta → text、input_json_delta → tool_use、thinking_delta/signature_delta
// → thinking，并且补出的帧必须排在同一 index 的 delta 之前。
//
// 子用例一（锚定合成分支）：anthropic 上游（如 DeepSeek 的 anthropic 兼容端点）
// 只发 delta、不发 content_block_start——IR 里就没有 start，合成逻辑是唯一来源。
func TestStreamSynthesizesMissingContentBlockStart(t *testing.T) {
	t.Run("anthropic upstream omits every block start", func(t *testing.T) {
		// 上游故意一帧 content_block_start 都不发
		srv := spSSEUpstream(t,
			"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_synth\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-x\",\"content\":[],\"usage\":{\"input_tokens\":7,\"output_tokens\":1}}}\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"hmm\"}}\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"sig-1\"}}\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hi\"}}\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":2,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"city\\\":\\\"SF\\\"}\"}}\n\n",
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":5}}\n\n",
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
		)

		g, d := setupGateway(t)
		uid, _ := store.CreateUpstream(d, &store.Upstream{Name: "ant", BaseURL: srv.URL, APIKey: "sk-ant", Format: "anthropic"})
		store.AddModel(d, uid, store.UpstreamModel{ModelName: "claude-x"})
		k, _ := store.CreateExtKey(d, "test", "", 0, 0, nil)
		g.client = upstream.NewClient(http.DefaultClient)

		req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"ant/claude-x","max_tokens":50,"messages":[{"role":"user","content":"hi"}],"stream":true}`))
		req.Header.Set("x-api-key", k.Key)
		w := httptest.NewRecorder()
		g.ServeHTTP(w, req)

		if w.Code != 200 {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		frames := spParseSSE(w.Body.String())
		// 三个块类型各补一次 start，index 顺序即上游给的 0/1/2
		types := spStartTypes(frames)
		want := map[int]string{0: "thinking", 1: "text", 2: "tool_use"}
		for idx, typ := range want {
			if types[idx] != typ {
				t.Fatalf("content_block_start[%d] type=%q want %q (starts=%v body=%s)", idx, types[idx], typ, types, w.Body.String())
			}
		}
		if len(types) != 3 {
			t.Fatalf("content_block_start count=%d want 3 (starts=%v body=%s)", len(types), types, w.Body.String())
		}
		// 每个 delta 的 index 必须已经有 start：合成帧要排在 delta 之前
		started := map[int]bool{}
		for _, f := range frames {
			switch f.Event {
			case "content_block_start":
				var p struct {
					Index int `json:"index"`
				}
				if err := json.Unmarshal([]byte(f.Data), &p); err == nil {
					started[p.Index] = true
				}
			case "content_block_delta":
				var p struct {
					Index int `json:"index"`
					Delta struct {
						Type string `json:"type"`
					} `json:"delta"`
				}
				if err := json.Unmarshal([]byte(f.Data), &p); err != nil {
					t.Fatalf("bad delta frame %q: %v", f.Data, err)
				}
				if !started[p.Index] {
					t.Fatalf("delta %q arrived for index %d before its content_block_start (body=%s)", p.Delta.Type, p.Index, w.Body.String())
				}
			}
		}
		// tool_use 块在 start 里没有 id/name 可填（上游没说），但 delta 的
		// partial_json 必须原样转发，不能被吞掉。
		if !strings.Contains(w.Body.String(), `"partial_json":"{\"city\":\"SF\"}"`) {
			t.Fatalf("tool input delta lost: %s", w.Body.String())
		}
	})

	// 子用例二（父任务点名的场景）：OpenAI 格式上游只发 content delta。
	// OpenAI 流解码器自己会补 start，所以这里断言的是「客户端可见的规则」：
	// 不论 start 由解码器还是网关合成，anthropic 出站都不能出现裸 delta。
	t.Run("openai upstream only sends deltas", func(t *testing.T) {
		srv := spSSEUpstream(t,
			`data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{"reasoning_content":"think"}}]}`+"\n\n",
			`data: {"id":"c1","choices":[{"index":0,"delta":{"content":"Hi"}}]}`+"\n\n",
			`data: {"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":""}}]}}]}`+"\n\n",
			`data: {"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":\"SF\"}"}}]}}]}`+"\n\n",
			`data: {"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`+"\n\n",
			"data: [DONE]\n\n",
		)

		g, d := setupGateway(t)
		uid, _ := store.CreateUpstream(d, &store.Upstream{Name: "oai", BaseURL: srv.URL, APIKey: "sk", Format: "openai"})
		store.AddModel(d, uid, store.UpstreamModel{ModelName: "m"})
		k, _ := store.CreateExtKey(d, "test", "", 0, 0, nil)
		g.client = upstream.NewClient(http.DefaultClient)

		req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"oai/m","max_tokens":50,"messages":[{"role":"user","content":"hi"}],"stream":true}`))
		req.Header.Set("x-api-key", k.Key)
		w := httptest.NewRecorder()
		g.ServeHTTP(w, req)

		if w.Code != 200 {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		frames := spParseSSE(w.Body.String())
		types := spStartTypes(frames)
		if len(types) != 3 {
			t.Fatalf("want a start for thinking+text+tool_use, starts=%v body=%s", types, w.Body.String())
		}
		got := map[string]bool{}
		for _, typ := range types {
			got[typ] = true
		}
		if !got["thinking"] || !got["text"] || !got["tool_use"] {
			t.Fatalf("block types=%v want thinking+text+tool_use (body=%s)", got, w.Body.String())
		}
		started := map[int]bool{}
		for _, f := range frames {
			var p struct {
				Index int `json:"index"`
			}
			if err := json.Unmarshal([]byte(f.Data), &p); err != nil {
				continue
			}
			switch f.Event {
			case "content_block_start":
				started[p.Index] = true
			case "content_block_delta":
				if !started[p.Index] {
					t.Fatalf("delta for index %d without a preceding start (body=%s)", p.Index, w.Body.String())
				}
			}
		}
		// 工具块在 start 时必须带 id/name，否则工具调用无法还原
		if !strings.Contains(w.Body.String(), `"id":"call_1"`) || !strings.Contains(w.Body.String(), `"name":"get_weather"`) {
			t.Fatalf("tool_use metadata lost: %s", w.Body.String())
		}
	})
}

// ---------------------------------------------------------------------------
// 3. 全部候选失败：带内错误帧（handler_openai.go:330-364）
// ---------------------------------------------------------------------------

// 业务规则：流式请求的 200 头已经 flush，此后上游全部候选都失败时不可能再回
// HTTP 错误码，必须写一个「客户端格式」的带内错误帧并结束——绝不能是 200 +
// 空流（SDK 会把它当成「上游正常返回了空内容」，既不重试也不报错）。
// openai 出站：data: {"error":{"message","type"}}；anthropic/responses 出站：
// event: error + {"type":"error","error":{...}}。
// 每个候选各记一条 usage，status=error，stream=true。
func TestStreamAllCandidatesFailWritesInbandErrorFrame(t *testing.T) {
	cases := []struct {
		name        string
		path        string
		body        string
		viaXAPI     bool
		event       string // 期望帧的 event 名（openai 为空，走 data:）
		wantErrType string
	}{
		{
			name:        "openai client",
			path:        "/v1/chat/completions",
			body:        `{"model":"fixed","messages":[{"role":"user","content":"hi"}],"stream":true}`,
			wantErrType: `"type":"rate_limit_error"`,
		},
		{
			name:        "anthropic client",
			path:        "/v1/messages",
			body:        `{"model":"fixed","max_tokens":50,"messages":[{"role":"user","content":"hi"}],"stream":true}`,
			viaXAPI:     true,
			event:       "error",
			wantErrType: `"type":"rate_limit_error"`,
		},
		{
			name:        "responses client",
			path:        "/v1/responses",
			body:        `{"model":"fixed","stream":true,"input":[{"role":"user","content":[{"type":"input_text","text":"hi"}]}]}`,
			event:       "error",
			wantErrType: `"type":"rate_limit_error"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 首选 500、次选 429：客户端拿到的必须是「最后一个候选」的错误映射
			bad1 := spFailUpstream(t, 500)
			bad2 := spFailUpstream(t, 429)
			g, k := setupAliasGateway(t)
			d := g.db
			uid1, _ := store.CreateUpstream(d, &store.Upstream{Name: "bad1", BaseURL: bad1.URL, APIKey: "sk", Format: "openai"})
			uid2, _ := store.CreateUpstream(d, &store.Upstream{Name: "bad2", BaseURL: bad2.URL, APIKey: "sk", Format: "openai"})
			store.CreateAlias(d, &store.ModelAlias{Name: "fixed", Bindings: []store.AliasBinding{
				{UpstreamID: uid1, ModelName: "m1"},
				{UpstreamID: uid2, ModelName: "m2"},
			}})

			req := httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
			if tc.viaXAPI {
				req.Header.Set("x-api-key", k.Key)
			} else {
				req.Header.Set("Authorization", "Bearer "+k.Key)
			}
			w := httptest.NewRecorder()
			g.ServeHTTP(w, req)

			// 头部已 flush：状态码只能是 200，错误只能带内
			if w.Code != 200 {
				t.Fatalf("status=%d want 200 (headers already flushed), body=%s", w.Code, w.Body.String())
			}
			if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
				t.Fatalf("content-type=%q want SSE", ct)
			}
			body := w.Body.String()
			if strings.TrimSpace(body) == "" {
				t.Fatal("empty 200 stream: all candidates failed but client got no error frame")
			}
			frames := spParseSSE(body)
			if len(frames) != 1 {
				t.Fatalf("want exactly one in-band error frame, got %d: %q", len(frames), body)
			}
			f := frames[0]
			if f.Event != tc.event {
				t.Fatalf("frame event=%q want %q (body=%q)", f.Event, tc.event, body)
			}
			if !strings.Contains(f.Data, `"error"`) || !strings.Contains(f.Data, `"boom"`) {
				t.Fatalf("error payload missing message: %q", f.Data)
			}
			if !strings.Contains(f.Data, tc.wantErrType) {
				t.Fatalf("error payload type mismatch: %q want %s", f.Data, tc.wantErrType)
			}
			// 失败的流不能以结束帧收尾（否则客户端以为上游正常收完）
			if strings.Contains(body, "[DONE]") || strings.Contains(body, "message_stop") || strings.Contains(body, "response.completed") {
				t.Fatalf("failed stream must not carry a completion frame: %q", body)
			}

			records, total, _ := store.UsageRecordsList(d, 1, 10)
			if total != 2 {
				t.Fatalf("usage records=%d want 2 (one per attempted candidate)", total)
			}
			seen := map[string]bool{}
			for _, r := range records {
				if r.Status != "error" || !r.Stream {
					t.Fatalf("record=%+v want status=error stream=true", r)
				}
				seen[r.UpstreamName] = true
			}
			if !seen["bad1"] || !seen["bad2"] {
				t.Fatalf("usage records must cover both candidates: %+v", records)
			}
		})
	}
}

// 业务规则（本次为它补的生产修复）：上游在流中间发 error 事件（Responses 的
// response.failed / Anthropic 的 event: error）时，IR 事件是 {Type:"error"}，
// 而三个出站编码器都没有 error 分支：openai / responses 出站会静默丢弃它，
// anthropic 出站只会发出一个没有 error 明细的空壳 `{"type":"error"}` 帧——两种
// 情况客户端都拿不到可用的错误信息与结束信号，usage 还被记成 ok。网关必须在流
// 循环里拦下它：写客户端格式的带内错误帧、结束本请求、usage 记 error。
func TestStreamMidStreamUpstreamErrorIsNotSwallowed(t *testing.T) {
	cases := []struct {
		name    string
		path    string
		body    string
		viaXAPI bool
		event   string
	}{
		{
			name:  "openai client",
			path:  "/v1/chat/completions",
			body:  `{"model":"rsp/m","messages":[{"role":"user","content":"hi"}],"stream":true}`,
			event: "",
		},
		{
			name:    "anthropic client",
			path:    "/v1/messages",
			body:    `{"model":"rsp/m","max_tokens":50,"messages":[{"role":"user","content":"hi"}],"stream":true}`,
			viaXAPI: true,
			event:   "error",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// responses 上游：先建立流，再在中间报 response.failed
			srv := spSSEUpstream(t,
				"event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\",\"model\":\"m\",\"status\":\"in_progress\"}}\n\n",
				"event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_1\",\"status\":\"failed\",\"error\":{\"code\":\"server_error\",\"message\":\"upstream overloaded\"}}}\n\n",
			)

			g, d := setupGateway(t)
			uid, _ := store.CreateUpstream(d, &store.Upstream{Name: "rsp", BaseURL: srv.URL, APIKey: "sk", Format: "responses"})
			store.AddModel(d, uid, store.UpstreamModel{ModelName: "m"})
			k, _ := store.CreateExtKey(d, "test", "", 0, 0, nil)
			g.client = upstream.NewClient(http.DefaultClient)

			req := httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
			if tc.viaXAPI {
				req.Header.Set("x-api-key", k.Key)
			} else {
				req.Header.Set("Authorization", "Bearer "+k.Key)
			}
			w := httptest.NewRecorder()
			g.ServeHTTP(w, req)

			if w.Code != 200 {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			frames := spParseSSE(w.Body.String())
			if len(frames) == 0 {
				t.Fatalf("empty stream: upstream error event was dropped: %q", w.Body.String())
			}
			last := frames[len(frames)-1]
			if last.Event != tc.event {
				t.Fatalf("last frame event=%q want %q (frames=%+v)", last.Event, tc.event, frames)
			}
			if !strings.Contains(last.Data, `"error"`) || !strings.Contains(last.Data, `"type":"upstream_error"`) {
				t.Fatalf("terminal error frame payload=%q want error type upstream_error", last.Data)
			}
			// 出错后不得再补结束帧（不能把一次失败的流伪装成正常完成）
			if strings.Contains(w.Body.String(), "[DONE]") || strings.Contains(w.Body.String(), "message_stop") || strings.Contains(w.Body.String(), "response.completed") {
				t.Fatalf("error stream must not be closed with a completion frame: %q", w.Body.String())
			}

			records, total, _ := store.UsageRecordsList(d, 1, 10)
			if total != 1 {
				t.Fatalf("usage records=%d want 1", total)
			}
			if records[0].Status != "error" || !records[0].Stream {
				t.Fatalf("record=%+v want status=error stream=true (mid-stream upstream error must not be recorded as ok)", records[0])
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 6. 已出数据后不回退
// ---------------------------------------------------------------------------

// 业务规则（AGENTS.md「once stream events flow, no failover」）：候选一旦把内容
// 帧发给客户端就没有回头路——重放第二候选会把两家上游的内容混在一起（客户端
// 看到重复/错乱的内容），所以此时第一候选中途断开只能以错误结束本流。
func TestStreamNoFailoverAfterContentFramesFlowed(t *testing.T) {
	// 假上游：写一帧内容后以「声明了 Content-Length 却没写完」的方式断开，
	// 让解码器读到 unexpected EOF（= 内容已出、上游随后失败）。
	flaky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body) // 先读完请求体再劫持，避免 RST 把已写出的内容丢掉
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
		fmt.Fprintf(buf, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nContent-Length: 100000\r\n\r\n")
		fmt.Fprintf(buf, "data: {\"id\":\"c1\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"partial-from-first\"}}]}\n\n")
		buf.Flush()
	}))
	defer flaky.Close()

	var secondCalls atomic.Int32
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		secondCalls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		w.Write([]byte("data: {\"id\":\"c2\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"from-second\"}}]}\n\n"))
		f.Flush()
		w.Write([]byte("data: [DONE]\n\n"))
		f.Flush()
	}))
	defer second.Close()

	g, k := setupAliasGateway(t)
	d := g.db
	uid1, _ := store.CreateUpstream(d, &store.Upstream{Name: "flaky", BaseURL: flaky.URL, APIKey: "sk", Format: "openai"})
	uid2, _ := store.CreateUpstream(d, &store.Upstream{Name: "second", BaseURL: second.URL, APIKey: "sk", Format: "openai"})
	store.CreateAlias(d, &store.ModelAlias{Name: "fixed", Bindings: []store.AliasBinding{
		{UpstreamID: uid1, ModelName: "m1"},
		{UpstreamID: uid2, ModelName: "m2"},
	}})

	w := aliasRequest(t, g, k.Key, `{"model":"fixed","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	body := w.Body.String()
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, body)
	}
	if !strings.Contains(body, "partial-from-first") {
		t.Fatalf("first candidate's content frames missing: %q", body)
	}
	if strings.Contains(body, "from-second") {
		t.Fatalf("second candidate's content replayed after data flowed: %q", body)
	}
	if secondCalls.Load() != 0 {
		t.Fatalf("second candidate was called %d times after content flowed, want 0", secondCalls.Load())
	}
	if strings.Contains(body, "[DONE]") {
		t.Fatalf("stream that ended in upstream failure must not emit [DONE]: %q", body)
	}
	records, total, _ := store.UsageRecordsList(d, 1, 10)
	if total != 1 {
		t.Fatalf("usage records=%d want 1 (only the candidate that actually ran)", total)
	}
	if records[0].UpstreamName != "flaky" || records[0].Model != "m1" || records[0].Status != "error" || !records[0].Stream {
		t.Fatalf("record=%+v want flaky/m1 status=error stream=true", records[0])
	}
}

// ---------------------------------------------------------------------------
// 7. 过期别名候选跳过 / 全过期 404
// ---------------------------------------------------------------------------

// 业务规则（store.Upstream.Expired，读时判定）：过期上游与禁用同等对待但
// enabled 位不动——别名候选链里过期的候选在解析阶段就被剔除（不发上游调用、
// 不记 usage），故障转移到下一个候选；到期只是「暂时不服务」，续期即恢复，
// 所以绝不能顺手把 enabled 改成 false。
func TestAliasSkipsExpiredCandidateAndServesNext(t *testing.T) {
	good := spSSEUpstream(t,
		"data: {\"id\":\"c2\",\"model\":\"m2\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"from-second\"}}]}\n\n",
		"data: [DONE]\n\n",
	)

	g, k := setupAliasGateway(t)
	d := g.db
	// 首选绑定指向一个已过期上游（base_url 故意不可达：真被调用就会失败）
	uidOld, _ := store.CreateUpstream(d, &store.Upstream{Name: "old", BaseURL: "http://127.0.0.1:1", APIKey: "sk", Format: "openai"})
	uidGood, _ := store.CreateUpstream(d, &store.Upstream{Name: "good", BaseURL: good.URL, APIKey: "sk", Format: "openai"})
	store.CreateAlias(d, &store.ModelAlias{Name: "fixed", Bindings: []store.AliasBinding{
		{UpstreamID: uidOld, ModelName: "m1"},
		{UpstreamID: uidGood, ModelName: "m2"},
	}})
	u, _ := store.GetUpstreamByID(d, uidOld)
	at := time.Now().Add(-time.Hour)
	u.ExpiresAt = &at
	if err := store.UpdateUpstream(d, u); err != nil {
		t.Fatal(err)
	}

	w := aliasRequest(t, g, k.Key, `{"model":"fixed","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "from-second") {
		t.Fatalf("status=%d body=%s (expired candidate must fall through to the next binding)", w.Code, w.Body.String())
	}
	// 过期候选从未被调用：只有第二候选的一条 usage
	records, total, _ := store.UsageRecordsList(d, 1, 10)
	if total != 1 {
		t.Fatalf("usage records=%d want 1 (expired candidate never dispatched)", total)
	}
	if records[0].UpstreamName != "good" || records[0].Model != "m2" || records[0].Status != "ok" {
		t.Fatalf("record=%+v want good/m2 ok", records[0])
	}
	// 到期不改 enabled：续期即可恢复服务
	after, _ := store.GetUpstreamByID(d, uidOld)
	if !after.Enabled {
		t.Fatal("expiry must not flip enabled=false")
	}
}

// 业务规则：别名的绑定全部过期 → 与全部禁用/删除一致，404「has no available
// bindings」（而不是 500、也不是回落到直连路由），且不产生任何 usage。
func TestAliasAllCandidatesExpired404(t *testing.T) {
	g, k := setupAliasGateway(t)
	d := g.db
	uid1, _ := store.CreateUpstream(d, &store.Upstream{Name: "old1", BaseURL: "http://127.0.0.1:1", APIKey: "sk", Format: "openai"})
	uid2, _ := store.CreateUpstream(d, &store.Upstream{Name: "old2", BaseURL: "http://127.0.0.1:1", APIKey: "sk", Format: "openai"})
	store.CreateAlias(d, &store.ModelAlias{Name: "fixed", Bindings: []store.AliasBinding{
		{UpstreamID: uid1, ModelName: "m1"},
		{UpstreamID: uid2, ModelName: "m2"},
	}})
	at := time.Now().Add(-time.Hour)
	for _, uid := range []int64{uid1, uid2} {
		u, _ := store.GetUpstreamByID(d, uid)
		u.ExpiresAt = &at
		if err := store.UpdateUpstream(d, u); err != nil {
			t.Fatal(err)
		}
	}

	w := aliasRequest(t, g, k.Key, `{"model":"fixed","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	if w.Code != 404 {
		t.Fatalf("status=%d want 404, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "no available bindings") {
		t.Fatalf("body=%s want 'no available bindings'", w.Body.String())
	}
	if _, total, _ := store.UsageRecordsList(d, 1, 10); total != 0 {
		t.Fatalf("usage records=%d want 0 (no candidate was dispatched)", total)
	}
}
