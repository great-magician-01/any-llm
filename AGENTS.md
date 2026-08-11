# AGENTS.md

## Architecture

- **Monorepo**: Go backend (root, module `github.com/great-magician-01/any-llm`) + Vue 3 SPA frontend (`web/`)
- **Backend**: single-binary Go app with embedded frontend via `//go:embed web/dist` (relative to `cmd/any-llm/`)
- **Frontend**: Vue 3 + Naive UI + Vue Router (hash history) + Axios, built with Vite
- **DB**: SQLite (`modernc.org/sqlite`, pure Go, no CGO) or PostgreSQL (`jackc/pgx/v5`); selected via `DB_TYPE`. Tables auto-created on startup via `db.OpenSQLite` / `db.OpenPG`
- **No frameworks** on backend: stdlib `net/http` only
- **Translation layer**: requests flow through an IR (`internal/translate/`) — OpenAI/Anthropic in/out, any upstream format
- **9 internal packages**: `auth`, `config`, `db`, `gateway`, `logger`, `model`, `translate`, `upstream`, `webapi`

## Build & Run

```bash
# Full build (frontend must be built first — it auto-copies dist into cmd/any-llm/web/dist/ for embedding)
cd web && npm run build && cd ..
go build -o any-llm.exe ./cmd/any-llm/

# Docker build (multi-stage: node->golang->alpine)
docker build -t any-llm .

# Dev (two terminals)
go run ./cmd/any-llm/                    # terminal 1: backend on :6718 (default)
cd web && npm run dev                    # terminal 2: Vite HMR, proxies to :6718
```

`npm run build` runs `vue-tsc -b && vite build && <copy dist to ../cmd/any-llm/web/dist>`.

## Testing

```bash
go test ./...                            # all Go tests
go test ./internal/gateway -v            # single package with verbose
```

- Backend tests use stdlib `testing`, in-memory SQLite via `t.TempDir()`
- Frontend tests: vitest + happy-dom (`cd web && npm run test`), covering the router auth guard and the axios 401 interceptor
- **CI**: `.github/workflows/ci.yml` runs gofmt check, `go vet`, `go test`, `go build` (with a stub `cmd/any-llm/web/dist/`), plus `npm run test` and `npm run build`
- No golangci-lint/staticcheck config — linting is gofmt + go vet only

## Config (env vars)

All settings load from environment variables. A `.env` file in the working directory is loaded on startup but does **not** override existing env vars.

| Variable | Default | Notes |
|----------|---------|-------|
| `ANY_LLM_HOST` | `0.0.0.0` | |
| `ANY_LLM_PORT` | `6718` | |
| `DB_TYPE` | `sqlite` | `sqlite` / `postgres` / `postgresql` / `pg` (case-insensitive) |
| `ANY_LLM_DB_PATH` | `./any-llm.db` | SQLite path (used when `DB_TYPE=sqlite`) |
| `DB_HOST` | `localhost` | PostgreSQL host |
| `DB_PORT` | `5432` | PostgreSQL port |
| `DB_USER` | `postgres` | PostgreSQL user |
| `DB_PASSWORD` | (empty) | PostgreSQL password |
| `DB_NAME` | `amanuensis` | PostgreSQL database |
| `DB_SCHEMA` | `public` | PostgreSQL schema (created if missing; validated as identifier) |
| `ANY_LLM_MASTER_PASSWORD` | `admin` | warns on default at startup |
| `ANY_LLM_SESSION_SECRET` | auto-gen | if unset, a random secret is generated and persisted to `ANY_LLM_SESSION_SECRET_FILE` so sessions survive restarts; falls back to ephemeral (with warning) if the file is unwritable |
| `ANY_LLM_SESSION_SECRET_FILE` | `./.session-secret` | where the auto-generated session secret is persisted (0600); only used when `ANY_LLM_SESSION_SECRET` is unset |
| `ANY_LLM_SESSION_TTL` | `24h` | admin login session expiry; Go duration (`24h`, `168h`) or plain hours (`24`); `0` = never expire |
| `ANY_LLM_LOG_FILE` | `./logs/any-llm.log` | empty string disables file logging |
| `ANY_LLM_LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |

`.env` is gitignored. See `.env.example` for the template.

## Logging

- `internal/logger` wraps stdlib `log/slog` (text handler), writing to **both** stdout and the configured log file (append mode, parent dirs auto-created)
- All HTTP requests logged via `gateway.LoggingMiddleware` (method, path, status, size, duration, remote)
- Gateway completion requests additionally log: key id/label, upstream, model, in_format, stream flag, token usage, status
- Upstream errors are logged with status code and truncated response body (≤512 chars)

## Routes

- `/v1/*` — public gateway (ext-key auth via `Authorization: Bearer all-sk-...`)
  - `GET /v1/models`, `POST /v1/chat/completions` (OpenAI), `POST /v1/messages` (Anthropic)
  - Model format in request body: `upstream-name/model-name`
- `/api/admin/*` — admin API (HMAC session auth, cookie `s`)
  - CRUD for upstreams, models, ext keys; usage summary/records
  - `GET /api/admin/conversations[?page=&size=]` and `/api/admin/conversations/:id` — read-only access to archived conversations (`conversation_records`, **PG only**); on SQLite the list returns `{"data": [], "total": 0, "disabled": true}` so the frontend can show a hint. Responses never include the raw byte columns
- `/*` — SPA fallback (serves embedded `web/dist/`; falls back to `index.html` for client-side routing)

## Gotchas

- **Graceful shutdown**: `main.go` uses `signal.NotifyContext` + `http.Server.Shutdown` (30s drain, then force-close). Deferred cleanup runs LIFO: `writer.Stop` (drains queued usage writes) → `db.Close` → `logger.Close`. Server sets `ReadHeaderTimeout` (10s) but **no WriteTimeout** — SSE streams are long-lived.
- **`cmd/any-llm/web/dist/` must exist** when compiling the Go binary (`//go:embed web/dist`) — `npm run build` copies it there; CI stubs it with an empty `index.html`
- **Ext key format**: `all-sk-` prefix + 32 base62 chars
- **Session auth**: HMAC-SHA256, expiry from `ANY_LLM_SESSION_TTL` (default 24h, `0` = never — signed as a year-9999 expiry so verification needs no special case); secret persisted in `ANY_LLM_SESSION_SECRET_FILE` (default `./.session-secret`, gitignored) when env unset
- **DB writer**: `internal/db.Writer` serializes all writes through a single goroutine (buffered channel, capacity 512). `DoAsync` is fire-and-forget (silently drops on full buffer / after Stop); `DoSync` blocks for the result and returns `ErrWriterStopped` (mapped to HTTP 503) if the server is shutting down. `Stop` waits for in-flight sync calls to finish before draining and exiting, so concurrent shutdown cannot deadlock or orphan in-flight admin writes.
- **Dialect abstraction**: `db.Rebind(d, q)` rewrites `?` placeholders to `$N` for PostgreSQL, inferred from the `*sql.DB` driver (no global state). String literals (`'...'`, with `''` escape) and SQL comments (`--`, `/* */`) are skipped so `?` inside them is preserved. Migrations are split into `migrationSQLite` / `migrationPG`; PG uses `BIGSERIAL` + `TIMESTAMP(0)`, SQLite uses `INTEGER PRIMARY KEY AUTOINCREMENT` + `DATETIME`.
- **Streaming**: always injects `stream_options: {include_usage: true}` into OpenAI upstream requests
- **No rate limiting, no CORS middleware**
