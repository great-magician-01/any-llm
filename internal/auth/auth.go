package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/great-magician-01/any-llm/internal/logger"
)

const sessionName = "s"
const sessionExpiry = 24 * time.Hour

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
}

func NewMiddleware(secret, masterPassword string) *Middleware {
	return &Middleware{
		secret:         []byte(secret),
		masterPassword: masterPassword,
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
		if !m.authenticate(r) {
			logger.Warn("auth rejected: invalid or expired session", "remote", r.RemoteAddr, "path", r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(401)
			w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		handler.ServeHTTP(w, r)
	})
}

func (m *Middleware) authenticate(r *http.Request) bool {
	cookie, err := r.Cookie(sessionName)
	if err != nil {
		return false
	}
	_, err = VerifySession(m.secret, cookie.Value)
	return err == nil
}

func (m *Middleware) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		logger.Warn("auth login: invalid JSON body", "remote", r.RemoteAddr, "err", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]any{"error": "invalid json"})
		return
	}
	if req.Password != m.masterPassword {
		logger.Warn("auth login: wrong password", "remote", r.RemoteAddr)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(401)
		json.NewEncoder(w).Encode(map[string]any{"error": "wrong password"})
		return
	}
	logger.Info("auth login succeeded", "remote", r.RemoteAddr)
	exp := time.Now().Add(sessionExpiry)
	token, err := SignSession(m.secret, exp)
	if err != nil {
		logger.Error("auth login: failed to sign session", "remote", r.RemoteAddr, "err", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]any{"error": "session error"})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionName,
		Value:    token,
		Path:     "/api/admin",
		Expires:  exp,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (m *Middleware) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionName,
		Value:    "",
		Path:     "/api/admin",
		MaxAge:   -1,
		HttpOnly: true,
	})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}
