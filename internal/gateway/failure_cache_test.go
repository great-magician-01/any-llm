package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/great-magician-01/any-llm/internal/store"
	"github.com/great-magician-01/any-llm/internal/upstream"
)

// countingFailServer 返回一个恒以给定状态码报错、并记录被命中次数的假上游，
// 用来断言「冷却期内的请求没有穿透到上游」。
func countingFailServer(t *testing.T, status int) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(`{"error":{"message":"boom","type":"server_error"}}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// okStreamServer 返回一个固定应答单帧 SSE 的 OpenAI 流式假上游。
func okStreamServer(t *testing.T, text string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		w.Write([]byte("data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"" + text + "\"}}]}\n\n"))
		f.Flush()
		w.Write([]byte("data: [DONE]\n\n"))
		f.Flush()
	}))
	t.Cleanup(srv.Close)
	return srv
}

// failureCacheable 的可缓存口径：传输错误、5xx、401/402/403/429 进冷却；
// 其余 4xx 是请求级错误，不缓存。
func TestFailureCacheable(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"transport error", errors.New("call upstream: connection refused"), true},
		{"client canceled", fmt.Errorf("call upstream: %w", context.Canceled), false},
		{"deadline exceeded", fmt.Errorf("call upstream: %w", context.DeadlineExceeded), true},
		{"500", &upstream.UpstreamError{StatusCode: 500}, true},
		{"503", &upstream.UpstreamError{StatusCode: 503}, true},
		{"429", &upstream.UpstreamError{StatusCode: 429}, true},
		{"401", &upstream.UpstreamError{StatusCode: 401}, true},
		{"402", &upstream.UpstreamError{StatusCode: 402}, true},
		{"403", &upstream.UpstreamError{StatusCode: 403}, true},
		{"400", &upstream.UpstreamError{StatusCode: 400}, false},
		{"404", &upstream.UpstreamError{StatusCode: 404}, false},
		{"422", &upstream.UpstreamError{StatusCode: 422}, false},
	} {
		if got := failureCacheable(tc.err); got != tc.want {
			t.Errorf("%s: failureCacheable=%v want %v", tc.name, got, tc.want)
		}
	}
}

// 冷却语义：mark 后 TTL 内 down=true，到期自动失效（条目随之清除），
// clear 立即摘除；不可缓存的失败 mark 是空操作；末尾候选（无后继）不记录。
func TestFailureCacheMarkDownClear(t *testing.T) {
	c := newFailureCache()
	target := &store.AliasTarget{Upstream: &store.Upstream{ID: 1, Name: "u"}, ModelName: "m"}
	t0 := time.Now()

	c.mark(target, errors.New("call upstream: timeout"), t0, true)
	if !c.down(target, t0.Add(time.Second)) {
		t.Fatal("marked candidate should be down inside TTL")
	}
	if c.down(target, t0.Add(failureCacheTTL)) {
		t.Fatal("candidate should leave cooldown at TTL boundary")
	}
	if len(c.until) != 0 {
		t.Fatalf("expired entry should be evicted, got %d entries", len(c.until))
	}

	c.mark(target, errors.New("boom"), t0, true)
	c.clear(target)
	if c.down(target, t0.Add(time.Second)) {
		t.Fatal("cleared candidate should not be down")
	}

	c.mark(target, &upstream.UpstreamError{StatusCode: 400}, t0, true)
	if c.down(target, t0.Add(time.Second)) {
		t.Fatal("request-level error (400) must not enter cooldown")
	}

	// 末尾候选（hasSuccessor=false）不记录：它的冷却从不被查询。
	c.mark(target, errors.New("call upstream: timeout"), t0, false)
	if len(c.until) != 0 {
		t.Fatalf("tail candidate must not be marked, got %d entries", len(c.until))
	}
}

// 非流式：首选候选 500 失败后进入冷却，后续请求直接路由第二候选，
// 不再穿透到故障上游；冷却跳过不记 usage。
func TestFailureCacheSkipsFailedCandidateNonStream(t *testing.T) {
	bad, badHits := countingFailServer(t, 500)
	good := okUpstreamServer(t, "from-second")
	g, k := setupAliasGateway(t)
	d := g.db
	uid1, _ := store.CreateUpstream(d, &store.Upstream{Name: "bad", BaseURL: bad.URL, APIKey: "sk", Format: "openai"})
	uid2, _ := store.CreateUpstream(d, &store.Upstream{Name: "good", BaseURL: good.URL, APIKey: "sk", Format: "openai"})
	store.CreateAlias(d, &store.ModelAlias{Name: "fixed", Bindings: []store.AliasBinding{
		{UpstreamID: uid1, ModelName: "m1"},
		{UpstreamID: uid2, ModelName: "m2"},
	}})

	body := `{"model":"fixed","messages":[{"role":"user","content":"hi"}]}`
	if w := aliasRequest(t, g, k.Key, body); w.Code != 200 {
		t.Fatalf("first request status=%d body=%s", w.Code, w.Body.String())
	}
	if badHits.Load() != 1 {
		t.Fatalf("bad upstream hits=%d want 1", badHits.Load())
	}

	w := aliasRequest(t, g, k.Key, body)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "from-second") {
		t.Fatalf("second request status=%d body=%s", w.Code, w.Body.String())
	}
	if badHits.Load() != 1 {
		t.Fatalf("cooling candidate was hit again: hits=%d want 1", badHits.Load())
	}
	// 第一次 error+ok 两条；第二次冷却跳过不记，仅 ok 一条
	_, total, _ := store.UsageRecordsList(d, 1, 10, "")
	if total != 3 {
		t.Fatalf("usage records=%d want 3 (error+ok, then ok only)", total)
	}
}

// 单候选直连不应用故障冷却：每次请求都实际调用上游，并透传原始上游错误，
// 不能在第二次请求时改回合成的 503 cooldown。
func TestFailureCacheDirectNonStreamReturnsUpstreamError(t *testing.T) {
	bad, badHits := countingFailServer(t, 500)
	g, k := setupAliasGateway(t)
	d := g.db
	uid, _ := store.CreateUpstream(d, &store.Upstream{Name: "bad", BaseURL: bad.URL, APIKey: "sk", Format: "openai"})
	store.AddModel(d, uid, store.UpstreamModel{ModelName: "m"})

	body := `{"model":"bad/m","messages":[{"role":"user","content":"hi"}]}`
	for i := 1; i <= 2; i++ {
		w := aliasRequest(t, g, k.Key, body)
		if w.Code != 500 || !strings.Contains(w.Body.String(), "boom") || strings.Contains(w.Body.String(), "failure cooldown") {
			t.Fatalf("request %d status=%d want original 500 upstream error, body=%s", i, w.Code, w.Body.String())
		}
	}
	if badHits.Load() != 2 {
		t.Fatalf("direct upstream hits=%d want 2 (cooldown must not suppress the only call)", badHits.Load())
	}
}

// 请求级错误（400）不进冷却：第二次请求仍先试首选候选（命中数增加），
// 再故障转移到第二候选。
func TestFailureCacheIgnoresRequestLevelErrors(t *testing.T) {
	bad, badHits := countingFailServer(t, 400)
	good := okUpstreamServer(t, "from-second")
	g, k := setupAliasGateway(t)
	d := g.db
	uid1, _ := store.CreateUpstream(d, &store.Upstream{Name: "bad", BaseURL: bad.URL, APIKey: "sk", Format: "openai"})
	uid2, _ := store.CreateUpstream(d, &store.Upstream{Name: "good", BaseURL: good.URL, APIKey: "sk", Format: "openai"})
	store.CreateAlias(d, &store.ModelAlias{Name: "fixed", Bindings: []store.AliasBinding{
		{UpstreamID: uid1, ModelName: "m1"},
		{UpstreamID: uid2, ModelName: "m2"},
	}})

	body := `{"model":"fixed","messages":[{"role":"user","content":"hi"}]}`
	for i := 1; i <= 2; i++ {
		w := aliasRequest(t, g, k.Key, body)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "from-second") {
			t.Fatalf("request %d status=%d body=%s", i, w.Code, w.Body.String())
		}
	}
	if badHits.Load() != 2 {
		t.Fatalf("request-level failure must not be cached: bad hits=%d want 2", badHits.Load())
	}
}

// 流式：首选候选失败后进入冷却，后续流式请求的候选预占直接跳过它，
// 客户端拿到的仍是第二候选的正常流。
func TestFailureCacheSkipsFailedCandidateStream(t *testing.T) {
	bad, badHits := countingFailServer(t, 500)
	good := okStreamServer(t, "Hi")
	g, k := setupAliasGateway(t)
	d := g.db
	uid1, _ := store.CreateUpstream(d, &store.Upstream{Name: "bad", BaseURL: bad.URL, APIKey: "sk", Format: "openai"})
	uid2, _ := store.CreateUpstream(d, &store.Upstream{Name: "good", BaseURL: good.URL, APIKey: "sk", Format: "openai"})
	store.CreateAlias(d, &store.ModelAlias{Name: "fixed", Bindings: []store.AliasBinding{
		{UpstreamID: uid1, ModelName: "m1"},
		{UpstreamID: uid2, ModelName: "m2"},
	}})

	body := `{"model":"fixed","messages":[{"role":"user","content":"hi"}],"stream":true}`
	for i := 1; i <= 2; i++ {
		w := aliasRequest(t, g, k.Key, body)
		if w.Code != 200 {
			t.Fatalf("request %d status=%d body=%s", i, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), `"content":"Hi"`) {
			t.Fatalf("request %d stream body=%s", i, w.Body.String())
		}
	}
	if badHits.Load() != 1 {
		t.Fatalf("cooling candidate was hit again: hits=%d want 1", badHits.Load())
	}
}

// 单候选直连流式请求同样不应用冷却：每次都在 200 头部后返回真实上游的
// 带内错误，不能在后续请求的头部 flush 前改回合成的 503 cooldown。
func TestFailureCacheDirectStreamReturnsUpstreamError(t *testing.T) {
	bad, badHits := countingFailServer(t, 500)
	g, k := setupAliasGateway(t)
	d := g.db
	uid, _ := store.CreateUpstream(d, &store.Upstream{Name: "bad", BaseURL: bad.URL, APIKey: "sk", Format: "openai"})
	store.AddModel(d, uid, store.UpstreamModel{ModelName: "m"})

	body := `{"model":"bad/m","messages":[{"role":"user","content":"hi"}],"stream":true}`
	for i := 1; i <= 2; i++ {
		w := aliasRequest(t, g, k.Key, body)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"error"`) || !strings.Contains(w.Body.String(), "boom") || strings.Contains(w.Body.String(), "failure cooldown") {
			t.Fatalf("request %d should contain the original in-band upstream error: status=%d body=%s", i, w.Code, w.Body.String())
		}
	}
	if badHits.Load() != 2 {
		t.Fatalf("direct upstream hits=%d want 2 (cooldown must not suppress the only call)", badHits.Load())
	}
}

// 别名链全部失败的流式请求：前面的候选可在后续请求中冷却跳过，但末尾候选
// 始终实际调用，客户端拿到的仍是它的原始错误类型与报文。
func TestFailureCacheAliasStreamReturnsLastUpstreamError(t *testing.T) {
	bad1, bad1Hits := countingFailServer(t, 500)
	bad2, bad2Hits := countingFailServer(t, 429)
	g, k := setupAliasGateway(t)
	d := g.db
	uid1, _ := store.CreateUpstream(d, &store.Upstream{Name: "bad1", BaseURL: bad1.URL, APIKey: "sk", Format: "openai"})
	uid2, _ := store.CreateUpstream(d, &store.Upstream{Name: "bad2", BaseURL: bad2.URL, APIKey: "sk", Format: "openai"})
	store.CreateAlias(d, &store.ModelAlias{Name: "fixed", Bindings: []store.AliasBinding{
		{UpstreamID: uid1, ModelName: "m1"},
		{UpstreamID: uid2, ModelName: "m2"},
	}})

	body := `{"model":"fixed","messages":[{"role":"user","content":"hi"}],"stream":true}`
	for i := 1; i <= 2; i++ {
		w := aliasRequest(t, g, k.Key, body)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "rate_limit_error") || !strings.Contains(w.Body.String(), "boom") || strings.Contains(w.Body.String(), "failure cooldown") {
			t.Fatalf("request %d should contain last candidate's original in-band 429: status=%d body=%s", i, w.Code, w.Body.String())
		}
	}
	if bad1Hits.Load() != 1 || bad2Hits.Load() != 2 {
		t.Fatalf("upstream hits=(%d, %d), want (1, 2): first cools down, last is always tried", bad1Hits.Load(), bad2Hits.Load())
	}
}

// 非流式客户端主动终止：随断开冒出的 context.Canceled 不把候选打进冷却，
// 也不再对余下候选做无效转移（上下文已取消，调用只会立即失败、逐个记档）。
// 与流式 clientGone（callWithKeepalive）口径一致；修的是一次断开会把整条
// 候选链关进 60s 冷却的回归。
func TestNonStreamClientGoneNoCooldown(t *testing.T) {
	upstreamHit := make(chan struct{})
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 先读完请求体：net/http 只在读完 body 后才开始后台读连接，
		// 否则客户端断开时服务端的 r.Context() 不会被取消（测试自身的坑）。
		io.Copy(io.Discard, r.Body)
		if hits.Add(1) == 1 {
			close(upstreamHit)
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"c1","model":"m1","choices":[{"index":0,"message":{"role":"assistant","content":"recovered"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	t.Cleanup(srv.Close)

	g, k := setupAliasGateway(t)
	d := g.db
	uid1, _ := store.CreateUpstream(d, &store.Upstream{Name: "slow", BaseURL: srv.URL, APIKey: "sk", Format: "openai"})
	uid2, _ := store.CreateUpstream(d, &store.Upstream{Name: "other", BaseURL: srv.URL, APIKey: "sk", Format: "openai"})
	store.CreateAlias(d, &store.ModelAlias{Name: "fixed", Bindings: []store.AliasBinding{
		{UpstreamID: uid1, ModelName: "m1"},
		{UpstreamID: uid2, ModelName: "m2"},
	}})

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"fixed","messages":[{"role":"user","content":"hi"}]}`)).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+k.Key)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		g.ServeHTTP(w, req)
		close(done)
	}()
	select {
	case <-upstreamHit:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("upstream was never called")
	}
	cancel() // 客户端主动终止
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ServeHTTP did not return after the client disconnected")
	}

	// 回归点：冷却缓存必须为空——断开不是候选故障
	if len(g.fail.until) != 0 {
		t.Fatalf("failure cache should be empty after client abort, got %d entries", len(g.fail.until))
	}
	// 只记了实际尝试的那条档案；第二候选未被转移（上下文已取消，调用只会立即失败）
	records, total, _ := store.UsageRecordsList(d, 1, 10, "")
	if total != 1 || records[0].Status != "error" || records[0].Model != "m1" || records[0].Stream {
		t.Fatalf("usage records=%+v want exactly 1 non-stream error row for m1", records)
	}
	// 后续请求照常先试首选候选并成功——冷却没有把链路关掉
	w2 := aliasRequest(t, g, k.Key, `{"model":"fixed","messages":[{"role":"user","content":"hi"}]}`)
	if w2.Code != 200 || !strings.Contains(w2.Body.String(), "recovered") {
		t.Fatalf("follow-up request status=%d body=%s (first candidate wrongly cooling?)", w2.Code, w2.Body.String())
	}
	if hits.Load() != 2 {
		t.Fatalf("upstream hits=%d want 2", hits.Load())
	}
}

// 尾候选（含单候选直连）失败不打冷却：末尾候选的冷却从不被 down() 查询，
// 记了也只会沉淀成永不过期的死条目（直连 model 名来自客户端输入，map 会
// 无界增长）和无人响应的 WARN。
func TestFailureCacheTailNotMarked(t *testing.T) {
	bad, hits := countingFailServer(t, 500)
	g, k := setupAliasGateway(t)
	d := g.db
	uid, _ := store.CreateUpstream(d, &store.Upstream{Name: "bad", BaseURL: bad.URL, APIKey: "sk", Format: "openai"})
	store.AddModel(d, uid, store.UpstreamModel{ModelName: "m"})

	body := `{"model":"bad/m","messages":[{"role":"user","content":"hi"}]}`
	for i := 1; i <= 2; i++ {
		if w := aliasRequest(t, g, k.Key, body); w.Code != 500 {
			t.Fatalf("request %d status=%d want 500", i, w.Code)
		}
	}
	if hits.Load() != 2 {
		t.Fatalf("upstream hits=%d want 2", hits.Load())
	}
	if len(g.fail.until) != 0 {
		t.Fatalf("failure cache entries=%d want 0: tail/direct candidates must not be marked", len(g.fail.until))
	}
}
