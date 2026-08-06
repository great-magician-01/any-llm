# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

any-llm is a single-binary LLM API gateway. It exposes OpenAI-compatible (`/v1/chat/completions`) and Anthropic-compatible (`/v1/messages`) endpoints, routes to multiple upstream providers (also OpenAI/Anthropic/Responses format), and translates between any inbound/upstream format pair through a normalized intermediate representation (IR). It embeds a Vue 3 admin SPA and records per-request token usage. Backend is stdlib `net/http` only — no framework. `AGENTS.md` holds the full env-var table, route list, and operational details; read it for anything not covered here.

## Commands

```bash
# Backend tests
go test ./...                          # all
go test ./internal/translate/cross_test.go -run Cross -v   # single package/file tests
go vet ./...                           # lint is gofmt + go vet only (checked in CI)

# Frontend
cd web && npm run dev                  # Vite dev server, proxies /api and /v1 to :6718
cd web && npm run test                 # vitest (router auth guard, axios 401 interceptor)
cd web && npm run build                # vue-tsc typecheck + vite build + copy dist to ../cmd/any-llm/web/dist

# Build the binary (frontend dist MUST exist first — it is embedded)
go build -o any-llm ./cmd/any-llm/
go run ./cmd/any-llm/                  # dev server on :6718
```

## Architecture

### The IR translation core (the heart of the project)

`internal/translate/ir.go` defines format-agnostic types: `Request`, `Response`, `StreamEvent`, `ContentBlock` (a discriminated union: text / image / tool_use / tool_result / thinking / redacted_thinking), `Tool`, `ToolChoice`, `Usage`. `StopReason` uses a canonical IR vocabulary (`stop` / `max_tokens` / `tool_calls` / `content_filter`).

Each format is a sibling package (`internal/translate/openai`, `.../anthropic`, `.../responses`) exposing the same shape: `DecodeRequest`/`EncodeRequest`, `DecodeResponse`/`EncodeResponse`, and stream decoders/encoders. A request round-trips as:

```
inbound format → DecodeRequest → IR Request → EncodeRequest(upstream format) → upstream
upstream response → DecodeResponse → IR Response → EncodeResponse(inbound format) → client
```

**Streaming normalizes everything to Anthropic-style fine-grained `StreamEvent`s** (`message_start`, `content_block_start`, `content_block_delta`, `content_block_stop`, `message_delta`, `message_stop`). Upstream SSE lines are parsed per-format (see `upstream.Client.streamLoop`), pushed as IR events, then re-encoded to the client's format. Token usage is aggregated from `message_start`/`message_delta` events — including Anthropic cache tokens and OpenAI reasoning tokens — and made available via `Result.Usage()`.

To add a new format (e.g. Google): create a new package under `internal/translate/`, implement the decode/encode pair, and wire it into `upstream/client.go` (`Call`) and `gateway/handler_openai.go` (`decodeInbound`, the response encoders, and the stream encoder switch). Cross-format equivalence is tested in `internal/translate/cross_test.go` and `internal/gateway/cross_stream_test.go`.

### Gateway request lifecycle

`internal/gateway/router.go` routes the three public endpoints (`/v1/models`, `/v1/chat/completions`, `/v1/messages`, `/v1/responses`). `handler_openai.go` implements the pipeline:

1. **Ext-key auth** — `Authorization: Bearer all-sk-...` or `x-api-key` header; validated against DB and `Enabled` flag; `last_used_at` touched via async writer.
2. **Model split** — request model is `upstream-name/model-name`, split on the first `/`; unknown upstream → 404.
3. **Token-limit check** — daily/monthly quotas on both the ext key and the upstream (aggregated from `usage_records` over local-day/local-month windows; `0` = unbounded), exceeded → 429.
4. **Dispatch** — decode inbound → IR, call upstream, encode response back. Errors are written in the *client's* format (`WriteError`/`mapErrorType` in `errors.go`).

Every request ends in `recordUsage` (via the async writer, so it never blocks the response). Upstream errors carry their status through as `upstream.UpstreamError` and the gateway maps status → the client format's error type.

### Streaming specifics (handler_openai.go)

- The response header is flushed before the upstream call completes; a 500ms ticker sends keep-alive pings (`: kp` for OpenAI, `ping` events for Anthropic) until the call returns.
- Anthropic clients abort if they receive a `content_block_delta` for an index that never had `content_block_start` — the gateway synthesizes missing `content_block_start` events for upstreams like DeepSeek that omit them.
- If the upstream answers a stream request with a non-stream JSON body, it is forwarded as a complete response (`result.Response != nil` branch).
- On stream end, `encoder.Flush()` emits trailing frames (e.g. Responses `response.completed`).

### DB layer

- **`db.Writer`** (`internal/db/writer.go`) serializes *all* DB writes through a single goroutine with a buffered channel (capacity 512). `DoAsync` is fire-and-forget (drops on full buffer / after `Stop`); `DoSync` blocks and returns `ErrWriterStopped` (mapped to HTTP 503) during shutdown. Usage records, key touches, and admin writes go through it; read queries hit the `*sql.DB` directly. This means no two goroutines write concurrently — mirror this for new writes.
- **Dialect abstraction** (`internal/db/db.go`): queries are written with `?` placeholders and passed through `db.Rebind(d, q)` which rewrites them to `$N` for PostgreSQL (skipping string literals and comments). Migrations are split into `migrationSQLite` / `migrationPG`; SQLite uses `INTEGER PRIMARY KEY AUTOINCREMENT` + `DATETIME`, PG uses `BIGSERIAL` + `TIMESTAMP(0)`. `db.DialectOf` infers the dialect from the driver — never store dialect as global state.
- Backend tests use in-memory SQLite via `t.TempDir()`; PG behavior is covered in `pg_e2e_test.go` (skipped unless a PG test server is configured).

### Auth — two independent schemes

- **Admin** (`internal/auth`): password login → HMAC-SHA256 session cookie `s` (24h expiry, path-scoped to `/api/admin`). Session secret is auto-generated and persisted to `ANY_LLM_SESSION_SECRET_FILE` (default `./.session-secret`) so sessions survive restarts.
- **Public gateway**: stateless ext keys, `all-sk-` + 32 base62 chars, stored in DB.

### Server wiring (cmd/any-llm/main.go)

One `http.ServeMux` serves three traffic classes: `/v1/*` (gateway), `/api/admin/*` (admin API wrapped in auth middleware), `/*` (embedded SPA, with `assets/` cached immutable, everything else no-cache). Graceful shutdown: 30s drain via `signal.NotifyContext`, no `WriteTimeout` because SSE streams are long-lived. `//go:embed all:web/dist` requires `cmd/any-llm/web/dist/` to exist at compile time — `npm run build` copies it there; CI stubs it.

### Frontend (web/)

Vue 3 + TypeScript + Naive UI + Vue Router (hash history) + Axios. `src/views/` holds one page per feature (Dashboard, Upstreams, Keys, Usage, Login); `src/api/` has typed API modules with a shared axios instance whose response interceptor redirects to login on 401. Vite dev server proxies `/api` and `/v1` to `localhost:6718`.

## Gotchas

- **The upstream call for streams runs in a goroutine while keep-alives are written** — the first-phase loop in `handleStream` waits for the call result *or* client disconnect; the client context drives cancellation of the upstream call.
- **Responses-format sessions** (`gateway/session.go`): `previous_response_id` threads conversation history stored in the `response_sessions` table (24h idle TTL). The response `id` returned to the client must equal the session key, or continuation fails with 400 — non-stream Responses responses get their ID re-stamped (`result.Response.ID = sess.respID`).
- Client request headers (except hop-by-hop and auth ones) are forwarded verbatim to the upstream (`upstream.copyForwardableHeaders`) — e.g. `anthropic-beta` or trace headers.
- Always injects `stream_options: {include_usage: true}` into OpenAI upstream requests; never use `CURRENT_TIMESTAMP` in SQLite comparisons where sub-second precision matters (see the comment in `session.go Put`).
- No rate limiting and no CORS middleware — don't assume they exist.
