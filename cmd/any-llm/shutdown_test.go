package main

import (
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// startTestServer 在 127.0.0.1 上起一个真实监听的 http.Server（不用默认端口，
// 端口由内核分配），返回 server 与基地址。
func startTestServer(t *testing.T, h http.Handler) (*http.Server, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return srv, "http://" + ln.Addr().String()
}

// TestDrainAndClose_WaitsForInFlightRequest 钉住「排空窗口内让在途请求跑完」：
// 关服不能把已经进来的请求直接掐断（用户会看到半截响应），drain 必须等到
// handler 返回，并且不判定为强制关闭。
func TestDrainAndClose_WaitsForInFlightRequest(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		_, _ = io.WriteString(w, "finished")
	})
	srv, base := startTestServer(t, h)

	type result struct {
		body string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := http.Get(base + "/slow")
		if err != nil {
			done <- result{err: err}
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		done <- result{body: string(b)}
	}()
	<-started // 请求已进入 handler，此刻才开始关服

	drainDone := make(chan bool, 1)
	go func() { drainDone <- drainAndClose(srv, 5*time.Second) }()

	// 关服必须等 handler：给 100ms 让 Shutdown 真正阻塞住。
	select {
	case forced := <-drainDone:
		t.Fatalf("drain returned before the in-flight request finished (forcedClose=%v)", forced)
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("in-flight request failed during drain: %v", got.err)
		}
		if got.body != "finished" {
			t.Fatalf("body=%q want finished (drain must let the handler complete)", got.body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight request never completed")
	}

	select {
	case forced := <-drainDone:
		if forced {
			t.Error("a request that finished inside the window must not force-close connections")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("drainAndClose did not return after the handler finished")
	}
}

// TestDrainAndClose_ForceClosesAfterTimeout 钉住另一头：挂住不返回的请求
// （长连接 SSE 的极端情况）不能阻止进程退出，超时后必须强制关闭并返回 true。
// 同时断言它确实等满了整个窗口（没有提前放弃在途请求）。
func TestDrainAndClose_ForceClosesAfterTimeout(t *testing.T) {
	timeout := 150 * time.Millisecond
	started := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() { close(release) }) // 别让 handler goroutine 泄漏到别的用例
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
	})
	srv, base := startTestServer(t, h)

	go func() {
		resp, err := http.Get(base + "/hang")
		if err == nil {
			resp.Body.Close()
		}
	}()
	<-started

	start := time.Now()
	forced := drainAndClose(srv, timeout)
	elapsed := time.Since(start)

	if !forced {
		t.Fatal("a request hung past the drain window must be force-closed")
	}
	if elapsed < timeout {
		t.Errorf("drain returned after %s, before the %s window elapsed (in-flight requests were cut early)", elapsed, timeout)
	}
	if elapsed > 3*time.Second {
		t.Errorf("drain took %s: the drain window must bound shutdown", elapsed)
	}
}

// TestDrainAndClose_IdleServerReturnsImmediately：没有在途请求时立即返回 false。
func TestDrainAndClose_IdleServerReturnsImmediately(t *testing.T) {
	srv, _ := startTestServer(t, http.NotFoundHandler())
	start := time.Now()
	if forced := drainAndClose(srv, 5*time.Second); forced {
		t.Error("idle server must not report a forced close")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("idle drain took %s, want near-instant", elapsed)
	}
}
