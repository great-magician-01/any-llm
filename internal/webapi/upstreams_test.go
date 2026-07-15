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
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	a := NewAPI(d, nil)
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
