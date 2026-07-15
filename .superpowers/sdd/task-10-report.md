### Task 10: Gateway router — auth + routing + models endpoint

**Status:** Complete

**Files created:**
- `internal/gateway/router.go` — Gateway struct with New(), ServeHTTP, handleModels, handleCompletion, dispatch (stub)
- `internal/gateway/router_test.go` — 5 tests covering models endpoint, auth, and routing

**Tests (all passing):**
| Test | What it verifies |
|---|---|
| TestModelsEndpoint | GET /v1/models returns correct model IDs in "name/model" format |
| TestAuthMissingKey | POST without key returns 401 |
| TestAuthInvalidKey | POST with invalid key (right prefix, not in DB) returns 401 |
| TestRouteModelNotFound | Valid key, nonexistent upstream → 404 |
| TestRouteInvalidModelFormat | Valid key, model missing "/" separator → 400 |

**Implementation details:**
- `Gateway` struct holds `*sql.DB` and `*upstream.Client`
- `ServeHTTP` routes: GET /v1/models → handleModels, POST /v1/chat/completions → openai, POST /v1/messages → anthropic
- `handleModels`: iterates upstreams + models, emits `{"object":"list","data":[...]}` with `id` in "name/model" format
- `handleCompletion`: extractKey (Bearer/x-api-key), IsValidKeyFormat, GetExtKey + Touch, readBody, parse model JSON, splitModel, GetUpstreamByName, dispatch (501 stub for Task 11)
- `extractKey`: checks Authorization: Bearer prefix and x-api-key header
- `splitModel`: splits on first "/" with bounds checks
- `readBody`/`readAll`: wraps `io.ReadAll`
- `dispatch` stub returns 501
