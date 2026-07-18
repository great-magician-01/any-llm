# AGENTS.md

## Architecture

- **Monorepo**: Go backend (root, module `github.com/great-magician-01/any-llm`) + Vue 3 SPA frontend (`web/`)
- **Backend**: single-binary Go app with embedded frontend via `//go:embed web/dist` (relative to `cmd/any-llm/`)
- **Frontend**: Vue 3 + Naive UI + Vue Router (hash history) + Axios, built with Vite
- **DB**: SQLite via `modernc.org/sqlite` (pure Go, no CGO), tables auto-created on startup via `db.Open`
- **No frameworks** on backend: stdlib `net/http` only
- **Translation layer**: requests flow through an IR (`internal/translate/`) — OpenAI/Anthropic in/out, any upstream format
- **10 internal packages**: `auth`, `config`, `db`, `gateway`, `logger`, `model`, `translate`, `upstream`, `usage`, `webapi`

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
- **No frontend tests exist** (no vitest/jest configured)
- **No lint/formatter config, no CI**

## Config (env vars)

All settings load from environment variables. A `.env` file in the working directory is loaded on startup but does **not** override existing env vars.

| Variable | Default | Notes |
|----------|---------|-------|
| `ANY_LLM_HOST` | `0.0.0.0` | |
| `ANY_LLM_PORT` | `6718` | `.env.example` shows `8080` but code default is `6718` |
| `ANY_LLM_DB_PATH` | `./any-llm.db` | |
| `ANY_LLM_MASTER_PASSWORD` | `admin` | warns on default at startup |
| `ANY_LLM_SESSION_SECRET` | auto-gen | **sessions lost on restart unless set**; use 64-char hex |
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
- `/*` — SPA fallback (serves embedded `web/dist/`; falls back to `index.html` for client-side routing)

## Gotchas

- **`.gitignore` is minimal** (only `.env` and `logs/`) — `any-llm.db`, `any-llm.exe`, `web/dist/`, and `web/node_modules/` are all committed
- **`web/dist/` must exist somewhere** when compiling the Go binary — `npm run build` handles this by copying into `cmd/any-llm/web/dist/`
- **Ext key format**: `all-sk-` prefix + 32 base62 chars
- **Session auth**: HMAC-SHA256, 24h expiry; secret changes on restart unless `ANY_LLM_SESSION_SECRET` is set
- **Usage recorder**: async buffered channel (capacity 256); silently drops records on overflow
- **Streaming**: always injects `stream_options: {include_usage: true}` into OpenAI upstream requests
- **No rate limiting, no CORS middleware**
