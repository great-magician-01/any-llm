package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testSecret = "test-secret-32-bytes-long!!"

// recordingHandler 返回一个受保护 handler，并记录它是否真的被调用到。
func recordingHandler(called *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*called = true
		w.WriteHeader(200)
	})
}

func respCookie(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionName {
			return c
		}
	}
	t.Fatalf("no %q cookie in response (cookies=%v)", sessionName, w.Result().Cookies())
	return nil
}

// tamperSignature 把签名首字符换成另一个 base64 字符，制造「格式合法、签名错」
// 的 token —— 这正是 VerifySession 里 hmac.Equal 那条分支，此前没有任何用例
// 走到（现有用例只测了 "bad-token" 这种连格式都不对的串）。
func tamperSignature(token string) string {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return token
	}
	sig := parts[1]
	first := byte('A')
	if len(sig) > 0 && sig[0] == 'A' {
		first = 'B'
	}
	return parts[0] + "." + string(first) + sig[1:]
}

// TestVerifySession_RejectsWrongSecretAndTamperedSignature 是会话 cookie 的
// 完整性契约：换个密钥、或者改一个签名字符都必须被拒绝（HMAC 校验不能被绕过）。
func TestVerifySession_RejectsWrongSecretAndTamperedSignature(t *testing.T) {
	secret := []byte(testSecret)
	token, err := SignSession(secret, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := VerifySession([]byte("another-secret-32-bytes-long"), token); err == nil {
		t.Error("token signed with a different secret must not verify")
	} else if !strings.Contains(err.Error(), "signature") {
		t.Errorf("wrong-secret error=%v want a signature error", err)
	}

	if _, err := VerifySession(secret, tamperSignature(token)); err == nil {
		t.Error("tampered signature must not verify")
	}

	// 合法过期时间 + 空签名：格式对但对不上签名。
	exp := time.Now().Add(time.Hour).Format(time.RFC3339)
	if _, err := VerifySession(secret, exp+"."); err == nil {
		t.Error("empty signature must not verify")
	}

	// 过期时间不可解析（格式错）。
	if _, err := VerifySession(secret, "not-a-time.abc"); err == nil {
		t.Error("unparseable expiry must not verify")
	}
}

// TestLoginCookieAttributes 钉住会话 cookie 的安全属性：只发给 /api/admin、
// HttpOnly（JS 读不到）、SameSite=Strict（CSRF 防护）、带过期时间。
// 这些属性没有任何用例断言过，改错了也不会有测试报警。
func TestLoginCookieAttributes(t *testing.T) {
	ttl := 24 * time.Hour
	m := NewMiddleware(testSecret, "admin", ttl)
	req := httptest.NewRequest("POST", "/api/admin/login", strings.NewReader(`{"password":"admin"}`))
	w := httptest.NewRecorder()
	m.handleLogin(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	c := respCookie(t, w)
	if c.Value == "" {
		t.Error("session cookie value must be a signed token")
	}
	if c.Path != "/api/admin" {
		t.Errorf("Path=%q want /api/admin (cookie must not be sent to /v1)", c.Path)
	}
	if !c.HttpOnly {
		t.Error("HttpOnly must be set so JS cannot read the session token")
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Errorf("SameSite=%v want Strict", c.SameSite)
	}
	if c.Expires.IsZero() || time.Until(c.Expires) > ttl+time.Minute || time.Until(c.Expires) < ttl-time.Minute {
		t.Errorf("Expires=%v want about now+%s", c.Expires, ttl)
	}
	if _, err := VerifySession([]byte(testSecret), c.Value); err != nil {
		t.Errorf("issued cookie does not verify: %v", err)
	}
}

// TestLoginInvalidJSONIs400 覆盖登录体不是 JSON 的分支：必须 400 且**不**下发
// 会话 cookie（不能因为解析失败就放行）。
func TestLoginInvalidJSONIs400(t *testing.T) {
	m := NewMiddleware(testSecret, "admin", time.Hour)
	req := httptest.NewRequest("POST", "/api/admin/login", strings.NewReader("not-json"))
	w := httptest.NewRecorder()
	m.handleLogin(w, req)
	if w.Code != 400 {
		t.Fatalf("status=%d want 400, body=%s", w.Code, w.Body.String())
	}
	if len(w.Result().Cookies()) != 0 {
		t.Errorf("no session cookie may be issued on a bad login: %v", w.Result().Cookies())
	}
}

// TestLoginWrongPasswordIssuesNoCookie：密码错误必须 401 且不下发 cookie。
func TestLoginWrongPasswordIssuesNoCookie(t *testing.T) {
	m := NewMiddleware(testSecret, "admin", time.Hour)
	req := httptest.NewRequest("POST", "/api/admin/login", strings.NewReader(`{"password":"nope"}`))
	w := httptest.NewRecorder()
	m.handleLogin(w, req)
	if w.Code != 401 {
		t.Fatalf("status=%d want 401", w.Code)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionName && c.Value != "" {
			t.Fatalf("wrong password must not issue a session cookie: %+v", c)
		}
	}
}

// TestWrap_ExemptPaths 断言 Wrap 的免鉴权白名单：只有 POST login/logout 免鉴权，
// 其余（含 GET /api/admin/login）必须 401 且不触达被保护的 handler。
// logout 免鉴权是刻意的：会话过期后用户仍然要能登出。
func TestWrap_ExemptPaths(t *testing.T) {
	m := NewMiddleware(testSecret, "admin", time.Hour)
	called := false
	h := m.Wrap(recordingHandler(&called))

	t.Run("POST logout 免鉴权并清 cookie", func(t *testing.T) {
		called = false
		req := httptest.NewRequest("POST", "/api/admin/logout", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("status=%d want 200 (logout must work without a valid session)", w.Code)
		}
		c := respCookie(t, w)
		if c.MaxAge != -1 || c.Value != "" {
			t.Errorf("logout cookie=%+v want MaxAge=-1 and empty value", c)
		}
		if c.Path != "/api/admin" {
			t.Errorf("logout cookie Path=%q want /api/admin (otherwise it never clears)", c.Path)
		}
		if called {
			t.Error("protected handler must not run for /api/admin/logout")
		}
	})

	t.Run("GET login 不免鉴权", func(t *testing.T) {
		called = false
		req := httptest.NewRequest("GET", "/api/admin/login", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 401 {
			t.Fatalf("status=%d want 401 (only POST /api/admin/login is exempt)", w.Code)
		}
		if called {
			t.Error("protected handler must not run for GET /api/admin/login")
		}
	})
}

// TestWrap_ExpiredAndTamperedSessionsAre401：过期会话与签名被篡改的会话经过
// 中间件都必须 401（并且带客户端可识别的 JSON 错误体），受保护的 handler 不能
// 被调用。
func TestWrap_ExpiredAndTamperedSessionsAre401(t *testing.T) {
	m := NewMiddleware(testSecret, "admin", time.Hour)
	called := false
	h := m.Wrap(recordingHandler(&called))

	expired, _ := SignSession([]byte(testSecret), time.Now().Add(-time.Minute))
	valid, _ := SignSession([]byte(testSecret), time.Now().Add(time.Hour))

	cases := []struct {
		name  string
		token string
	}{
		{"过期", expired},
		{"签名被篡改", tamperSignature(valid)},
		{"换了密钥签发", func() string {
			t, _ := SignSession([]byte("other-secret-32-bytes-long!!"), time.Now().Add(time.Hour))
			return t
		}()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			called = false
			req := httptest.NewRequest("GET", "/api/admin/upstreams", nil)
			req.AddCookie(&http.Cookie{Name: sessionName, Value: c.token})
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != 401 {
				t.Fatalf("status=%d want 401", w.Code)
			}
			if !strings.Contains(w.Body.String(), "unauthorized") {
				t.Errorf("body=%q want an unauthorized JSON error", w.Body.String())
			}
			if called {
				t.Error("protected handler must not run with an invalid session")
			}
		})
	}
}

// TestWrap_RenewedCookieKeepsAttributes：滑动续期下发的 cookie 必须和登录时
// 一样带全安全属性（续期路径是另一处 Set-Cookie，容易漏属性）。
func TestWrap_RenewedCookieKeepsAttributes(t *testing.T) {
	ttl := 2 * time.Hour
	m := NewMiddleware(testSecret, "admin", ttl)
	called := false
	h := m.Wrap(recordingHandler(&called))

	// 剩余 30 分钟 < TTL 的一半 → 触发续期。
	near, _ := SignSession([]byte(testSecret), time.Now().Add(30*time.Minute))
	req := httptest.NewRequest("GET", "/api/admin/upstreams", nil)
	req.AddCookie(&http.Cookie{Name: sessionName, Value: near})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 || !called {
		t.Fatalf("status=%d called=%v want 200/true", w.Code, called)
	}
	c := respCookie(t, w)
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/api/admin" {
		t.Errorf("renewed cookie lost security attributes: %+v", c)
	}
	exp, err := VerifySession([]byte(testSecret), c.Value)
	if err != nil {
		t.Fatalf("renewed token does not verify: %v", err)
	}
	if time.Until(*exp) < ttl-time.Minute {
		t.Errorf("renewed expiry=%v want about now+%s", exp, ttl)
	}
}
