package gateway

import (
	"errors"
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
// clear 立即摘除；不可缓存的失败 mark 是空操作。
func TestFailureCacheMarkDownClear(t *testing.T) {
	c := newFailureCache()
	target := &store.AliasTarget{Upstream: &store.Upstream{ID: 1, Name: "u"}, ModelName: "m"}
	t0 := time.Now()

	c.mark(target, errors.New("call upstream: timeout"), t0)
	if !c.down(target, t0.Add(time.Second)) {
		t.Fatal("marked candidate should be down inside TTL")
	}
	if c.down(target, t0.Add(failureCacheTTL)) {
		t.Fatal("candidate should leave cooldown at TTL boundary")
	}
	if len(c.until) != 0 {
		t.Fatalf("expired entry should be evicted, got %d entries", len(c.until))
	}

	c.mark(target, errors.New("boom"), t0)
	c.clear(target)
	if c.down(target, t0.Add(time.Second)) {
		t.Fatal("cleared candidate should not be down")
	}

	c.mark(target, &upstream.UpstreamError{StatusCode: 400}, t0)
	if c.down(target, t0.Add(time.Second)) {
		t.Fatal("request-level error (400) must not enter cooldown")
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

// 单候选（直连路由）：首个请求拿到真实上游错误并标记冷却；冷却期内的请求
// 直接回 503，上游不再被命中。
func TestFailureCacheAllDownNonStream503(t *testing.T) {
	bad, badHits := countingFailServer(t, 500)
	g, k := setupAliasGateway(t)
	d := g.db
	uid, _ := store.CreateUpstream(d, &store.Upstream{Name: "bad", BaseURL: bad.URL, APIKey: "sk", Format: "openai"})
	store.AddModel(d, uid, store.UpstreamModel{ModelName: "m"})

	if w := aliasRequest(t, g, k.Key, `{"model":"bad/m","messages":[{"role":"user","content":"hi"}]}`); w.Code != 500 {
		t.Fatalf("first request status=%d want 500 (real upstream error), body=%s", w.Code, w.Body.String())
	}
	w := aliasRequest(t, g, k.Key, `{"model":"bad/m","messages":[{"role":"user","content":"hi"}]}`)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "failure cooldown") {
		t.Fatalf("second request status=%d want 503 cooldown, body=%s", w.Code, w.Body.String())
	}
	if badHits.Load() != 1 {
		t.Fatalf("cooling upstream was hit again: hits=%d want 1", badHits.Load())
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

// 流式全候选冷却：第一次请求头部已 flush，只能带内报错（状态仍 200）；
// 第二次请求在头部 flush 前识别冷却，回干净的 503，上游不再被命中。
func TestFailureCacheAllDownStream503(t *testing.T) {
	bad, badHits := countingFailServer(t, 500)
	g, k := setupAliasGateway(t)
	d := g.db
	uid, _ := store.CreateUpstream(d, &store.Upstream{Name: "bad", BaseURL: bad.URL, APIKey: "sk", Format: "openai"})
	store.CreateAlias(d, &store.ModelAlias{Name: "fixed", Bindings: []store.AliasBinding{
		{UpstreamID: uid, ModelName: "m1"},
	}})

	body := `{"model":"fixed","messages":[{"role":"user","content":"hi"}],"stream":true}`
	w := aliasRequest(t, g, k.Key, body)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"error"`) {
		t.Fatalf("first request should fail in-band after 200 header: status=%d body=%s", w.Code, w.Body.String())
	}
	w = aliasRequest(t, g, k.Key, body)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "failure cooldown") {
		t.Fatalf("second request status=%d want 503 cooldown, body=%s", w.Code, w.Body.String())
	}
	if badHits.Load() != 1 {
		t.Fatalf("cooling upstream was hit again: hits=%d want 1", badHits.Load())
	}
}
