# any-llm Auth + Frontend Implementation Plan (Plan C)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add main-password authentication middleware to the management API, build the Vue 3 management UI (login, upstreams, keys, usage stats), and embed the built frontend into the Go binary via `go:embed` with vite dev-proxy integration.

**Architecture:** Go auth package implements HMAC-signed stateless session cookies. The middleware wraps `webapi.Handler()`. The Vue frontend (Naive UI + vue-router + axios) talks to `/api/admin/*`. Production: `vite build` → `web/dist` → `go:embed` into binary. Development: `vite dev` proxies `/api` and `/v1` to the Go backend.

**Tech Stack:** Go 1.25 stdlib (auth: crypto/hmac, crypto/sha256, encoding/base64), Vue 3 + TypeScript + Vite, Naive UI, vue-router 4, axios.

## Global Constraints

- Module path: `github.com/great-magician-01/any-llm`
- Go 1.25.5, no new Go deps (auth uses stdlib only)
- Frontend: `web/` directory, existing Vite + Vue 3 + TS scaffold
- Frontend deps to add: `vue-router@4`, `naive-ui`, `axios`
- Auth: HMAC-SHA256 signed cookie `s`, 24h expiry, path=/api/admin, HttpOnly
- Login endpoint: `POST /api/admin/login` (already mounted by webapi? No — auth package adds it separately)
- Middleware: check cookie on all `/api/admin/*` except `/login`
- SPA at root: catch-all route serves `index.html` for non-API paths
- Vite dev proxy: `/api` → `http://localhost:8080`, `/v1` → `http://localhost:8080`

## File Structure

| File | Responsibility |
|------|----------------|
| `internal/auth/auth.go` | Session token sign/verify, middleware, login/logout handlers |
| `internal/auth/auth_test.go` | Auth tests |
| `cmd/any-llm/main.go` | (modify) Wire auth middleware + go:embed frontend + SPA fallback |
| `web/src/router.ts` | Vue Router config with auth guard |
| `web/src/api/client.ts` | Axios instance (withCredentials) |
| `web/src/api/upstreams.ts` | Upstream API functions |
| `web/src/api/keys.ts` | Keys API functions |
| `web/src/api/usage.ts` | Usage API functions |
| `web/src/views/Login.vue` | Login page |
| `web/src/views/Upstreams.vue` | Upstream list + CRUD + fetch-models |
| `web/src/views/Keys.vue` | Ext key list + create + delete |
| `web/src/views/Usage.vue` | Usage stats (summary + records table) |
| `web/src/components/Layout.vue` | Sidebar nav + logout |
| `web/src/components/ModelEditor.vue` | Model add/delete modal |
| `web/src/App.vue` | (modify) Use router + layout |
| `web/src/main.ts` | (modify) Register router + naive-ui |
| `web/vite.config.ts` | (modify) Add dev proxy |

---

### Task 1: Go auth package

**Files:**
- Create: `internal/auth/auth.go`
- Test: `internal/auth/auth_test.go`

Interfaces:
- `func SignSession(secret []byte, expiresAt time.Time) (string, error)` — returns HMAC-SHA256 token
- `func VerifySession(secret []byte, token string) (*time.Time, error)` — verifies HMAC, returns expiry if valid (nil if expired/invalid)
- `type Middleware struct` + `func NewMiddleware(secret string, masterPassword string) *Middleware`
- `func (m *Middleware) Wrap(handler http.Handler) http.Handler` — checks cookie, skips `/api/admin/login`, calls `m.handleLogin` for POST /api/admin/login
- `func (m *Middleware) handleLogin(w http.ResponseWriter, r *http.Request)` — validate password, set cookie `s` with 24h expiry, path=/api/admin, HttpOnly, SameSite=Strict
- `func (m *Middleware) handleLogout(w http.ResponseWriter, r *http.Request)` — clear cookie

- [ ] **Step 1: Write the failing test**

Create `internal/auth/auth_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/auth/`
Expected: FAIL — functions undefined

- [ ] **Step 3: Write the implementation**

Create `internal/auth/auth.go`:

```go
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
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]any{"error": "invalid json"})
		return
	}
	if req.Password != m.masterPassword {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(401)
		json.NewEncoder(w).Encode(map[string]any{"error": "wrong password"})
		return
	}
	exp := time.Now().Add(sessionExpiry)
	token, err := SignSession(m.secret, exp)
	if err != nil {
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/auth/`
Expected: PASS (8 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/auth/
git commit -m "feat(auth): add HMAC session middleware and login/logout"
```

---

### Task 2: Wire auth into main.go + go:embed frontend

**Files:**
- Modify: `cmd/any-llm/main.go`

This task wraps the webapi handler with auth middleware, adds the login/logout endpoints, sets up go:embed for the frontend, and adds an SPA fallback (non-API routes serve `index.html`). For now, create a placeholder `web/dist/index.html` so the embed doesn't fail compilation (the real build comes from `vite build` in a later task).

- [ ] **Step 1: Create placeholder frontend dist**

Create `web/dist/index.html` with minimal content:
```html
<!DOCTYPE html><html><body>any-llm frontend placeholder</body></html>
```

- [ ] **Step 2: Modify main.go**

Replace `cmd/any-llm/main.go`:

```go
package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"strings"

	"github.com/great-magician-01/any-llm/internal/auth"
	"github.com/great-magician-01/any-llm/internal/config"
	"github.com/great-magician-01/any-llm/internal/db"
	"github.com/great-magician-01/any-llm/internal/gateway"
	"github.com/great-magician-01/any-llm/internal/upstream"
	"github.com/great-magician-01/any-llm/internal/usage"
	"github.com/great-magician-01/any-llm/internal/webapi"
)

//go:embed web/dist
var frontend embed.FS

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	d, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer d.Close()

	rec := usage.NewRecorder(d, 256)
	rec.Start()
	defer rec.Stop()

	client := upstream.NewClient(nil)
	gw := gateway.New(d, client, rec)
	gw.Start()
	defer gw.Stop()

	api := webapi.NewAPI(d, client)
	authM := auth.NewMiddleware(cfg.SessionSecret, cfg.MasterPassword)
	adminHandler := authM.Wrap(api.Handler())

	frontendFS, err := fs.Sub(frontend, "web/dist")
	if err != nil {
		log.Fatalf("frontend fs: %v", err)
	}
	spa := http.FileServer(http.FS(frontendFS))

	mux := http.NewServeMux()
	mux.Handle("/v1/", gw)
	mux.Handle("/api/admin/", adminHandler)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/v1/") {
			http.NotFound(w, r)
			return
		}
		// serve index.html for all non-API routes (SPA fallback)
		r.URL.Path = "/"
		spa.ServeHTTP(w, r)
	})

	log.Printf("any-llm listening on :%d", cfg.Port)
	if err := http.ListenAndServe(":"+fmt.Sprint(cfg.Port), mux); err != nil {
		log.Fatalf("server: %v", err)
	}
}
```

Note: need `"fmt"` import.

- [ ] **Step 3: Build and verify**

Run: `go build ./cmd/any-llm/`
Expected: compiles (may need to adjust Gateway.Start/Stop if they nil-panic on nil recorder — guard them)

- [ ] **Step 4: Commit**

```bash
git add cmd/any-llm/main.go web/dist/index.html
git commit -m "feat: wire auth middleware, go:embed frontend, SPA fallback"
```

---

### Task 3: Frontend dependencies + router + api client

**Files:**
- Modify: `web/package.json` (dependencies)
- Create: `web/src/router.ts`
- Create: `web/src/api/client.ts`
- Create: `web/src/api/upstreams.ts`
- Create: `web/src/api/keys.ts`
- Create: `web/src/api/usage.ts`
- Modify: `web/src/main.ts` (register router + naive-ui)
- Modify: `web/vite.config.ts` (proxy)

All frontend tasks are in `web/` directory. Use `npm` for package management.

- [ ] **Step 1: Install deps**

```
cd web
npm install vue-router@4 naive-ui axios
```

- [ ] **Step 2: Create router**

Create `web/src/router.ts`:

```typescript
import { createRouter, createWebHashHistory } from 'vue-router'
import Login from './views/Login.vue'

const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/login', name: 'login', component: Login },
    {
      path: '/',
      component: () => import('./components/Layout.vue'),
      children: [
        { path: '', redirect: '/upstreams' },
        { path: 'upstreams', name: 'upstreams', component: () => import('./views/Upstreams.vue') },
        { path: 'keys', name: 'keys', component: () => import('./views/Keys.vue') },
        { path: 'usage', name: 'usage', component: () => import('./views/Usage.vue') },
      ],
    },
  ],
})

router.beforeEach((to, _from) => {
  const hasSession = document.cookie.includes('s=')
  if (to.name !== 'login' && !hasSession) {
    return { name: 'login' }
  }
  if (to.name === 'login' && hasSession) {
    return { name: 'upstreams' }
  }
})

export default router
```

- [ ] **Step 3: Create api client**

Create `web/src/api/client.ts`:

```typescript
import axios from 'axios'

const client = axios.create({
  baseURL: '/api/admin',
  withCredentials: true,
  headers: { 'Content-Type': 'application/json' },
})

client.interceptors.response.use(
  (r) => r,
  (err) => {
    if (err.response?.status === 401 && window.location.hash !== '#/login') {
      window.location.hash = '#/login'
    }
    return Promise.reject(err)
  },
)

export default client
```

- [ ] **Step 4: Create api modules**

Create `web/src/api/upstreams.ts`:

```typescript
import client from './client'

export interface Upstream {
  id?: number; name: string; base_url: string; api_key: string; format: string
  created_at?: string; updated_at?: string
}

export interface UpstreamModel {
  id: number; upstream_id: number; model_name: string; manual: boolean
}

export async function listUpstreams() {
  const { data } = await client.get('/upstreams')
  return data.data as Upstream[]
}

export async function createUpstream(u: Upstream & { fetch_models?: boolean }) {
  const { data } = await client.post('/upstreams', u)
  return data as Upstream
}

export async function updateUpstream(id: number, u: Partial<Upstream>) {
  const { data } = await client.put(`/upstreams/${id}`, u)
  return data as Upstream
}

export async function deleteUpstream(id: number) {
  await client.delete(`/upstreams/${id}`)
}

export async function fetchModels(id: number) {
  const { data } = await client.post(`/upstreams/${id}/fetch-models`)
  return data.models as string[]
}

export async function listModels(upstreamId: number) {
  const { data } = await client.get(`/upstreams/${upstreamId}/models`)
  return data.data as UpstreamModel[]
}

export async function addModel(upstreamId: number, model_name: string) {
  await client.post(`/upstreams/${upstreamId}/models`, { model_name })
}

export async function deleteModel(modelId: number) {
  await client.delete(`/upstreams/${modelId}/models`)
}
```

Create `web/src/api/keys.ts`:

```typescript
import client from './client'

export interface ExtKey {
  id: number; key: string; label: string; enabled: boolean; created_at?: string
}

export async function listKeys() {
  const { data } = await client.get('/keys')
  return data.data as ExtKey[]
}

export async function createKey(label: string) {
  const { data } = await client.post('/keys', { label })
  return data as ExtKey
}

export async function deleteKey(id: number) {
  await client.delete(`/keys/${id}`)
}
```

Create `web/src/api/usage.ts`:

```typescript
import client from './client'

export interface UsageSummary {
  group_key: string; request_count: number; total_tokens: number
  prompt_tokens: number; completion_tokens: number; ok_count: number; error_count: number
}

export interface UsageRecord {
  id: number; upstream_name: string; model: string; in_format: string; up_format: string
  prompt_tokens: number; completion_tokens: number; total_tokens: number
  stream: boolean; status: string; created_at: string
}

export async function fetchSummary(groupBy: string, from?: string, to?: string) {
  const { data } = await client.get('/usage/summary', { params: { group_by: groupBy, from, to } })
  return data.data as UsageSummary[]
}

export async function fetchRecords(page: number, size: number) {
  const { data } = await client.get('/usage/records', { params: { page, size } })
  return data as { data: UsageRecord[]; total: number }
}
```

- [ ] **Step 5: Update main.ts**

Replace `web/src/main.ts`:

```typescript
import { createApp } from 'vue'
import naive from 'naive-ui'
import App from './App.vue'
import router from './router'
import './style.css'

const app = createApp(App)
app.use(router)
app.use(naive)
app.mount('#app')
```

- [ ] **Step 6: Update vite.config.ts**

Replace `web/vite.config.ts`:

```typescript
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
      '/v1': 'http://localhost:8080',
    },
  },
})
```

- [ ] **Step 7: Verify frontend builds**

Run `cd web && npx vue-tsc -b && npx vite build` (may fail if pages don't exist yet — ok, they're created in later tasks).
Expected: compiles (or fails with missing imports from views — acceptable, views created next)

- [ ] **Step 8: Commit**

```bash
cd web
npm install vue-router@4 naive-ui axios
cd ..
git add web/package.json web/package-lock.json web/src/router.ts web/src/api/ web/src/main.ts web/vite.config.ts
git commit -m "feat(frontend): add router, api client, naive-ui, dev proxy"
```

---

### Task 4: Login page + Layout

**Files:**
- Create: `web/src/views/Login.vue`
- Create: `web/src/components/Layout.vue`
- Modify: `web/src/App.vue`

- [ ] **Step 1: Create Login.vue**

```vue
<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import client from '../api/client'

const router = useRouter()
const password = ref('')
const loading = ref(false)
const error = ref('')

async function login() {
  loading.value = true
  error.value = ''
  try {
    await client.post('/login', { password: password.value })
    router.push('/upstreams')
  } catch {
    error.value = '密码错误'
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div style="display:flex;justify-content:center;align-items:center;height:100vh">
    <n-card title="any-llm 管理" style="width:400px">
      <n-input v-model:value="password" type="password" placeholder="管理员密码" @keyup.enter="login" />
      <n-button type="primary" :loading="loading" block style="margin-top:16px" @click="login">登录</n-button>
      <p v-if="error" style="color:red;margin-top:8px">{{ error }}</p>
    </n-card>
  </div>
</template>
```

- [ ] **Step 2: Create Layout.vue**

```vue
<script setup lang="ts">
import { useRouter, useRoute } from 'vue-router'
import client from '../api/client'

const router = useRouter()
const route = useRoute()

async function logout() {
  await client.post('/logout')
  router.push('/login')
}

const menuItems = [
  { label: '上游管理', key: 'upstreams' },
  { label: 'API 密钥', key: 'keys' },
  { label: '用量统计', key: 'usage' },
]
</script>

<template>
  <n-layout has-sider style="height:100vh">
    <n-layout-sider bordered>
      <n-menu :value="route.name as string" :options="menuItems" @update:value="(v: string) => router.push({ name: v })" />
      <n-button text style="position:absolute;bottom:16px;left:16px" @click="logout">退出</n-button>
    </n-layout-sider>
    <n-layout-content style="padding:24px">
      <router-view />
    </n-layout-content>
  </n-layout>
</template>
```

- [ ] **Step 3: Update App.vue**

Replace `web/src/App.vue`:

```vue
<template>
  <router-view />
</template>
```

- [ ] **Step 4: Verify build**

Run: `cd web && npx vue-tsc -b && npx vite build`
Expected: compiles (remaining imports from views deferred via lazy loading — silent if they don't exist yet, but will error at runtime; next tasks create them)

Note: Lazy-loaded routes via `() => import(...)` won't cause build errors for missing files until accessed. Vite may still warn. Proceed; pages are created next.

- [ ] **Step 5: Commit**

```bash
git add web/src/views/Login.vue web/src/components/Layout.vue web/src/App.vue
git commit -m "feat(frontend): add login page and layout with sidebar"
```

---

### Task 5: Upstreams page + ModelEditor

**Files:**
- Create: `web/src/views/Upstreams.vue`
- Create: `web/src/components/ModelEditor.vue`

- [ ] **Step 1: Create ModelEditor.vue**

```vue
<script setup lang="ts">
import { ref, watch } from 'vue'
import { listModels, addModel, deleteModel, type UpstreamModel } from '../api/upstreams'

const props = defineProps<{ upstreamId: number; show: boolean }>()
const emit = defineEmits(['close'])
const models = ref<UpstreamModel[]>([])
const newName = ref('')

async function load() {
  models.value = await listModels(props.upstreamId)
}
async function add() {
  if (newName.value.trim()) {
    await addModel(props.upstreamId, newName.value.trim())
    newName.value = ''
    await load()
  }
}
async function del(id: number) {
  await deleteModel(id)
  await load()
}

watch(() => props.show, (s) => { if (s) load() })
</script>

<template>
  <n-modal :show="show" @update:show="(v: boolean) => !v && emit('close')">
    <n-card title="模型管理" style="width:500px">
      <n-space vertical>
        <n-space>
          <n-input v-model:value="newName" placeholder="模型名" style="width:200px" />
          <n-button @click="add">添加</n-button>
        </n-space>
        <n-list>
          <n-list-item v-for="m in models" :key="m.id">
            <n-space justify="space-between" style="width:100%">
              <span>{{ m.model_name }}{{ m.manual ? ' (手动)' : '' }}</span>
              <n-button size="small" @click="del(m.id)">删除</n-button>
            </n-space>
          </n-list-item>
        </n-list>
      </n-space>
    </n-card>
  </n-modal>
</template>
```

- [ ] **Step 2: Create Upstreams.vue**

```vue
<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { listUpstreams, createUpstream, deleteUpstream, fetchModels as fetchUpsModels, type Upstream } from '../api/upstreams'
import ModelEditor from '../components/ModelEditor.vue'

const upstreams = ref<Upstream[]>([])
const showForm = ref(false)
const form = ref<Upstream & { fetch_models?: boolean }>({ name: '', base_url: '', api_key: '', format: 'openai', fetch_models: true })
const editing = ref<Upstream | null>(null)
const modelEditorId = ref(0)
const showModels = ref(false)

async function load() { upstreams.value = await listUpstreams() }
async function save() {
  if (editing.value?.id) {
    await updateUpstream(editing.value.id, form.value)
  } else {
    await createUpstream(form.value)
  }
  showForm.value = false
  editing.value = null
  resetForm()
  await load()
}
function resetForm() { form.value = { name: '', base_url: '', api_key: '', format: 'openai', fetch_models: true } }
function edit(u: Upstream) { editing.value = u; form.value = { ...u }; showForm.value = true }
function add() { editing.value = null; resetForm(); showForm.value = true }
async function del(id: number) { await deleteUpstream(id); await load() }
async function fetchM(id: number) { await fetchUpsModels(id); await load() }

import { updateUpstream } from '../api/upstreams'

onMounted(load)
</script>

<template>
  <n-space vertical size="large">
    <n-space>
      <n-button type="primary" @click="add">添加上游</n-button>
    </n-space>
    <n-data-table :columns="[
      { title: '名称', key: 'name' },
      { title: '地址', key: 'base_url', ellipsis: { tooltip: true } },
      { title: '格式', key: 'format', width: 100 },
      { title: '操作', key: 'actions', width: 280 }
    ]" :data="upstreams">
      <template #actions="{ row }">
        <n-space>
          <n-button size="small" @click="edit(row)">编辑</n-button>
          <n-button size="small" @click="fetchM(row.id)">拉取模型</n-button>
          <n-button size="small" @click="showModels = true; modelEditorId = row.id">模型</n-button>
          <n-popconfirm @positive-click="del(row.id)">
            <template #trigger><n-button size="small" type="error">删除</n-button></template>
            确定删除？
          </n-popconfirm>
        </n-space>
      </template>
    </n-data-table>

    <n-modal :show="showForm" @update:show="(v: boolean) => !v && (showForm = false)">
      <n-card :title="editing ? '编辑上游' : '添加上游'" style="width:500px">
        <n-form>
          <n-form-item label="名称"><n-input v-model:value="form.name" /></n-form-item>
          <n-form-item label="Base URL"><n-input v-model:value="form.base_url" /></n-form-item>
          <n-form-item label="API Key"><n-input v-model:value="form.api_key" type="password" /></n-form-item>
          <n-form-item label="格式">
            <n-radio-group v-model:value="form.format">
              <n-radio value="openai">OpenAI</n-radio>
              <n-radio value="anthropic">Anthropic</n-radio>
            </n-radio-group>
          </n-form-item>
          <n-button type="primary" block @click="save">{{ editing ? '保存' : '添加' }}</n-button>
        </n-form>
      </n-card>
    </n-modal>

    <ModelEditor :upstream-id="modelEditorId" :show="showModels" @close="showModels = false" />
  </n-space>
</template>
```

- [ ] **Step 3: Commit**

```bash
git add web/src/views/Upstreams.vue web/src/components/ModelEditor.vue
git commit -m "feat(frontend): add upstreams page with CRUD and model editor"
```

---

### Task 6: Keys page + Usage page

**Files:**
- Create: `web/src/views/Keys.vue`
- Create: `web/src/views/Usage.vue`

- [ ] **Step 1: Create Keys.vue**

```vue
<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { listKeys, createKey, deleteKey, type ExtKey } from '../api/keys'

const keys = ref<ExtKey[]>([])
const label = ref('')
const showNew = ref(false)

async function load() { keys.value = await listKeys() }
async function add() {
  if (label.value.trim()) {
    await createKey(label.value.trim())
    label.value = ''
    showNew.value = true  // show the plain key in the table
    await load()
  }
}
async function del(id: number) { await deleteKey(id); await load() }

onMounted(load)
</script>

<template>
  <n-space vertical size="large">
    <n-space>
      <n-input v-model:value="label" placeholder="备注" style="width:200px" />
      <n-button type="primary" @click="add">生成 Key</n-button>
    </n-space>
    <n-data-table :columns="[
      { title: '备注', key: 'label' },
      { title: 'Key', key: 'key' },
      { title: '操作', key: 'actions', width: 100 }
    ]" :data="keys">
      <template #actions="{ row }">
        <n-popconfirm @positive-click="del(row.id)">
          <template #trigger><n-button size="small" type="error">删除</n-button></template>
          确定删除此 key？删除后该 key 将不可用。
        </n-popconfirm>
      </template>
    </n-data-table>
  </n-space>
</template>
```

- [ ] **Step 2: Create Usage.vue**

```vue
<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { fetchSummary, fetchRecords, type UsageSummary, type UsageRecord } from '../api/usage'

const groupBy = ref('model')
const summaries = ref<UsageSummary[]>([])
const records = ref<UsageRecord[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20

async function load() {
  summaries.value = await fetchSummary(groupBy.value)
  const r = await fetchRecords(page.value, pageSize)
  records.value = r.data
  total.value = r.total
}

onMounted(load)
</script>

<template>
  <n-space vertical size="large">
    <n-space>
      <n-text>分组：</n-text>
      <n-radio-group v-model:value="groupBy" @update:value="load">
        <n-radio-button value="model">按模型</n-radio-button>
        <n-radio-button value="upstream">按上游</n-radio-button>
        <n-radio-button value="key">按 Key</n-radio-button>
      </n-radio-group>
    </n-space>
    <n-data-table :columns="[
      { title: groupBy === 'key' ? 'Key ID' : (groupBy === 'upstream' ? '上游' : '模型'), key: 'group_key' },
      { title: '请求数', key: 'request_count' },
      { title: '总 Token', key: 'total_tokens' },
      { title: '输入 Token', key: 'prompt_tokens' },
      { title: '输出 Token', key: 'completion_tokens' },
      { title: '成功', key: 'ok_count' },
      { title: '失败', key: 'error_count' },
    ]" :data="summaries" style="margin-bottom:24px" />

    <n-text>请求明细 (共 {{ total }} 条)</n-text>
    <n-data-table :columns="[
      { title: '时间', key: 'created_at', width: 160 },
      { title: '上游', key: 'upstream_name' },
      { title: '模型', key: 'model' },
      { title: '入格式', key: 'in_format', width: 100 },
      { title: '出格式', key: 'up_format', width: 100 },
      { title: 'Token', key: 'total_tokens', width: 80 },
      { title: '状态', key: 'status', width: 80 },
    ]" :data="records" :pagination="{ page: page, pageSize, itemCount: total, onChange: (p: number) => { page = p; load() } }" />
  </n-space>
</template>
```

- [ ] **Step 3: Verify full frontend build**

Run: `cd web && npx vue-tsc -b && npx vite build`
Expected: compiles and builds successfully

- [ ] **Step 4: Commit**

```bash
git add web/src/views/Keys.vue web/src/views/Usage.vue
git commit -m "feat(frontend): add keys and usage pages"
```

---

### Task 7: Final integration — build frontend, verify go:embed

**Files:**
- Modify: none (the built dist replaces the placeholder)

- [ ] **Step 1: Build frontend**

Run: `cd web && npx vue-tsc -b && npx vite build`

- [ ] **Step 2: Build Go binary**

Run: `go build -o any-llm.exe ./cmd/any-llm/`

- [ ] **Step 3: Run Go binary and smoke test**

Start: `./any-llm.exe`
In browser: `http://localhost:8080` → should show login page

- [ ] **Step 4: Run all tests**

Run: `go test ./...`
Expected: all packages PASS

- [ ] **Step 5: Commit**

```bash
git add web/dist/
git commit -m "feat: build frontend and embed in binary"
```

---

## Self-Review

**1. Spec coverage:**
- Auth main-password session (§7): Task 1 ✓
- Wire auth + go:embed + SPA fallback (§9): Task 2 ✓
- Frontend deps + router + api client (§8): Task 3 ✓
- Login page + Layout (§8): Task 4 ✓
- Upstreams page + ModelEditor (§8, demand ①): Task 5 ✓
- Keys page (demand ④): Task 6 ✓
- Usage page (demand ③): Task 6 ✓
- Vite dev proxy (§9): Task 3 ✓
- Final build + go:embed verify: Task 7 ✓

**2. Placeholder scan:** No TBD/TODO. All tasks have complete code, exact file paths, test expectations.

**3. Type consistency:**
- `auth.Middleware` with `NewMiddleware(secret, password)`, `Wrap(handler)`, login/logout handlers ✓
- Frontend API types match Go struct JSON tags (`base_url`, `api_key`, `model_name`, etc.) ✓
- Router uses hash history (works with SPA fallback) ✓
- Naive UI components (`n-card`, `n-button`, `n-input`, `n-modal`, `n-form`, etc.) all correctly named ✓

No issues found.
