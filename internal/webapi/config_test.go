package webapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/great-magician-01/any-llm/internal/model"
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
	id1, _ := model.CreateUpstream(d, &model.Upstream{Name: "openai", BaseURL: "https://api.openai.com", APIKey: "sk-real-secret-key-123", Format: "openai", DailyTokenLimit: 1000, MonthlyTokenLimit: 20000})
	model.AddModel(d, id1, "gpt-4o", false, 128000, 16384)
	id2, _ := model.CreateUpstream(d, &model.Upstream{Name: "deepseek", BaseURL: "https://api.deepseek.com", APIKey: "sk-another-key", Format: "anthropic"})
	u2, _ := model.GetUpstreamByID(d, id2)
	u2.Enabled = false
	model.UpdateUpstream(d, u2)
	model.CreateAlias(d, &model.ModelAlias{Name: "gpt4", Bindings: []model.AliasBinding{
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
	if m["model_name"] != "gpt-4o" || m["context_length"] != float64(128000) || m["max_output_length"] != float64(16384) {
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
	keepID, _ := model.CreateUpstream(d, &model.Upstream{Name: "keep", BaseURL: "https://keep.example.com", APIKey: "sk-keep", Format: "openai", DailyTokenLimit: 42})
	model.AddModel(d, keepID, "keep-model", true, 11, 12)
	dupID, _ := model.CreateUpstream(d, &model.Upstream{Name: "dup", BaseURL: "https://old.example.com", APIKey: "sk-old", Format: "openai"})
	model.AddModel(d, dupID, "old-model", false, 1, 2)
	keepAliasID, _ := model.CreateAlias(d, &model.ModelAlias{Name: "keep-alias", Bindings: []model.AliasBinding{{UpstreamID: keepID, ModelName: "keep-model"}}})
	dupAliasID, _ := model.CreateAlias(d, &model.ModelAlias{Name: "dup-alias", Bindings: []model.AliasBinding{{UpstreamID: dupID, ModelName: "old-model"}}})

	payload := map[string]any{
		"version": 1,
		"upstreams": []map[string]any{
			{
				"name": "dup", "base_url": "https://new.example.com", "api_key": "sk-new",
				"format": "anthropic", "enabled": false, "daily_token_limit": 5, "monthly_token_limit": 7,
				"models": []map[string]any{{"model_name": "new-model", "manual": true, "context_length": 111, "max_output_length": 222}},
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
	ku, _ := model.GetUpstreamByID(d, keepID)
	if ku.BaseURL != "https://keep.example.com" || ku.APIKey != "sk-keep" || ku.DailyTokenLimit != 42 || !ku.Enabled {
		t.Fatalf("keep changed: %+v", ku)
	}
	kms, _ := model.ListModels(d, keepID)
	if len(kms) != 1 || kms[0].ModelName != "keep-model" || !kms[0].Manual || kms[0].ContextLength != 11 {
		t.Fatalf("keep models=%+v", kms)
	}

	// dup: fully overwritten, models replaced exactly
	du, _ := model.GetUpstreamByID(d, dupID)
	if du.BaseURL != "https://new.example.com" || du.APIKey != "sk-new" || du.Format != "anthropic" ||
		du.Enabled || du.DailyTokenLimit != 5 || du.MonthlyTokenLimit != 7 {
		t.Fatalf("dup not overwritten: %+v", du)
	}
	dms, _ := model.ListModels(d, dupID)
	if len(dms) != 1 || dms[0].ModelName != "new-model" || !dms[0].Manual || dms[0].ContextLength != 111 || dms[0].MaxOutputLength != 222 {
		t.Fatalf("dup models=%+v", dms)
	}

	// fresh: created
	fu, err := model.GetUpstreamByName(d, "fresh")
	if err != nil {
		t.Fatalf("fresh missing: %v", err)
	}
	if fu.BaseURL != "https://fresh.example.com" || !fu.Enabled {
		t.Fatalf("fresh=%+v", fu)
	}
	fms, _ := model.ListModels(d, fu.ID)
	if len(fms) != 0 {
		t.Fatalf("fresh models=%+v", fms)
	}

	// aliases
	ka, _ := model.GetAliasByID(d, keepAliasID)
	if len(ka.Bindings) != 1 || ka.Bindings[0].ModelName != "keep-model" {
		t.Fatalf("keep-alias changed: %+v", ka.Bindings)
	}
	da, _ := model.GetAliasByID(d, dupAliasID)
	if len(da.Bindings) != 2 || da.Bindings[0].UpstreamName != "dup" || da.Bindings[0].ModelName != "new-model" ||
		da.Bindings[0].Priority != 0 || da.Bindings[1].UpstreamName != "fresh" || da.Bindings[1].ModelName != "fresh-model" || da.Bindings[1].Priority != 1 {
		t.Fatalf("dup-alias bindings=%+v", da.Bindings)
	}
	fa, err := model.GetAliasByName(d, "fresh-alias")
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
	id, _ := model.CreateUpstream(d, &model.Upstream{Name: "u", BaseURL: "https://old", APIKey: "sk-old", Format: "openai"})
	model.AddModel(d, id, "m1", false, 3, 4)
	u, _ := model.GetUpstreamByID(d, id)
	u.Enabled = false
	model.UpdateUpstream(d, u)

	payload := map[string]any{"upstreams": []map[string]any{
		{"name": "u", "base_url": "https://new", "api_key": "sk-new", "format": "openai"},
	}}
	w := postConfig(t, a, "/api/admin/config/import", payload)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	u, _ = model.GetUpstreamByID(d, id)
	if u.BaseURL != "https://new" || u.APIKey != "sk-new" {
		t.Fatalf("fields not updated: %+v", u)
	}
	if u.Enabled {
		t.Fatal("enabled should stay false when absent from file")
	}
	ms, _ := model.ListModels(d, id)
	if len(ms) != 1 || ms[0].ModelName != "m1" || ms[0].ContextLength != 3 {
		t.Fatalf("models should be kept: %+v", ms)
	}
}

// TestImportConfig_RoundTrip exports db1's config and imports it into a fresh
// db2, then re-exports db2 and compares — the primary real-world flow.
func TestImportConfig_RoundTrip(t *testing.T) {
	a1, d1 := setupAPI(t)
	id1, _ := model.CreateUpstream(d1, &model.Upstream{Name: "openai", BaseURL: "https://api.openai.com", APIKey: "sk-key-one", Format: "openai", DailyTokenLimit: 100})
	model.AddModel(d1, id1, "gpt-4o", false, 128000, 16384)
	id2, _ := model.CreateUpstream(d1, &model.Upstream{Name: "deepseek", BaseURL: "https://api.deepseek.com", APIKey: "sk-key-two", Format: "anthropic"})
	u2, _ := model.GetUpstreamByID(d1, id2)
	u2.Enabled = false
	model.UpdateUpstream(d1, u2)
	model.CreateAlias(d1, &model.ModelAlias{Name: "gpt4", Bindings: []model.AliasBinding{
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

// TestImportConfig_SkipsUnresolvableBindings: bindings referencing upstreams
// that exist neither in the file nor in the DB are dropped; an alias with no
// resolvable bindings left is skipped entirely. Bindings may also reference
// upstreams that pre-exist in the DB but are absent from the file.
func TestImportConfig_SkipsUnresolvableBindings(t *testing.T) {
	a, d := setupAPI(t)
	dbOnlyID, _ := model.CreateUpstream(d, &model.Upstream{Name: "db-only", BaseURL: "https://x", APIKey: "k", Format: "openai"})
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
	if _, err := model.GetAliasByName(d, "bad"); err == nil {
		t.Fatal("alias 'bad' should not be created")
	}
	pa, err := model.GetAliasByName(d, "partial")
	if err != nil {
		t.Fatal(err)
	}
	if len(pa.Bindings) != 2 {
		t.Fatalf("partial bindings=%+v", pa.Bindings)
	}
	freshU, _ := model.GetUpstreamByName(d, "fresh")
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
		if _, err := model.GetUpstreamByName(d, "ok"); err == nil {
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
