package upstream

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/great-magician-01/any-llm/internal/model"
)

func TestFetchModels_OpenAI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Fatalf("auth=%s", r.Header.Get("Authorization"))
		}
		w.Write([]byte(`{"data":[{"id":"gpt-4o"},{"id":"gpt-4o-mini"}]}`))
	}))
	defer srv.Close()

	u := &model.Upstream{BaseURL: srv.URL, APIKey: "sk-test", Format: "openai"}
	models, err := FetchModels(context.Background(), http.DefaultClient, u)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0] != "gpt-4o" || models[1] != "gpt-4o-mini" {
		t.Fatalf("models=%+v", models)
	}
}

func TestFetchModels_Anthropic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "sk-ant" {
			t.Fatalf("x-api-key=%s", r.Header.Get("x-api-key"))
		}
		w.Write([]byte(`{"data":[{"id":"claude-3-5-sonnet"},{"id":"claude-3-opus"}]}`))
	}))
	defer srv.Close()

	u := &model.Upstream{BaseURL: srv.URL, APIKey: "sk-ant", Format: "anthropic"}
	models, err := FetchModels(context.Background(), http.DefaultClient, u)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0] != "claude-3-5-sonnet" {
		t.Fatalf("models=%+v", models)
	}
}
