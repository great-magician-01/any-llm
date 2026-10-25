package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/great-magician-01/any-llm/internal/logger"
)

const sessionName = "s"

// writeJSON 写一个 JSON 响应；管理端各错误出口共用（adminapi 有同名实现的
// 前身，这里保持同一形状，避免每个出口手写三行）。
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		logger.Warn("auth: encode response failed", "err", err)
	}
}

// neverExpires is the signed expiry for session TTLs of 0 (never expire).
// It round-trips through RFC3339 fine and always parses as valid, so
// VerifySession needs no special case.
var neverExpires = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)

func SignSession(secret []byte, expiresAt time.Time) (string, error) {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(expiresAt.Format(time.RFC3339)))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return expiresAt.Format(time.RFC3339) + "." + sig, nil
}

func VerifySession(secret []byte, token string) (*time.Time, error) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid token format")
	}
	expiresAt, err := time.Parse(time.RFC3339, parts[0])
	if err != nil {
		return nil, fmt.Errorf("invalid expiry: %w", err)
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(parts[0]))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(parts[1])) {
		return nil, fmt.Errorf("invalid signature")
	}
	if time.Now().After(expiresAt) {
		return nil, fmt.Errorf("session expired")
	}
	return &expiresAt, nil
}

type Middleware struct {
	secret         []byte
	masterPassword string
	sessionTTL     time.Duration
}

// NewMiddleware creates the admin auth middleware. sessionTTL is how long
// logins stay valid; 0 means sessions never expire.
func NewMiddleware(secret, masterPassword string, sessionTTL time.Duration) *Middleware {
	return &Middleware{
		secret:         []byte(secret),
		masterPassword: masterPassword,
		sessionTTL:     sessionTTL,
	}
}

func (m *Middleware) Wrap(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/admin/login" && r.Method == "POST" {
			m.handleLogin(w, r)
			return
		}
		if r.URL.Path == "/api/admin/logout" && r.Method == "POST" {
			m.handleLogout(w, r)
			return
		}
		if !m.authenticate(w, r) {
			logger.Warn("auth rejected: invalid or expired session", "remote", r.RemoteAddr, "path", r.URL.Path)
			writeJSON(w, 401, map[string]any{"error": "unauthorized"})
			return
		}
		handler.ServeHTTP(w, r)
	})
}

func (m *Middleware) authenticate(w http.ResponseWriter, r *http.Request) bool {
	cookie, err := r.Cookie(sessionName)
	if err != nil {
		return false
	}
	exp, err := VerifySession(m.secret, cookie.Value)
	if err != nil {
		return false
	}
	m.maybeRenew(w, *exp)
	return true
}

// maybeRenew slides the session forward: once less than half the TTL remains,
// the cookie is re-issued with a fresh full TTL so active users are not
// logged out mid-session. Never-expiring sessions (TTL <= 0) are left alone.
func (m *Middleware) maybeRenew(w http.ResponseWriter, exp time.Time) {
	if m.sessionTTL <= 0 || time.Until(exp) > m.sessionTTL/2 {
		return
	}
	newExp := time.Now().Add(m.sessionTTL)
	token, err := SignSession(m.secret, newExp)
	if err != nil {
		logger.Warn("auth: failed to renew session", "err", err)
		return
	}
	setSessionCookie(w, token, newExp)
}

func setSessionCookie(w http.ResponseWriter, token string, exp time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionName,
		Value:    token,
		Path:     "/api/admin",
		Expires:  exp,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

func (m *Middleware) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		logger.Warn("auth login: invalid JSON body", "remote", r.RemoteAddr, "err", err)
		writeJSON(w, 400, map[string]any{"error": "invalid json"})
		return
	}
	// 常量时间比较：明文 != 会在时间上泄漏前缀匹配长度（登录无频率限制，
	// 比较是唯一可以做得更稳的一环）。
	if subtle.ConstantTimeCompare([]byte(req.Password), []byte(m.masterPassword)) != 1 {
		logger.Warn("auth login: wrong password", "remote", r.RemoteAddr)
		writeJSON(w, 401, map[string]any{"error": "wrong password"})
		return
	}
	logger.Info("auth login succeeded", "remote", r.RemoteAddr)
	exp := time.Now().Add(m.sessionTTL)
	if m.sessionTTL <= 0 {
		exp = neverExpires
	}
	token, err := SignSession(m.secret, exp)
	if err != nil {
		logger.Error("auth login: failed to sign session", "remote", r.RemoteAddr, "err", err)
		writeJSON(w, 500, map[string]any{"error": "session error"})
		return
	}
	setSessionCookie(w, token, exp)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (m *Middleware) handleLogout(w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionName,
		Value:    "",
		Path:     "/api/admin",
		MaxAge:   -1,
		HttpOnly: true,
	})
	writeJSON(w, 200, map[string]any{"ok": true})
}
