package gateway

import (
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/great-magician-01/any-llm/internal/db"
	"github.com/great-magician-01/any-llm/internal/model"
)

func setupGateway(t *testing.T) (*Gateway, *sql.DB) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	g := New(d, nil)
	return g, d
}

func TestModelsEndpoint(t *testing.T) {
	g, d := setupGateway(t)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "my-openai", BaseURL: "b", APIKey: "k", Format: "openai"})
	model.AddModel(d, uid, "gpt-4o", false)
	model.AddModel(d, uid, "gpt-4o-mini", false)

	req := httptest.NewRequest("GET", "/v1/models", nil)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status=%d", w.Code)
	}
	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Data) != 2 {
		t.Fatalf("models=%d", len(resp.Data))
	}
	ids := map[string]bool{}
	for _, m := range resp.Data {
		ids[m.ID] = true
	}
	if !ids["my-openai/gpt-4o"] || !ids["my-openai/gpt-4o-mini"] {
		t.Fatalf("model ids=%+v", ids)
	}
}

func TestAuthMissingKey(t *testing.T) {
	g, _ := setupGateway(t)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"x/y","messages":[]}`))
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("status=%d want 401", w.Code)
	}
}

func TestAuthInvalidKey(t *testing.T) {
	g, d := setupGateway(t)
	model.CreateExtKey(d, "l")
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"x/y","messages":[]}`))
	req.Header.Set("Authorization", "Bearer all-sk-invalid")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("status=%d want 401", w.Code)
	}
}

func TestRouteModelNotFound(t *testing.T) {
	g, d := setupGateway(t)
	k, _ := model.CreateExtKey(d, "l")
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"nonexistent/model","messages":[]}`))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatalf("status=%d want 404", w.Code)
	}
}

func TestRouteInvalidModelFormat(t *testing.T) {
	g, d := setupGateway(t)
	k, _ := model.CreateExtKey(d, "l")
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"nomodelslash","messages":[]}`))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("status=%d want 400", w.Code)
	}
}
