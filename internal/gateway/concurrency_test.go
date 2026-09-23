package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/great-magician-01/any-llm/internal/store"
	"github.com/great-magician-01/any-llm/internal/upstream"
)

func TestConcManagerTryAcquireLimit(t *testing.T) {
	m := newConcManager()
	u := &store.Upstream{ID: 1, MaxConcurrent: 2}
	r1, ok := m.tryAcquire(u)
	if !ok {
		t.Fatal("first acquire should succeed")
	}
	r2, ok := m.tryAcquire(u)
	if !ok {
		t.Fatal("second acquire should succeed")
	}
	if _, ok := m.tryAcquire(u); ok {
		t.Fatal("third acquire should fail (limit 2)")
	}
	r1()
	r3, ok := m.tryAcquire(u)
	if !ok {
		t.Fatal("acquire after release should succeed")
	}
	r2()
	r3()
}

func TestConcManagerUnlimitedWhenZeroOrNegative(t *testing.T) {
	m := newConcManager()
	for _, limit := range []int{0, -1} {
		u := &store.Upstream{ID: 2, MaxConcurrent: limit}
		for i := 0; i < 1000; i++ {
			if _, ok := m.tryAcquire(u); !ok {
				t.Fatalf("limit %d should be unlimited, failed at %d", limit, i)
			}
		}
	}
}

// 上限修改后重建信号量：新容量立即生效，旧通道上的在途请求不受影响。
func TestConcManagerLimitChangeRebuilds(t *testing.T) {
	m := newConcManager()
	u := &store.Upstream{ID: 3, MaxConcurrent: 1}
	rel, ok := m.tryAcquire(u)
	if !ok {
		t.Fatal("acquire should succeed")
	}
	if _, ok := m.tryAcquire(u); ok {
		t.Fatal("limit 1 should block second acquire")
	}
	// 管理端调大上限：下一次请求解析到新的 MaxConcurrent，信号量重建。
	u2 := &store.Upstream{ID: 3, MaxConcurrent: 3}
	for i := 0; i < 3; i++ {
		if _, ok := m.tryAcquire(u2); !ok {
			t.Fatalf("after limit raise, acquire %d should succeed", i)
		}
	}
	if _, ok := m.tryAcquire(u2); ok {
		t.Fatal("new limit 3 should block fourth acquire")
	}
	rel() // 旧槽位释放回旧通道，不 panic、不影响新通道
	if _, ok := m.tryAcquire(u2); ok {
		t.Fatal("releasing old-channel slot must not free a new-channel slot")
	}
}

func TestConcManagerIndependentPerUpstream(t *testing.T) {
	m := newConcManager()
	a := &store.Upstream{ID: 10, MaxConcurrent: 1}
	b := &store.Upstream{ID: 11, MaxConcurrent: 1}
	if _, ok := m.tryAcquire(a); !ok {
		t.Fatal("acquire on a should succeed")
	}
	if _, ok := m.tryAcquire(b); !ok {
		t.Fatal("acquire on b should succeed (independent semaphore)")
	}
}

// 重复释放是空操作：不会凭空多出槽位，也不会在空通道上阻塞。
func TestConcManagerReleaseIdempotent(t *testing.T) {
	m := newConcManager()
	u := &store.Upstream{ID: 20, MaxConcurrent: 1}
	rel, ok := m.tryAcquire(u)
	if !ok {
		t.Fatal("acquire should succeed")
	}
	rel()
	rel() // 第二次释放必须是空操作（否则这里会在空 channel 上永久阻塞）
	rel2, ok := m.tryAcquire(u)
	if !ok {
		t.Fatal("acquire after release should succeed")
	}
	if _, ok := m.tryAcquire(u); ok {
		t.Fatal("double release must not create a phantom slot")
	}
	rel2()
}

// 并发压测：大量 goroutine 抢同一上限的信号量，在途持有数不得超过上限。
// （无 -race 环境也能验证计数正确性。）
func TestConcManagerConcurrentStress(t *testing.T) {
	m := newConcManager()
	u := &store.Upstream{ID: 30, MaxConcurrent: 10}
	var inFlight, maxSeen atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				rel, ok := m.tryAcquire(u)
				if !ok {
					continue
				}
				n := inFlight.Add(1)
				for {
					old := maxSeen.Load()
					if n <= old || maxSeen.CompareAndSwap(old, n) {
						break
					}
				}
				inFlight.Add(-1)
				rel()
			}
		}()
	}
	wg.Wait()
	if got := maxSeen.Load(); got > 10 {
		t.Fatalf("max in-flight %d exceeds limit 10", got)
	} else if got == 0 {
		t.Fatal("no acquires succeeded")
	}
	if got := inFlight.Load(); got != 0 {
		t.Fatalf("in-flight %d after all released", got)
	}
}

// 并发占满时直连路由回干净 429（rate_limit_error），且不产生 usage 记录；
// 释放槽位后同一路由恢复可用。
func TestCompletionConcurrencyLimit429(t *testing.T) {
	srv := okUpstreamServer(t, "ok-after-release")
	g, d := setupGateway(t)
	uid, _ := store.CreateUpstream(d, &store.Upstream{Name: "oai", BaseURL: srv.URL, APIKey: "sk", Format: "openai", MaxConcurrent: 1})
	store.AddModel(d, uid, store.UpstreamModel{ModelName: "gpt-4o"})
	k, _ := store.CreateExtKey(d, "test", "", 0, 0, nil)
	g.client = upstream.NewClient(http.DefaultClient)

	// 测试直接占住唯一的并发槽，模拟上游满载。
	u, _ := store.GetUpstreamByID(d, uid)
	rel, ok := g.conc.tryAcquire(u)
	if !ok {
		t.Fatal("test should acquire the only slot")
	}

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"oai/gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != 429 {
		t.Fatalf("status=%d want 429, body=%s", w.Code, w.Body.String())
	}
	var errBody struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &errBody); err != nil || errBody.Error.Type != "rate_limit_error" {
		t.Fatalf("error body=%s err=%v", w.Body.String(), err)
	}
	// 未发起上游调用，不记 usage。
	if _, total, _ := store.UsageRecordsList(d, 1, 10); total != 0 {
		t.Fatalf("usage records=%d want 0 (no upstream call made)", total)
	}

	// 释放后恢复。
	rel()
	req2 := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"oai/gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req2.Header.Set("Authorization", "Bearer "+k.Key)
	w2 := httptest.NewRecorder()
	g.ServeHTTP(w2, req2)
	if w2.Code != 200 || !strings.Contains(w2.Body.String(), "ok-after-release") {
		t.Fatalf("after release status=%d body=%s", w2.Code, w2.Body.String())
	}
}

// 别名候选链：首选候选并发占满时故障转移到下一候选（占满不算调用失败，
// 不记 usage）；并验证 ResolveAliasTargets 带出了 max_concurrent（否则首选
// 不会被跳过，响应会来自 first）。
func TestCompletionConcurrencyFailover(t *testing.T) {
	first := okUpstreamServer(t, "from-first")
	second := okUpstreamServer(t, "from-second")
	g, k := setupAliasGateway(t)
	d := g.db
	uid1, _ := store.CreateUpstream(d, &store.Upstream{Name: "busy", BaseURL: first.URL, APIKey: "sk", Format: "openai", MaxConcurrent: 1})
	uid2, _ := store.CreateUpstream(d, &store.Upstream{Name: "free", BaseURL: second.URL, APIKey: "sk", Format: "openai", MaxConcurrent: 5})
	store.CreateAlias(d, &store.ModelAlias{Name: "fixed", Bindings: []store.AliasBinding{
		{UpstreamID: uid1, ModelName: "m1"},
		{UpstreamID: uid2, ModelName: "m2"},
	}})

	u1, _ := store.GetUpstreamByID(d, uid1)
	rel, ok := g.conc.tryAcquire(u1)
	if !ok {
		t.Fatal("test should acquire busy upstream's only slot")
	}
	defer rel()

	w := aliasRequest(t, g, k.Key, `{"model":"fixed","messages":[{"role":"user","content":"hi"}]}`)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "from-second") {
		t.Fatalf("expected failover to second candidate, body=%s", w.Body.String())
	}
	// 只有真正调用的候选记 usage。
	records, total, _ := store.UsageRecordsList(d, 1, 10)
	if total != 1 || records[0].UpstreamName != "free" {
		t.Fatalf("records=%+v total=%d", records, total)
	}
}

// 流式请求在所有候选并发占满时回干净 429（头部未 flush），而不是 200 + 带内
// 错误帧——SDK 对 HTTP 429 会自动重试。
func TestStreamConcurrencyLimit429(t *testing.T) {
	srv := okUpstreamServer(t, "unused")
	g, d := setupGateway(t)
	uid, _ := store.CreateUpstream(d, &store.Upstream{Name: "oai", BaseURL: srv.URL, APIKey: "sk", Format: "openai", MaxConcurrent: 1})
	store.AddModel(d, uid, store.UpstreamModel{ModelName: "gpt-4o"})
	k, _ := store.CreateExtKey(d, "test", "", 0, 0, nil)
	g.client = upstream.NewClient(http.DefaultClient)

	u, _ := store.GetUpstreamByID(d, uid)
	rel, ok := g.conc.tryAcquire(u)
	if !ok {
		t.Fatal("test should acquire the only slot")
	}
	defer rel()

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"oai/gpt-4o","messages":[{"role":"user","content":"hi"}],"stream":true}`))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != 429 {
		t.Fatalf("status=%d want 429 (clean rejection before stream headers), body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type=%q want JSON error, not an SSE stream", ct)
	}
}

// 并发槽在完成（或客户端中断）后释放：上限 1 时同上游连续两个请求都成功，
// 覆盖非流式与流式两条释放路径。
func TestCompletionConcurrencySlotReleased(t *testing.T) {
	g, d := setupGateway(t)
	k, _ := store.CreateExtKey(d, "test", "", 0, 0, nil)
	g.client = upstream.NewClient(http.DefaultClient)

	// 非流式：cap 1，连续两请求（第二个成功说明第一个的槽位已释放）。
	jsonSrv := okUpstreamServer(t, "non-stream-ok")
	uid1, _ := store.CreateUpstream(d, &store.Upstream{Name: "plain", BaseURL: jsonSrv.URL, APIKey: "sk", Format: "openai", MaxConcurrent: 1})
	store.AddModel(d, uid1, store.UpstreamModel{ModelName: "gpt-4o"})
	for i := 0; i < 2; i++ {
		w := aliasRequest(t, g, k.Key, `{"model":"plain/gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "non-stream-ok") {
			t.Fatalf("non-stream request %d: status=%d body=%s (slot not released?)", i, w.Code, w.Body.String())
		}
	}

	// 流式：cap 1，连续两请求。
	sseSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hi\"}}]}\n\n"))
		w.Write([]byte("data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
		w.(http.Flusher).Flush()
	}))
	defer sseSrv.Close()
	uid2, _ := store.CreateUpstream(d, &store.Upstream{Name: "sse", BaseURL: sseSrv.URL, APIKey: "sk", Format: "openai", MaxConcurrent: 1})
	store.AddModel(d, uid2, store.UpstreamModel{ModelName: "gpt-4o"})
	for i := 0; i < 2; i++ {
		w := aliasRequest(t, g, k.Key, `{"model":"sse/gpt-4o","messages":[{"role":"user","content":"hi"}],"stream":true}`)
		if w.Code != 200 {
			t.Fatalf("stream request %d: status=%d want 200 (slot not released?)", i, w.Code)
		}
		if !strings.Contains(w.Body.String(), "[DONE]") {
			t.Fatalf("stream request %d: body=%s", i, w.Body.String())
		}
	}
}
