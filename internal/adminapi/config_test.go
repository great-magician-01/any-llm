package adminapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/great-magician-01/any-llm/internal/store"
)

// configExport mirrors the /api/admin/config/export payload: upstreams with
// their real (unmasked) API keys and model lists, plus model aliases with
// bindings addressed by upstream name (instance-stable across export/import).
type configExport struct {
	Version    int              `json:"version"`
	ExportedAt string           `json:"exported_at"`
	Upstreams  []map[string]any `json:"upstreams"`
	Aliases    []map[string]any `json:"aliases"`
}

func postConfig(t *testing.T, a *API, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", path, bytes.NewReader(b))
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	return w
}

func getConfig(t *testing.T, a *API, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	return w
}

// TestExportConfig verifies the export payload: full upstream rows with the
// REAL api_key (a config backup must be restorable), model lists, enabled
// state, and aliases whose bindings reference upstreams by name.
func TestExportConfig(t *testing.T) {
	a, d := setupAPI(t)
	id1, _ := store.CreateUpstream(d, &store.Upstream{Name: "openai", BaseURL: "https://api.openai.com", APIKey: "sk-real-secret-key-123", Format: "openai", DailyTokenLimit: 1000, MonthlyTokenLimit: 20000})
	store.AddModel(d, id1, store.UpstreamModel{ModelName: "gpt-4o", ContextLength: 128000, MaxOutputLength: 16384, Multimodal: true})
	id2, _ := store.CreateUpstream(d, &store.Upstream{Name: "deepseek", BaseURL: "https://api.deepseek.com", APIKey: "sk-another-key", Format: "anthropic"})
	u2, _ := store.GetUpstreamByID(d, id2)
	u2.Enabled = false
	store.UpdateUpstream(d, u2)
	store.CreateAlias(d, &store.ModelAlias{Name: "gpt4", Bindings: []store.AliasBinding{
		{UpstreamID: id1, ModelName: "gpt-4o"},
		{UpstreamID: id2, ModelName: "deepseek-chat"},
	}})

	w := getConfig(t, a, "/api/admin/config/export")
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var out configExport
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v body=%s", err, w.Body.String())
	}
	if out.Version != 1 {
		t.Fatalf("version=%d", out.Version)
	}
	if out.ExportedAt == "" {
		t.Fatal("exported_at empty")
	}
	if len(out.Upstreams) != 2 {
		t.Fatalf("upstreams=%d", len(out.Upstreams))
	}
	u1 := out.Upstreams[0]
	if u1["name"] != "openai" || u1["base_url"] != "https://api.openai.com" || u1["format"] != "openai" {
		t.Fatalf("u1=%v", u1)
	}
	if u1["api_key"] != "sk-real-secret-key-123" {
		t.Fatalf("api_key must be the real key, got %v", u1["api_key"])
	}
	if u1["enabled"] != true || u1["daily_token_limit"] != float64(1000) || u1["monthly_token_limit"] != float64(20000) {
		t.Fatalf("u1 flags/limits=%v", u1)
	}
	models1, _ := u1["models"].([]any)
	if len(models1) != 1 {
		t.Fatalf("u1 models=%v", u1["models"])
	}
	m := models1[0].(map[string]any)
	if m["model_name"] != "gpt-4o" || m["context_length"] != float64(128000) || m["max_output_length"] != float64(16384) || m["multimodal"] != true {
		t.Fatalf("u1 model=%v", m)
	}
	u2out := out.Upstreams[1]
	if u2out["enabled"] != false {
		t.Fatalf("u2 enabled=%v", u2out["enabled"])
	}
	if len(out.Aliases) != 1 {
		t.Fatalf("aliases=%d", len(out.Aliases))
	}
	al := out.Aliases[0]
	if al["name"] != "gpt4" {
		t.Fatalf("alias=%v", al)
	}
	bindings, _ := al["bindings"].([]any)
	if len(bindings) != 2 {
		t.Fatalf("bindings=%v", al["bindings"])
	}
	b0 := bindings[0].(map[string]any)
	if b0["upstream"] != "openai" || b0["model_name"] != "gpt-4o" {
		t.Fatalf("binding0=%v", b0)
	}
	b1 := bindings[1].(map[string]any)
	if b1["upstream"] != "deepseek" || b1["model_name"] != "deepseek-chat" {
		t.Fatalf("binding1=%v", b1)
	}
}

// TestExportConfig_EmptyDb verifies export on a fresh database returns an
// empty (but well-formed) config rather than an error.
func TestExportConfig_EmptyDb(t *testing.T) {
	a, _ := setupAPI(t)
	w := getConfig(t, a, "/api/admin/config/export")
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var out configExport
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Upstreams) != 0 || len(out.Aliases) != 0 {
		t.Fatalf("expected empty config, got %+v", out)
	}
}

// TestImportConfig_CreatesAndOverwrites pins the core semantics: same-name
// upstreams/aliases are overwritten (all fields + exact model list / binding
// list), new names are created, and configs NOT present in the file survive
// untouched.
func TestImportConfig_CreatesAndOverwrites(t *testing.T) {
	a, d := setupAPI(t)
	keepID, _ := store.CreateUpstream(d, &store.Upstream{Name: "keep", BaseURL: "https://keep.example.com", APIKey: "sk-keep", Format: "openai", DailyTokenLimit: 42})
	store.AddModel(d, keepID, store.UpstreamModel{ModelName: "keep-model", Manual: true, ContextLength: 11, MaxOutputLength: 12})
	dupID, _ := store.CreateUpstream(d, &store.Upstream{Name: "dup", BaseURL: "https://old.example.com", APIKey: "sk-old", Format: "openai"})
	store.AddModel(d, dupID, store.UpstreamModel{ModelName: "old-model", ContextLength: 1, MaxOutputLength: 2})
	keepAliasID, _ := store.CreateAlias(d, &store.ModelAlias{Name: "keep-alias", Bindings: []store.AliasBinding{{UpstreamID: keepID, ModelName: "keep-model"}}})
	dupAliasID, _ := store.CreateAlias(d, &store.ModelAlias{Name: "dup-alias", Bindings: []store.AliasBinding{{UpstreamID: dupID, ModelName: "old-model"}}})

	payload := map[string]any{
		"version": 1,
		"upstreams": []map[string]any{
			{
				"name": "dup", "base_url": "https://new.example.com", "api_key": "sk-new",
				"format": "anthropic", "enabled": false, "daily_token_limit": 5, "monthly_token_limit": 7,
				"models": []map[string]any{{"model_name": "new-model", "manual": true, "context_length": 111, "max_output_length": 222, "multimodal": true}},
			},
			{"name": "fresh", "base_url": "https://fresh.example.com", "api_key": "sk-fresh", "format": "openai", "models": []map[string]any{}},
		},
		"aliases": []map[string]any{
			{"name": "dup-alias", "bindings": []map[string]any{
				{"upstream": "dup", "model_name": "new-model"},
				{"upstream": "fresh", "model_name": "fresh-model"},
			}},
			{"name": "fresh-alias", "bindings": []map[string]any{{"upstream": "fresh", "model_name": "fresh-model"}}},
		},
	}
	w := postConfig(t, a, "/api/admin/config/import", payload)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var res importResult
	json.Unmarshal(w.Body.Bytes(), &res)
	if res != (importResult{UpstreamsCreated: 1, UpstreamsUpdated: 1, AliasesCreated: 1, AliasesUpdated: 1}) {
		t.Fatalf("counts=%+v", res)
	}

	// keep: untouched
	ku, _ := store.GetUpstreamByID(d, keepID)
	if ku.BaseURL != "https://keep.example.com" || ku.APIKey != "sk-keep" || ku.DailyTokenLimit != 42 || !ku.Enabled {
		t.Fatalf("keep changed: %+v", ku)
	}
	kms, _ := store.ListModels(d, keepID)
	if len(kms) != 1 || kms[0].ModelName != "keep-model" || !kms[0].Manual || kms[0].ContextLength != 11 {
		t.Fatalf("keep models=%+v", kms)
	}

	// dup: fully overwritten, models replaced exactly
	du, _ := store.GetUpstreamByID(d, dupID)
	if du.BaseURL != "https://new.example.com" || du.APIKey != "sk-new" || du.Format != "anthropic" ||
		du.Enabled || du.DailyTokenLimit != 5 || du.MonthlyTokenLimit != 7 {
		t.Fatalf("dup not overwritten: %+v", du)
	}
	dms, _ := store.ListModels(d, dupID)
	if len(dms) != 1 || dms[0].ModelName != "new-model" || !dms[0].Manual || dms[0].ContextLength != 111 || dms[0].MaxOutputLength != 222 || !dms[0].Multimodal {
		t.Fatalf("dup models=%+v", dms)
	}

	// fresh: created
	fu, err := store.GetUpstreamByName(d, "fresh")
	if err != nil {
		t.Fatalf("fresh missing: %v", err)
	}
	if fu.BaseURL != "https://fresh.example.com" || !fu.Enabled {
		t.Fatalf("fresh=%+v", fu)
	}
	fms, _ := store.ListModels(d, fu.ID)
	if len(fms) != 0 {
		t.Fatalf("fresh models=%+v", fms)
	}

	// aliases
	ka, _ := store.GetAliasByID(d, keepAliasID)
	if len(ka.Bindings) != 1 || ka.Bindings[0].ModelName != "keep-model" {
		t.Fatalf("keep-alias changed: %+v", ka.Bindings)
	}
	da, _ := store.GetAliasByID(d, dupAliasID)
	if len(da.Bindings) != 2 || da.Bindings[0].UpstreamName != "dup" || da.Bindings[0].ModelName != "new-model" ||
		da.Bindings[0].Priority != 0 || da.Bindings[1].UpstreamName != "fresh" || da.Bindings[1].ModelName != "fresh-model" || da.Bindings[1].Priority != 1 {
		t.Fatalf("dup-alias bindings=%+v", da.Bindings)
	}
	fa, err := store.GetAliasByName(d, "fresh-alias")
	if err != nil {
		t.Fatalf("fresh-alias missing: %v", err)
	}
	if len(fa.Bindings) != 1 || fa.Bindings[0].UpstreamName != "fresh" || fa.Bindings[0].ModelName != "fresh-model" {
		t.Fatalf("fresh-alias=%+v", fa.Bindings)
	}
}

// TestImportConfig_KeepsFieldsWhenAbsent pins the「没给就保留」half of the
// contract: a minimal (hand-written) payload without enabled/models updates the
// scalar fields but leaves the enabled state and model list alone.
func TestImportConfig_KeepsFieldsWhenAbsent(t *testing.T) {
	a, d := setupAPI(t)
	id, _ := store.CreateUpstream(d, &store.Upstream{Name: "u", BaseURL: "https://old", APIKey: "sk-old", Format: "openai"})
	store.AddModel(d, id, store.UpstreamModel{ModelName: "m1", ContextLength: 3, MaxOutputLength: 4})
	u, _ := store.GetUpstreamByID(d, id)
	u.Enabled = false
	store.UpdateUpstream(d, u)

	payload := map[string]any{"upstreams": []map[string]any{
		{"name": "u", "base_url": "https://new", "api_key": "sk-new", "format": "openai"},
	}}
	w := postConfig(t, a, "/api/admin/config/import", payload)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	u, _ = store.GetUpstreamByID(d, id)
	if u.BaseURL != "https://new" || u.APIKey != "sk-new" {
		t.Fatalf("fields not updated: %+v", u)
	}
	if u.Enabled {
		t.Fatal("enabled should stay false when absent from file")
	}
	ms, _ := store.ListModels(d, id)
	if len(ms) != 1 || ms[0].ModelName != "m1" || ms[0].ContextLength != 3 {
		t.Fatalf("models should be kept: %+v", ms)
	}
}

// TestImportConfig_RoundTrip exports db1's config and imports it into a fresh
// db2, then re-exports db2 and compares — the primary real-world flow.
func TestImportConfig_RoundTrip(t *testing.T) {
	a1, d1 := setupAPI(t)
	id1, _ := store.CreateUpstream(d1, &store.Upstream{Name: "openai", BaseURL: "https://api.openai.com", APIKey: "sk-key-one", Format: "openai", DailyTokenLimit: 100})
	store.AddModel(d1, id1, store.UpstreamModel{ModelName: "gpt-4o", ContextLength: 128000, MaxOutputLength: 16384})
	id2, _ := store.CreateUpstream(d1, &store.Upstream{Name: "deepseek", BaseURL: "https://api.deepseek.com", APIKey: "sk-key-two", Format: "anthropic"})
	u2, _ := store.GetUpstreamByID(d1, id2)
	u2.Enabled = false
	store.UpdateUpstream(d1, u2)
	store.CreateAlias(d1, &store.ModelAlias{Name: "gpt4", Bindings: []store.AliasBinding{
		{UpstreamID: id1, ModelName: "gpt-4o"}, {UpstreamID: id2, ModelName: "deepseek-chat"},
	}})

	w := getConfig(t, a1, "/api/admin/config/export")
	if w.Code != 200 {
		t.Fatalf("export status=%d", w.Code)
	}
	var payload map[string]any
	json.Unmarshal(w.Body.Bytes(), &payload)

	a2, _ := setupAPI(t)
	w = postConfig(t, a2, "/api/admin/config/import", payload)
	if w.Code != 200 {
		t.Fatalf("import status=%d body=%s", w.Code, w.Body.String())
	}
	var res importResult
	json.Unmarshal(w.Body.Bytes(), &res)
	if res != (importResult{UpstreamsCreated: 2, AliasesCreated: 1}) {
		t.Fatalf("counts=%+v", res)
	}

	// re-export from db2 must equal the payload modulo exported_at
	payload["exported_at"] = ""
	w = getConfig(t, a2, "/api/admin/config/export")
	if w.Code != 200 {
		t.Fatalf("re-export status=%d", w.Code)
	}
	var out map[string]any
	json.Unmarshal(w.Body.Bytes(), &out)
	out["exported_at"] = ""
	if fmt.Sprint(out) != fmt.Sprint(payload) {
		t.Fatalf("round trip mismatch:\nwant=%s\ngot=%s", fmt.Sprint(payload), fmt.Sprint(out))
	}
}

// TestImportConfig_MaxConcurrent pins max_concurrent in config transfer:
// export carries it; import applies it when present, keeps the current value
// when absent, defaults a newly-created upstream to DefaultMaxConcurrent, and
// rejects negatives.
func TestImportConfig_MaxConcurrent(t *testing.T) {
	a, d := setupAPI(t)
	id, _ := store.CreateUpstream(d, &store.Upstream{Name: "u", BaseURL: "https://x", APIKey: "k", Format: "openai", MaxConcurrent: 42})

	// 导出带上 max_concurrent
	w := getConfig(t, a, "/api/admin/config/export")
	if w.Code != 200 {
		t.Fatalf("export status=%d", w.Code)
	}
	var out configExport
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Upstreams) != 1 || out.Upstreams[0]["max_concurrent"] != float64(42) {
		t.Fatalf("export upstreams=%v", out.Upstreams)
	}

	// 导入显式值 → 覆盖
	w = postConfig(t, a, "/api/admin/config/import", map[string]any{
		"upstreams": []map[string]any{
			{"name": "u", "base_url": "https://x", "api_key": "k", "format": "openai", "max_concurrent": 33},
			{"name": "fresh", "base_url": "https://y", "api_key": "k", "format": "openai", "max_concurrent": 0},
		},
	})
	if w.Code != 200 {
		t.Fatalf("import status=%d body=%s", w.Code, w.Body.String())
	}
	u, _ := store.GetUpstreamByID(d, id)
	if u.MaxConcurrent != 33 {
		t.Fatalf("after import max_concurrent=%d want 33", u.MaxConcurrent)
	}
	fresh, err := store.GetUpstreamByName(d, "fresh")
	if err != nil || fresh.MaxConcurrent != 0 {
		t.Fatalf("fresh explicit 0 = unlimited: %+v err=%v", fresh, err)
	}

	// 导入缺省 → 保留现状
	w = postConfig(t, a, "/api/admin/config/import", map[string]any{
		"upstreams": []map[string]any{
			{"name": "u", "base_url": "https://x2", "api_key": "k2", "format": "openai"},
			{"name": "fresh2", "base_url": "https://z", "api_key": "k", "format": "openai"},
		},
	})
	if w.Code != 200 {
		t.Fatalf("import status=%d body=%s", w.Code, w.Body.String())
	}
	u, _ = store.GetUpstreamByID(d, id)
	if u.MaxConcurrent != 33 {
		t.Fatalf("absent max_concurrent must keep current value, got %d", u.MaxConcurrent)
	}
	fresh2, err := store.GetUpstreamByName(d, "fresh2")
	if err != nil || fresh2.MaxConcurrent != store.DefaultMaxConcurrent {
		t.Fatalf("fresh2 omitted → default %d: %+v err=%v", store.DefaultMaxConcurrent, fresh2, err)
	}

	// 负数 → 400，且整体拒绝
	w = postConfig(t, a, "/api/admin/config/import", map[string]any{
		"upstreams": []map[string]any{{"name": "bad", "base_url": "https://x", "api_key": "k", "format": "openai", "max_concurrent": -1}},
	})
	if w.Code != 400 {
		t.Fatalf("negative status=%d want 400", w.Code)
	}
	if _, err := store.GetUpstreamByName(d, "bad"); err == nil {
		t.Fatal("negative max_concurrent should reject the whole file")
	}
}

// TestImportConfig_SkipsUnresolvableBindings: bindings referencing upstreams
// that exist neither in the file nor in the DB are dropped; an alias with no
// resolvable bindings left is skipped entirely. Bindings may also reference
// upstreams that pre-exist in the DB but are absent from the file.
func TestImportConfig_SkipsUnresolvableBindings(t *testing.T) {
	a, d := setupAPI(t)
	dbOnlyID, _ := store.CreateUpstream(d, &store.Upstream{Name: "db-only", BaseURL: "https://x", APIKey: "k", Format: "openai"})
	payload := map[string]any{
		"upstreams": []map[string]any{
			{"name": "fresh", "base_url": "https://x", "api_key": "k", "format": "openai"},
		},
		"aliases": []map[string]any{
			{"name": "bad", "bindings": []map[string]any{{"upstream": "ghost", "model_name": "m"}}},
			{"name": "partial", "bindings": []map[string]any{
				{"upstream": "ghost", "model_name": "m"},
				{"upstream": "fresh", "model_name": "from-file"},
				{"upstream": "db-only", "model_name": "from-db"},
			}},
		},
	}
	w := postConfig(t, a, "/api/admin/config/import", payload)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var res importResult
	json.Unmarshal(w.Body.Bytes(), &res)
	if res.AliasesCreated != 1 || res.AliasesSkipped != 1 {
		t.Fatalf("counts=%+v", res)
	}
	if res.BindingsDropped != 2 { // "bad" 的 ghost + "partial" 的 ghost
		t.Fatalf("bindings_dropped=%d", res.BindingsDropped)
	}
	if _, err := store.GetAliasByName(d, "bad"); err == nil {
		t.Fatal("alias 'bad' should not be created")
	}
	pa, err := store.GetAliasByName(d, "partial")
	if err != nil {
		t.Fatal(err)
	}
	if len(pa.Bindings) != 2 {
		t.Fatalf("partial bindings=%+v", pa.Bindings)
	}
	freshU, _ := store.GetUpstreamByName(d, "fresh")
	if pa.Bindings[0].UpstreamID != freshU.ID || pa.Bindings[0].ModelName != "from-file" || pa.Bindings[0].Priority != 0 {
		t.Fatalf("binding[0]=%+v", pa.Bindings[0])
	}
	if pa.Bindings[1].UpstreamID != dbOnlyID || pa.Bindings[1].ModelName != "from-db" || pa.Bindings[1].Priority != 1 {
		t.Fatalf("binding[1]=%+v", pa.Bindings[1])
	}
}

// TestImportConfig_InvalidInput: malformed payloads are rejected with 400 and
// NOTHING is written (validation happens before any DB write).
func TestImportConfig_InvalidInput(t *testing.T) {
	t.Run("bad json", func(t *testing.T) {
		a, _ := setupAPI(t)
		req := httptest.NewRequest("POST", "/api/admin/config/import", bytes.NewReader([]byte("{not json")))
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, req)
		if w.Code != 400 {
			t.Fatalf("status=%d", w.Code)
		}
	})
	t.Run("unsupported version", func(t *testing.T) {
		a, _ := setupAPI(t)
		w := postConfig(t, a, "/api/admin/config/import", map[string]any{"version": 99})
		if w.Code != 400 {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("bad upstream format rejects whole file", func(t *testing.T) {
		a, d := setupAPI(t)
		w := postConfig(t, a, "/api/admin/config/import", map[string]any{
			"upstreams": []map[string]any{
				{"name": "ok", "base_url": "https://x", "api_key": "k", "format": "openai"},
				{"name": "bad", "base_url": "https://x", "api_key": "k", "format": "yaml"},
			},
		})
		if w.Code != 400 {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		// the valid entry before the bad one must not have been written
		if _, err := store.GetUpstreamByName(d, "ok"); err == nil {
			t.Fatal("partial import leaked: 'ok' was written despite invalid file")
		}
	})
	t.Run("missing upstream name", func(t *testing.T) {
		a, _ := setupAPI(t)
		w := postConfig(t, a, "/api/admin/config/import", map[string]any{
			"upstreams": []map[string]any{{"base_url": "https://x", "api_key": "k", "format": "openai"}},
		})
		if w.Code != 400 {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("negative token limit", func(t *testing.T) {
		a, _ := setupAPI(t)
		w := postConfig(t, a, "/api/admin/config/import", map[string]any{
			"upstreams": []map[string]any{{"name": "n", "base_url": "https://x", "api_key": "k", "format": "openai", "daily_token_limit": -1}},
		})
		if w.Code != 400 {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("alias missing name", func(t *testing.T) {
		a, _ := setupAPI(t)
		w := postConfig(t, a, "/api/admin/config/import", map[string]any{
			"aliases": []map[string]any{{"bindings": []map[string]any{{"upstream": "x", "model_name": "m"}}}},
		})
		if w.Code != 400 {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("empty binding model name", func(t *testing.T) {
		a, _ := setupAPI(t)
		w := postConfig(t, a, "/api/admin/config/import", map[string]any{
			"aliases": []map[string]any{{"name": "a", "bindings": []map[string]any{{"upstream": "x", "model_name": ""}}}},
		})
		if w.Code != 400 {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("duplicate upstream name in file", func(t *testing.T) {
		a, _ := setupAPI(t)
		w := postConfig(t, a, "/api/admin/config/import", map[string]any{
			"upstreams": []map[string]any{
				{"name": "dup", "base_url": "https://x", "api_key": "k", "format": "openai"},
				{"name": "dup", "base_url": "https://y", "api_key": "k2", "format": "openai"},
			},
		})
		if w.Code != 400 {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("duplicate alias name in file (after trim)", func(t *testing.T) {
		a, _ := setupAPI(t)
		w := postConfig(t, a, "/api/admin/config/import", map[string]any{
			"upstreams": []map[string]any{{"name": "u", "base_url": "https://x", "api_key": "k", "format": "openai"}},
			"aliases": []map[string]any{
				{"name": "a", "bindings": []map[string]any{{"upstream": "u", "model_name": "m"}}},
				{"name": " a ", "bindings": []map[string]any{{"upstream": "u", "model_name": "m"}}},
			},
		})
		if w.Code != 400 {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})
}

// TestImportConfig_DBBindingErrorPropagates 绑定解析时数据库真错误（非
// 「不存在」）必须让导入整体失败，而不是当作「上游不存在」静默丢弃绑定后
// 返回 200。用关闭数据库模拟读失败。
func TestImportConfig_DBBindingErrorPropagates(t *testing.T) {
	a, d := setupAPI(t)
	d.Close() // setupAPI 的 Cleanup 会再 Close 一次，幂等
	w := postConfig(t, a, "/api/admin/config/import", map[string]any{
		"aliases": []map[string]any{
			{"name": "a", "bindings": []map[string]any{{"upstream": "ghost", "model_name": "m"}}},
		},
	})
	if w.Code < 400 {
		t.Fatalf("DB error must fail the import, got status=%d body=%s", w.Code, w.Body.String())
	}
}

// TestConfigImport_ExpiresAt 覆盖有效期在配置迁移里的三态：缺省保留现状、
// 显式 null 清除、具体值设置。与 enabled 的指针语义对齐。
func TestConfigImport_ExpiresAt(t *testing.T) {
	a, d := setupAPI(t)
	id, _ := store.CreateUpstream(d, &store.Upstream{Name: "u", BaseURL: "https://old", APIKey: "sk-old", Format: "openai"})
	at := time.Now().Add(24 * time.Hour).Truncate(time.Second)
	later := at.Add(48 * time.Hour)
	u, _ := store.GetUpstreamByID(d, id)
	u.ExpiresAt = &at
	store.UpdateUpstream(d, u)

	// 1. 文件里没有 expires_at → 保留现状
	postConfig(t, a, "/api/admin/config/import", map[string]any{"upstreams": []map[string]any{
		{"name": "u", "base_url": "https://x", "api_key": "sk-x", "format": "openai"},
	}})
	if got, _ := store.GetUpstreamByID(d, id); got.ExpiresAt == nil || !got.ExpiresAt.Equal(at) {
		t.Fatalf("absent expires_at=%v, want preserved %v", got.ExpiresAt, at)
	}

	// 2. 显式给出新值 → 覆盖
	postConfig(t, a, "/api/admin/config/import", map[string]any{"upstreams": []map[string]any{
		{"name": "u", "base_url": "https://x", "api_key": "sk-x", "format": "openai", "expires_at": later},
	}})
	if got, _ := store.GetUpstreamByID(d, id); got.ExpiresAt == nil || !got.ExpiresAt.Equal(later) {
		t.Fatalf("set expires_at=%v, want %v", got.ExpiresAt, later)
	}

	// 3. 显式 null → 清除，恢复永久有效
	postConfig(t, a, "/api/admin/config/import", map[string]any{"upstreams": []map[string]any{
		{"name": "u", "base_url": "https://x", "api_key": "sk-x", "format": "openai", "expires_at": nil},
	}})
	if got, _ := store.GetUpstreamByID(d, id); got.ExpiresAt != nil {
		t.Fatalf("clear expires_at=%v, want nil", got.ExpiresAt)
	}
}

// TestExportConfig_ExpiresAt 导出必须带上有效期（含 null），否则备份恢复后
// 会静默丢掉到期时间。
func TestExportConfig_ExpiresAt(t *testing.T) {
	a, d := setupAPI(t)
	at := time.Now().Add(24 * time.Hour).Truncate(time.Second)
	store.CreateUpstream(d, &store.Upstream{Name: "u", BaseURL: "b", APIKey: "k", Format: "openai", ExpiresAt: &at})
	store.CreateUpstream(d, &store.Upstream{Name: "perm", BaseURL: "b", APIKey: "k", Format: "openai"})

	w := getConfig(t, a, "/api/admin/config/export")
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var out configExport
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	byName := map[string]map[string]any{}
	for _, u := range out.Upstreams {
		byName[u["name"].(string)] = u
	}
	// 设置了有效期的：字段存在且是 RFC3339 字符串
	v, ok := byName["u"]["expires_at"]
	if !ok {
		t.Fatalf("expires_at missing from export: %v", byName["u"])
	}
	s, _ := v.(string)
	if s == "" {
		t.Fatal("expires_at should be a timestamp string")
	}
	if got, err := time.Parse(time.RFC3339, s); err != nil || !got.Equal(at) {
		t.Fatalf("expires_at=%q parsed=%v err=%v, want %v", s, got, err, at)
	}
	// 永久有效的：显式写出 null（不是缺省——缺省在导入侧意味着「保留现状」）
	v, ok = byName["perm"]["expires_at"]
	if !ok {
		t.Fatalf("expires_at missing for permanent upstream: %v", byName["perm"])
	}
	if v != nil {
		t.Fatalf("permanent expires_at=%v, want null", v)
	}
}
