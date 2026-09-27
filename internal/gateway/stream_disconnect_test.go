package gateway

import (
	"context"
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

// ---------------------------------------------------------------------------
// 本文件的 helper 统一加 sd 前缀，避免与同包既有 helper 重名。
// ---------------------------------------------------------------------------

// sdStreamUpstream 返回一个正常回一帧内容的 SSE 假上游。
func sdStreamUpstream(t *testing.T, text string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 先读完请求体：服务端带着未读请求体关闭连接会发 RST，可能丢掉已写出的响应
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		w.Write([]byte(`data: {"id":"c1","model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"` + text + `"}}]}` + "\n\n"))
		f.Flush()
		w.Write([]byte("data: [DONE]\n\n"))
		f.Flush()
	}))
	t.Cleanup(srv.Close)
	return srv
}

// ---------------------------------------------------------------------------
// 4. 客户端断连 → 取消上游 + 释放并发槽
// ---------------------------------------------------------------------------

// 业务规则：流式请求的客户端上下文就是上游调用的上下文——客户端断开必须
// 立刻取消上游请求（否则上游按写超时慢慢烧配额，网关还占着连接），并发槽必须
// 归还（流式槽位一直持有到请求结束，漏放会让上限 1 的上游永久卡死），usage 必须
// 如实记 error 而不是 ok。三件事一起断言：任一漏掉都是线上事故。
func TestStreamClientDisconnectCancelsUpstreamAndReleasesSlot(t *testing.T) {
	upstreamHit := make(chan struct{})
	upstreamCancelled := make(chan struct{})
	var calls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 先把请求体读完：net/http 只在读完 body 后才开始后台读连接，
		// 否则客户端断开时服务端的 r.Context() 不会被取消（测试自身的坑）。
		io.Copy(io.Discard, r.Body)
		if calls.Add(1) == 1 {
			close(upstreamHit)
			<-r.Context().Done()
			close(upstreamCancelled)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		w.Write([]byte("data: {\"id\":\"c1\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"after-release\"}}]}\n\n"))
		f.Flush()
		w.Write([]byte("data: [DONE]\n\n"))
		f.Flush()
	}))
	defer srv.Close()

	g, d := setupGateway(t)
	// MaxConcurrent=1：槽位泄漏的话第二个请求必定 429
	uid, _ := store.CreateUpstream(d, &store.Upstream{Name: "oai", BaseURL: srv.URL, APIKey: "sk", Format: "openai", MaxConcurrent: 1})
	store.AddModel(d, uid, store.UpstreamModel{ModelName: "m"})
	k, _ := store.CreateExtKey(d, "test", "", 0, 0, nil)
	g.client = upstream.NewClient(http.DefaultClient)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"oai/m","messages":[{"role":"user","content":"hi"}],"stream":true}`)).WithContext(ctx)
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
	// 客户端断连
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ServeHTTP did not return after the client disconnected")
	}
	// (a) 上游请求的 context 被取消
	select {
	case <-upstreamCancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("upstream request context was not cancelled on client disconnect")
	}
	// (c) usage 记 error：断连不是成功，不能污染用量统计
	records, total, _ := store.UsageRecordsList(d, 1, 10)
	if total != 1 {
		t.Fatalf("usage records=%d want 1", total)
	}
	rec := records[0]
	if rec.Status != "error" || !rec.Stream || rec.UpstreamName != "oai" || rec.Model != "m" {
		t.Fatalf("record=%+v want upstream=oai model=m status=error stream=true", rec)
	}
	if rec.TotalTokens != 0 {
		t.Fatalf("record total_tokens=%d want 0 (call never completed)", rec.TotalTokens)
	}

	// (b) 并发槽已释放：上限 1 的同一上游立刻还能再服务一个请求
	w2 := aliasRequest(t, g, k.Key, `{"model":"oai/m","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	if w2.Code != 200 || !strings.Contains(w2.Body.String(), "after-release") {
		t.Fatalf("follow-up status=%d body=%s (stream slot leaked after the client disconnected?)", w2.Code, w2.Body.String())
	}
	if calls.Load() != 2 {
		t.Fatalf("upstream calls=%d want 2", calls.Load())
	}
}

// ---------------------------------------------------------------------------
// 5. 流式候选因并发满被跳过并转移
// ---------------------------------------------------------------------------

// 业务规则（handleStream 的预占循环 + 循环内 tryAcquire）：流式请求同样受上游
// 并发上限约束；首选候选槽位已满时**不发调用、不记 usage**，直接转移到下一个
// 候选（对客户端透明）。同时验证流式请求会一直持有槽位到上游流结束——正是这个
// 持有让第二个请求看到了「满」。
func TestStreamCandidateSkippedWhenBusyAndFailover(t *testing.T) {
	release := make(chan struct{})
	slowHit := make(chan struct{})
	var slowFirst atomic.Bool

	// 慢上游：发一帧内容后挂住，直到测试放行——期间它一直占着唯一槽位
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body) // 先读完请求体，避免 RST 截断响应
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		if slowFirst.CompareAndSwap(false, true) {
			w.Write([]byte("data: {\"id\":\"a\",\"model\":\"m1\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"from-A\"}}]}\n\n"))
			f.Flush()
			close(slowHit)
			<-release
			w.Write([]byte("data: [DONE]\n\n"))
			f.Flush()
			return
		}
		w.Write([]byte("data: [DONE]\n\n"))
		f.Flush()
	}))
	defer slow.Close()
	second := sdStreamUpstream(t, "from-B")

	g, k := setupAliasGateway(t)
	d := g.db
	uid1, _ := store.CreateUpstream(d, &store.Upstream{Name: "busy", BaseURL: slow.URL, APIKey: "sk", Format: "openai", MaxConcurrent: 1})
	uid2, _ := store.CreateUpstream(d, &store.Upstream{Name: "free", BaseURL: second.URL, APIKey: "sk", Format: "openai", MaxConcurrent: 5})
	store.CreateAlias(d, &store.ModelAlias{Name: "fixed", Bindings: []store.AliasBinding{
		{UpstreamID: uid1, ModelName: "m1"},
		{UpstreamID: uid2, ModelName: "m2"},
	}})

	// 慢流式请求占住 busy 的唯一槽位
	w1 := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		req := httptest.NewRequest("POST", "/v1/chat/completions",
			strings.NewReader(`{"model":"busy/m1","messages":[{"role":"user","content":"hi"}],"stream":true}`))
		req.Header.Set("Authorization", "Bearer "+k.Key)
		g.ServeHTTP(w1, req)
		close(done)
	}()
	select {
	case <-slowHit:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("slow upstream was never called")
	}

	// 别名请求：首选 busy 已满，必须转移到 free
	w2 := aliasRequest(t, g, k.Key, `{"model":"fixed","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	if w2.Code != 200 {
		close(release)
		<-done
		t.Fatalf("status=%d want 200, body=%s", w2.Code, w2.Body.String())
	}
	body := w2.Body.String()
	if !strings.Contains(body, "from-B") {
		close(release)
		<-done
		t.Fatalf("alias request did not fail over to the free candidate: %q", body)
	}
	if strings.Contains(body, "from-A") {
		close(release)
		<-done
		t.Fatalf("busy candidate was called despite the concurrency cap: %q", body)
	}
	// 只记 1 条 usage（free 的成功调用）；被跳过的候选不记
	records, total, _ := store.UsageRecordsList(d, 1, 10)
	if total != 1 {
		close(release)
		<-done
		t.Fatalf("usage records=%d want 1 (skipped candidate must not record usage): %+v", total, records)
	}
	if records[0].UpstreamName != "free" || records[0].Model != "m2" || records[0].Status != "ok" {
		close(release)
		<-done
		t.Fatalf("record=%+v want free/m2 ok", records[0])
	}

	// 放行慢请求：它自己照常完成，槽位归还
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("slow request did not finish after release")
	}
	if !strings.Contains(w1.Body.String(), "from-A") || !strings.Contains(w1.Body.String(), "[DONE]") {
		t.Fatalf("slow request body=%q", w1.Body.String())
	}
	records2, total2, _ := store.UsageRecordsList(d, 1, 10)
	if total2 != 2 {
		t.Fatalf("usage records=%d want 2 after both requests ended: %+v", total2, records2)
	}
}
