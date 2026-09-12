# AGENTS.md

## Architecture

- **Monorepo**: Go backend (root, module `github.com/great-magician-01/any-llm`) + Vue 3 SPA frontend (`web/`)
- **Backend**: single-binary Go app with embedded frontend via `//go:embed web/dist` (relative to `cmd/any-llm/`)
- **Frontend**: Vue 3 + Naive UI + Vue Router (hash history) + Axios, built with Vite. Two parallel page sets: classic dark theme (`web/src/views/`, routes `/login`, `/dashboard`, ...) and a glassmorphism set (`web/src/glass/`, same pages under the `/glass` route prefix, e.g. `/glass/dashboard`) — glass views reuse `web/src/api`, `web/src/utils` and shared components (`StatCard`, `BarChart`, ...), adapting via CSS-var overrides in `web/src/glass/glass.css` + a nested `n-config-provider` in `web/src/glass/GlassShell.vue`; `body.glass-mode` (toggled by the router) styles teleported popups
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
| `ANY_LLM_SESSION_TTL` | `24h` | admin login session expiry; Go duration (`24h`, `168h`) or plain hours (`24`); `0` = never expire; sliding — the cookie is re-issued with a fresh full TTL once less than half remains, so active users are not logged out |
| `ANY_LLM_BALANCE_INTERVAL` | `10m` | vendor balance/quota snapshot polling interval; Go duration (`10m`, `30m`) or plain hours; `0` = disable all automatic fetching, including the boot-time snapshot (manual refresh still works) |
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
  - Model format in request body: `upstream-name/model-name`, **or a model alias** (fixed external name): the full model string is exact-matched against active aliases first (aliases take precedence, even names containing `/`); an alias resolves to an ordered binding list of `upstream + real model` that is tried in `priority` order with automatic failover (disabled or upstream-limit-exceeded candidates are skipped before dispatch; once stream events flow, no failover). Usage records store the **actual** upstream model per attempt.
  - Per-key model allowlist: each ext key can be restricted to specific public model names — alias names or `upstream/model`, exactly the ids `/v1/models` lists (`allowed_models` on the key, admin `POST/PUT /api/admin/keys`; empty = unrestricted, exact match only, entries need not exist yet). A request for an unlisted model is rejected 403 `permission_error`; `/v1/models` itself requires an ext key and lists only that key's allowed models.
  - Upstreams can be **disabled** via admin (`PUT /api/admin/upstreams/:id` with `{"enabled": false}`; the same optional field on create pre-disables a new upstream) — a lighter alternative to delete: alias bindings skip disabled upstreams (failover kicks in, all-disabled aliases 404 "has no available bindings"), direct `name/model` requests get 404 "upstream 'x' is disabled", and `/v1/models` omits them. Unlike delete, models and alias bindings are preserved so re-enabling restores service.
- `/api/admin/*` — admin API (HMAC session auth, cookie `s`)
  - CRUD for upstreams, models, ext keys, model aliases (`/api/admin/aliases`); usage summary/records. Ext keys carry a display name in the `label` column (required by the UI) plus a free-form `remark`; `GET /api/admin/usage/summary?group_by=key` reports the joined key `label` as `group_key` — falling back to `#<id>` for an empty label or a missing key row, then `—` when the record has no key id — while still grouping by `ext_key_id`, so two same-labelled keys stay separate rows and a renamed or soft-deleted key shows its current name (no name snapshot in `usage_records`)
  - `GET /api/admin/balances` — latest vendor balance/quota snapshot per upstream; `POST /api/admin/balances` — live-fetch + archive all supported, enabled upstreams (page-open auto-refresh); `GET /api/admin/upstreams/:id/balances[?page=&size=]` — paginated snapshot history; `POST /api/admin/upstreams/:id/balances/refresh` — live fetch + archive (400 for unsupported vendors, 502 on vendor API failure). Snapshots live in `balance_snapshots`; a background poller (`internal/upstream/balance_poller.go`, interval `ANY_LLM_BALANCE_INTERVAL`, plus one run at boot when the interval is > 0) archives every supported, enabled upstream. Vendor is identified by base-URL host: `api.deepseek.com` → `GET /user/balance`, `api.kimi.com` → `GET /coding/v1/usages`; payloads are normalized JSON (`{"kind":"balance",...}` / `{"kind":"quota",...}`)
  - `GET /api/admin/conversations[?page=&size=]` and `/api/admin/conversations/:id` — read-only access to archived conversations (**PG only**); on SQLite the list returns `{"data": [], "total": 0, "disabled": true}` so the frontend can show a hint. Responses never include the raw byte columns. Storage is **app-level monthly sharding** (`internal/model/conversation_shard.go`, design doc `docs/conversation-sharding.md`): writes route by `created_at` to `conversation_records_YYYY_MM` (auto-created via `model.EnsureConversationShard` on startup and on insert-miss); a pre-existing plain `conversation_records` table is kept untouched as the legacy shard. All shards share the sequence `conversation_records_id_seq` so `id` stays globally unique; a process-global registry cache (`model.LoadConvShards` at boot, refreshed on every shard creation) feeds reads — lists paginate shard-by-shard (newest month first, legacy last) via per-shard COUNT + window math, detail fans out `WHERE id=?` per shard
  - `GET /api/admin/config/export` — config backup: all upstreams (real API keys, models with lengths, enabled/limits) + model aliases with bindings addressed **by upstream name** (instance-stable); `POST /api/admin/config/import` — restore: same-name upstreams/aliases are overwritten (model list replaced exactly, bindings replaced), configs absent from the file are kept. `enabled`/token limits/`models` omitted from the file keep current values (base_url/api_key/format always follow the file); the whole file is validated before any write, duplicate names within a file are rejected 400; bindings referencing upstreams that exist neither in the file nor in the DB are dropped (`bindings_dropped` count; aliases left with none are skipped). Import is **not one transaction**: a mid-way DB failure leaves already-committed items in place — re-importing the same file is safe and converges (same-name overwrite is idempotent). Response `{"upstreams_created":…,"upstreams_updated":…,"aliases_created":…,"aliases_updated":…,"aliases_skipped":…,"bindings_dropped":…}`. Payload format `version` 1, `>1` rejected. UI lives on the Upstreams page (both classic and glass)
- `/*` — SPA fallback (serves embedded `web/dist/`; falls back to `index.html` for client-side routing)

## Gotchas

- **Graceful shutdown**: `main.go` uses `signal.NotifyContext` + `http.Server.Shutdown` (30s drain, then force-close). Deferred cleanup runs LIFO: `poller.Stop` (stops balance snapshot polling) → `writer.Stop` (drains queued usage writes) → `db.Close` → `logger.Close`. Server sets `ReadHeaderTimeout` (10s) but **no WriteTimeout** — SSE streams are long-lived.
- **`cmd/any-llm/web/dist/` must exist** when compiling the Go binary (`//go:embed web/dist`) — `npm run build` copies it there; CI stubs it with an empty `index.html`
- **Ext key format**: `all-sk-` prefix + 32 base62 chars
- **Session auth**: HMAC-SHA256, expiry from `ANY_LLM_SESSION_TTL` (default 24h, `0` = never — signed as a year-9999 expiry so verification needs no special case); sliding renewal — `authenticate` re-issues the cookie with a fresh full TTL when less than half remains; secret persisted in `ANY_LLM_SESSION_SECRET_FILE` (default `./.session-secret`, gitignored) when env unset
- **DB writer**: `internal/db.Writer` serializes all writes through a single goroutine (buffered channel, capacity 512). `DoAsync` is fire-and-forget (silently drops on full buffer / after Stop); `DoSync` blocks for the result and returns `ErrWriterStopped` (mapped to HTTP 503) if the server is shutting down. `Stop` waits for in-flight sync calls to finish before draining and exiting, so concurrent shutdown cannot deadlock or orphan in-flight admin writes.
- **Dialect abstraction**: `db.Rebind(d, q)` rewrites `?` placeholders to `$N` for PostgreSQL, inferred from the `*sql.DB` driver (no global state). String literals (`'...'`, with `''` escape) and SQL comments (`--`, `/* */`) are skipped so `?` inside them is preserved. Migrations are split into `migrationSQLite` / `migrationPG`; PG uses `BIGSERIAL` + `TIMESTAMP(0)`, SQLite uses `INTEGER PRIMARY KEY AUTOINCREMENT` + `DATETIME`. Auto-upgrade runs in a fixed order: main script → `migrateExtraCols` (add missing columns) → `migrateSoftDelete` (SQLite table rebuilds; its specs duplicate the `CREATE TABLE`s and must be kept in sync — an added column must go into `extraCols` *and* the rebuild spec, otherwise the rebuild drops it until the next restart).
- **Timestamp convention**: always write Go `time.Time` values in server-local time (`time.Now()`), never SQL `CURRENT_TIMESTAMP` defaults or `.UTC()`. PG `TIMESTAMP(0)` columns store the bare local wall clock (pgx discards the zone on encode); `OpenPG` registers a custom `timestamp` codec (`internal/db/pgtime.go`) that relabels scanned values from UTC to `time.Local`, so JSON serializes with the local offset and frontends convert correctly. SQLite stores RFC3339 text with the local offset and round-trips correctly on its own.
- **Streaming**: always injects `stream_options: {include_usage: true}` into OpenAI upstream requests
- **No rate limiting, no CORS middleware**
