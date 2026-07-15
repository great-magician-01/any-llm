package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSessionRoundTrip(t *testing.T) {
	secret := []byte("test-secret-32-bytes-long!!")
	exp := time.Now().Add(24 * time.Hour)
	token, err := SignSession(secret, exp)
	if err != nil {
		t.Fatal(err)
	}
	got, err := VerifySession(secret, token)
	if err != nil {
		t.Fatal(err)
	}
	if got.Unix() != exp.Unix() {
		t.Fatalf("expiry mismatch: %d != %d", got.Unix(), exp.Unix())
	}
}

func TestVerifyInvalidToken(t *testing.T) {
	secret := []byte("test-secret-32-bytes-long!!")
	_, err := VerifySession(secret, "bad-token")
	if err == nil {
		t.Fatal("expected error for invalid token")
	}
}

func TestVerifyExpiredToken(t *testing.T) {
	secret := []byte("test-secret-32-bytes-long!!")
	exp := time.Now().Add(-1 * time.Hour)
	token, _ := SignSession(secret, exp)
	_, err := VerifySession(secret, token)
	if err == nil {
		t.Fatal("expected error for expired token")
	}
}

func TestLoginSuccess(t *testing.T) {
	m := NewMiddleware("secret12345678901234", "admin")
	req := httptest.NewRequest("POST", "/api/admin/login", strings.NewReader(`{"password":"admin"}`))
	w := httptest.NewRecorder()
	m.handleLogin(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d", w.Code)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "s" {
		t.Fatalf("cookies=%+v", cookies)
	}
}

func TestLoginWrongPassword(t *testing.T) {
	m := NewMiddleware("secret12345678901234", "admin")
	req := httptest.NewRequest("POST", "/api/admin/login", strings.NewReader(`{"password":"wrong"}`))
	w := httptest.NewRecorder()
	m.handleLogin(w, req)
	if w.Code != 401 {
		t.Fatalf("status=%d want 401", w.Code)
	}
}

func TestLogout(t *testing.T) {
	m := NewMiddleware("secret12345678901234", "admin")
	req := httptest.NewRequest("POST", "/api/admin/logout", nil)
	w := httptest.NewRecorder()
	m.handleLogout(w, req)
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].MaxAge != -1 {
		t.Fatalf("logout cookie not set: %+v", cookies)
	}
}

func TestMiddlewareBlocksUnauthenticated(t *testing.T) {
	m := NewMiddleware("secret12345678901234", "admin")
	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	req := httptest.NewRequest("GET", "/api/admin/upstreams", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("status=%d want 401", w.Code)
	}
}

func TestMiddlewareAllowsLogin(t *testing.T) {
	m := NewMiddleware("secret12345678901234", "admin")
	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not reach inner handler for /login")
	}))
	req := httptest.NewRequest("POST", "/api/admin/login", strings.NewReader(`{"password":"admin"}`))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestMiddlewareAllowsAuthenticated(t *testing.T) {
	m := NewMiddleware("secret12345678901234", "admin")
	// login first to get token
	lr := httptest.NewRequest("POST", "/api/admin/login", strings.NewReader(`{"password":"admin"}`))
	lw := httptest.NewRecorder()
	m.handleLogin(lw, lr)
	cookies := lw.Result().Cookies()

	handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	req := httptest.NewRequest("GET", "/api/admin/upstreams", nil)
	req.AddCookie(cookies[0])
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
