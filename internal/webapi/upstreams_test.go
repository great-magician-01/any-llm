package webapi

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/great-magician-01/any-llm/internal/db"
	"github.com/great-magician-01/any-llm/internal/model"
	"github.com/great-magician-01/any-llm/internal/upstream"
)

func setupAPI(t *testing.T) (*API, *sql.DB) {
	t.Helper()
	d, err := db.OpenSQLite(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	a := NewAPI(d, nil, nil)
	return a, d
}

func TestCreateUpstream(t *testing.T) {
	a, _ := setupAPI(t)
	body, _ := json.Marshal(map[string]any{"name": "test", "base_url": "https://api.openai.com", "api_key": "sk-xxx", "format": "openai"})
	req := httptest.NewRequest("POST", "/api/admin/upstreams", bytes.NewReader(body))
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 && w.Code != 201 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["name"] != "test" {
		t.Fatalf("resp=%+v", resp)
	}
}

func TestListUpstreams(t *testing.T) {
	a, d := setupAPI(t)
	model.CreateUpstream(d, &model.Upstream{Name: "u1", BaseURL: "b", APIKey: "k", Format: "openai"})
	model.CreateUpstream(d, &model.Upstream{Name: "u2", BaseURL: "b", APIKey: "k", Format: "anthropic"})
	req := httptest.NewRequest("GET", "/api/admin/upstreams", nil)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d", w.Code)
	}
	var resp struct {
		Data []map[string]any `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Data) != 2 {
		t.Fatalf("len=%d", len(resp.Data))
	}
}

func TestDeleteUpstream(t *testing.T) {
	a, d := setupAPI(t)
	id, _ := model.CreateUpstream(d, &model.Upstream{Name: "u1", BaseURL: "b", APIKey: "k", Format: "openai"})
	req := httptest.NewRequest("DELETE", "/api/admin/upstreams/"+strconv.FormatInt(id, 10), nil)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 && w.Code != 204 {
		t.Fatalf("status=%d", w.Code)
	}
	list, _ := model.ListUpstreams(d)
	if len(list) != 0 {
		t.Fatalf("after delete len=%d", len(list))
	}
}

func TestFetchModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"id":"gpt-4o"},{"id":"gpt-4o-mini"}]}`))
	}))
	defer srv.Close()

	a, d := setupAPI(t)
	a.client = upstream.NewClient(http.DefaultClient)
	id, _ := model.CreateUpstream(d, &model.Upstream{Name: "u", BaseURL: srv.URL, APIKey: "k", Format: "openai"})

	req := httptest.NewRequest("POST", "/api/admin/upstreams/"+strconv.FormatInt(id, 10)+"/fetch-models", nil)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	models, _ := model.ListModels(d, id)
	if len(models) != 2 {
		t.Fatalf("models=%d", len(models))
	}
}

// TestUpdateUpstream_MaskedKeyNotOverwritten verifies that when the client
// sends back the masked API key placeholder (as the admin UI does when a user
// edits an upstream without re-entering the secret), the gateway must NOT
// overwrite the stored key with the masked string.
func TestUpdateUpstream_MaskedKeyNotOverwritten(t *testing.T) {
	a, d := setupAPI(t)
	const realKey = "sk-abcdefghijklmno1234567890qrstuvwxyz"
	id, _ := model.CreateUpstream(d, &model.Upstream{Name: "u", BaseURL: "https://example.com", APIKey: realKey, Format: "openai"})

	// Simulate the UI echoing back the masked key as returned by the
	// listUpstreams HTTP endpoint (model layer returns raw, webapi masks).
	masked := realKey[:4] + "****" + realKey[len(realKey)-4:]

	body, _ := json.Marshal(map[string]any{
		"name":     "u",
		"base_url": "https://example.com",
		"api_key":  masked,
		"format":   "openai",
	})
	req := httptest.NewRequest("PUT", "/api/admin/upstreams/"+strconv.FormatInt(id, 10), bytes.NewReader(body))
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	// Verify the actual stored key was NOT replaced with the masked string.
	u, _ := model.GetUpstreamByID(d, id)
	if u.APIKey != realKey {
		t.Fatalf("stored key got overwritten: got=%q want=%q", u.APIKey, realKey)
	}
}

// TestUpdateUpstream_EmptyKeyPreserved verifies that sending an empty api_key
// keeps the existing stored value (the standard "no change" signal).
func TestUpdateUpstream_EmptyKeyPreserved(t *testing.T) {
	a, d := setupAPI(t)
	const realKey = "sk-realsecret123"
	id, _ := model.CreateUpstream(d, &model.Upstream{Name: "u", BaseURL: "https://example.com", APIKey: realKey, Format: "openai"})

	body, _ := json.Marshal(map[string]any{
		"name":    "u-renamed",
		"base_url": "https://example.com",
		"api_key": "",
		"format":  "openai",
	})
	req := httptest.NewRequest("PUT", "/api/admin/upstreams/"+strconv.FormatInt(id, 10), bytes.NewReader(body))
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	u, _ := model.GetUpstreamByID(d, id)
	if u.APIKey != realKey {
		t.Fatalf("stored key changed: got=%q want=%q", u.APIKey, realKey)
	}
	if u.Name != "u-renamed" {
		t.Fatalf("name not updated: %q", u.Name)
	}
}

// TestUpdateUpstream_NewKeyApplied verifies that a real (non-masked) key
// still overwrites the stored value.
func TestUpdateUpstream_NewKeyApplied(t *testing.T) {
	a, d := setupAPI(t)
	id, _ := model.CreateUpstream(d, &model.Upstream{Name: "u", BaseURL: "https://example.com", APIKey: "sk-old", Format: "openai"})

	body, _ := json.Marshal(map[string]any{
		"name":    "u",
		"base_url": "https://example.com",
		"api_key": "sk-new-real-key",
		"format":  "openai",
	})
	req := httptest.NewRequest("PUT", "/api/admin/upstreams/"+strconv.FormatInt(id, 10), bytes.NewReader(body))
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	u, _ := model.GetUpstreamByID(d, id)
	if u.APIKey != "sk-new-real-key" {
		t.Fatalf("stored key not updated: got=%q", u.APIKey)
	}
}
