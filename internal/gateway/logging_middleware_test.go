package gateway

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/great-magician-01/any-llm/internal/logger"
)

// captureLogs 把包级 logger 换成写进 buf 的 slog，返回恢复函数。
//
// gateway 包里没有 t.Parallel()，所以这里临时改全局 logger 是安全的；用完必须
// 恢复，否则后面的用例会跟着往 buf 里写。
func captureLogs(t *testing.T, buf *bytes.Buffer) func() {
	t.Helper()
	prev := logger.Default()
	logger.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	return func() { logger.SetDefault(prev) }
}

// TestLoggingMiddleware_PassesThroughAndLogs 断言日志中间件的两件事：
// 请求必须原样透传给下游（状态码/响应体不变），并且产生一条带
// method/path/status/size/remote 的访问日志（运维排障全靠它）。
func TestLoggingMiddleware_PassesThroughAndLogs(t *testing.T) {
	var buf bytes.Buffer
	restore := captureLogs(t, &buf)
	defer restore()

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("hello"))
	})
	h := LoggingMiddleware(next, "sse-check")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.RemoteAddr = "203.0.113.7:5555"
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("downstream status=%d want 201 (middleware must not alter the response)", rec.Code)
	}
	if rec.Body.String() != "hello" {
		t.Errorf("body=%q want hello", rec.Body.String())
	}

	logged := buf.String()
	for _, want := range []string{
		`msg=request`,
		`handler=sse-check`,
		`method=POST`,
		`path=/v1/chat/completions`,
		`status=201`,
		`size=5`,
		`remote=203.0.113.7:5555`,
	} {
		if !strings.Contains(logged, want) {
			t.Errorf("access log missing %s:\n%s", want, logged)
		}
	}
}

// TestLoggingMiddleware_DefaultsStatusTo200：handler 只 Write 不显式 WriteHeader
// 时，日志里的 status 必须是 200 而不是 0（曾经会因为 status 初值为 0 而记成 0）。
func TestLoggingMiddleware_DefaultsStatusTo200(t *testing.T) {
	var buf bytes.Buffer
	restore := captureLogs(t, &buf)
	defer restore()

	h := LoggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}), "plain")

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/x", nil))

	if logged := buf.String(); !strings.Contains(logged, "status=200") {
		t.Errorf("implicit 200 not logged as 200:\n%s", logged)
	}
}

// TestLoggingMiddleware_KeepsFlusher 是 SSE 的回归位：日志中间件包了一层
// ResponseWriter，如果它不实现 http.Flusher，下游的流式 handler 一 Flush 就
// panic（或者更糟：整个响应被缓冲到结束才发出，keep-alive 全失效）。
func TestLoggingMiddleware_KeepsFlusher(t *testing.T) {
	var gotFlusher bool
	h := LoggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, ok := w.(http.Flusher)
		gotFlusher = ok
		if ok {
			_, _ = w.Write([]byte("part1"))
			f.Flush()
			_, _ = w.Write([]byte("part2"))
		}
	}), "stream")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions", nil))

	if !gotFlusher {
		t.Fatal("wrapped ResponseWriter lost http.Flusher: streaming would break behind the middleware")
	}
	if rec.Body.String() != "part1part2" {
		t.Fatalf("body=%q want part1part2", rec.Body.String())
	}
}

// bareWriter 故意不实现 http.Flusher，用来验证中间件在底层不支持 Flush 时
// 不会 panic。
type bareWriter struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (b *bareWriter) Header() http.Header         { return b.header }
func (b *bareWriter) Write(p []byte) (int, error) { return b.body.Write(p) }
func (b *bareWriter) WriteHeader(code int)        { b.status = code }

// TestLoggingMiddleware_FlushWithoutUnderlyingFlusher：底层 ResponseWriter 不
// 支持 Flusher 时，statusRecorder.Flush 必须安静降级而不是 panic。
func TestLoggingMiddleware_FlushWithoutUnderlyingFlusher(t *testing.T) {
	var buf bytes.Buffer
	restore := captureLogs(t, &buf)
	defer restore()

	h := LoggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f, ok := w.(http.Flusher); ok {
			f.Flush() // 必须不 panic
		}
		_, _ = w.Write([]byte("done"))
	}), "bare")

	bw := &bareWriter{header: http.Header{}}
	h.ServeHTTP(bw, httptest.NewRequest("GET", "/x", nil))

	if bw.body.String() != "done" {
		t.Fatalf("body=%q want done", bw.body.String())
	}
}

// TestTruncate 覆盖截断工具本身：短串原样返回、长串带 total 后缀、且**必须在
// UTF-8 字符边界切开**（上游错误体常常是中文，切出半个字符会写入非法 UTF-8）。
func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Errorf("truncate(short)=%q want unchanged", got)
	}

	long := strings.Repeat("a", 20)
	got := truncate(long, 10)
	if !strings.HasPrefix(got, strings.Repeat("a", 10)) {
		t.Errorf("truncate=%q want the first 10 bytes preserved", got)
	}
	if !strings.Contains(got, "total=20") {
		t.Errorf("truncate=%q should report the original length", got)
	}

	// 每个汉字 3 字节：n=7 落在第二个字的中间（3+3=6 是边界，7 在字内）。
	cn := "错误信息甲乙丙丁"
	got = truncate(cn, 7)
	if !utf8.ValidString(got) {
		t.Errorf("truncate split a UTF-8 rune, produced invalid UTF-8: %q", got)
	}
	if !strings.Contains(got, "total=") {
		t.Errorf("truncate=%q should be marked as truncated", got)
	}
}

// TestTruncateOfEmptyAndExact 边界：空串与正好等于上限的串都必须原样返回。
func TestTruncateOfEmptyAndExact(t *testing.T) {
	if got := truncate("", 5); got != "" {
		t.Errorf("truncate(\"\")=%q want empty", got)
	}
	if got := truncate("12345", 5); got != "12345" {
		t.Errorf("truncate(exact)=%q want unchanged", got)
	}
}
