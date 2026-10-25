package gateway

import (
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/great-magician-01/any-llm/internal/logger"
)

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	size        int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.wroteHeader {
		return
	}
	s.status = code
	s.wroteHeader = true
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		s.status = http.StatusOK
		s.wroteHeader = true
	}
	n, err := s.ResponseWriter.Write(b)
	s.size += n
	return n, err
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

type loggingMiddleware struct {
	next    http.Handler
	handler string
}

func LoggingMiddleware(next http.Handler, name string) http.Handler {
	return &loggingMiddleware{next: next, handler: name}
}

func (l *loggingMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	sr := &statusRecorder{ResponseWriter: w, status: 0}
	l.next.ServeHTTP(sr, r)
	duration := time.Since(start)
	status := sr.status
	if status == 0 {
		status = 200
	}
	logger.Info("request",
		"handler", l.handler,
		"method", r.Method,
		"path", r.URL.Path,
		"status", status,
		"size", sr.size,
		"duration_ms", duration.Milliseconds(),
		"remote", r.RemoteAddr,
	)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// 不要在 UTF-8 字符中间截断：这里截的是上游错误消息（errors.go 用 500），
	// 中文/emoji 很常见，按字节切会往日志里写入非法 UTF-8，日志采集端会报错。
	return cutUTF8(s, n) + "...(truncated, total=" + strconv.Itoa(len(s)) + ")"
}

// cutUTF8 返回 s 截到不超过 n 字节、且不切断多字节 rune 的前缀。
// 调用方保证 n < len(s)。会话 id 等入库字符串同样需要这种截断
// （见 convid.go 的 sessionIDMaxLen），按字节切会写入非法 UTF-8。
func cutUTF8(s string, n int) string {
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
