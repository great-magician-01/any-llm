package gateway

import (
	"net/http"
	"strconv"
	"time"

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
	return s[:n] + "...(truncated, total=" + strconv.Itoa(len(s)) + ")"
}
