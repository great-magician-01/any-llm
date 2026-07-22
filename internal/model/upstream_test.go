package model

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/great-magician-01/any-llm/internal/db"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := db.OpenSQLite(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestCreateAndGetUpstream(t *testing.T) {
	d := testDB(t)
	id, err := CreateUpstream(d, &Upstream{Name: "my-openai", BaseURL: "https://api.openai.com", APIKey: "sk-xxx", Format: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatal("id=0")
	}
	got, err := GetUpstreamByID(d, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "my-openai" || got.BaseURL != "https://api.openai.com" || got.APIKey != "sk-xxx" || got.Format != "openai" {
		t.Fatalf("got=%+v", got)
	}
	byName, err := GetUpstreamByName(d, "my-openai")
	if err != nil {
		t.Fatal(err)
	}
	if byName.ID != id {
		t.Fatalf("byName id=%d want %d", byName.ID, id)
	}
}

func TestUniqueName(t *testing.T) {
	d := testDB(t)
	_, _ = CreateUpstream(d, &Upstream{Name: "dup", BaseURL: "u", APIKey: "k", Format: "openai"})
	_, err := CreateUpstream(d, &Upstream{Name: "dup", BaseURL: "u2", APIKey: "k2", Format: "anthropic"})
	if err == nil {
		t.Fatal("expected duplicate name error")
	}
}

func TestListUpdateDeleteUpstream(t *testing.T) {
	d := testDB(t)
	id, _ := CreateUpstream(d, &Upstream{Name: "u1", BaseURL: "b", APIKey: "k", Format: "openai"})
	CreateUpstream(d, &Upstream{Name: "u2", BaseURL: "b", APIKey: "k", Format: "anthropic"})
	list, err := ListUpstreams(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("list len=%d", len(list))
	}
	u, _ := GetUpstreamByID(d, id)
	u.BaseURL = "updated"
	if err := UpdateUpstream(d, u); err != nil {
		t.Fatal(err)
	}
	u2, _ := GetUpstreamByID(d, id)
	if u2.BaseURL != "updated" {
		t.Fatalf("base_url=%q", u2.BaseURL)
	}
	if err := DeleteUpstream(d, id); err != nil {
		t.Fatal(err)
	}
	list, _ = ListUpstreams(d)
	if len(list) != 1 {
		t.Fatalf("after delete len=%d", len(list))
	}
}

func TestModelsCRUD(t *testing.T) {
	d := testDB(t)
	uid, _ := CreateUpstream(d, &Upstream{Name: "u", BaseURL: "b", APIKey: "k", Format: "openai"})
	if err := AddModel(d, uid, "gpt-4o", true, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := AddModel(d, uid, "gpt-4o-mini", false, 0, 0); err != nil {
		t.Fatal(err)
	}
	models, err := ListModels(d, uid)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("models len=%d", len(models))
	}
	// ReplaceModels should keep manual, replace non-manual
	if err := ReplaceModels(d, uid, []string{"gpt-4o-mini", "o3"}); err != nil {
		t.Fatal(err)
	}
	models, _ = ListModels(d, uid)
	if len(models) != 3 {
		t.Fatalf("after replace len=%d", len(models))
	}
	// gpt-4o (manual) kept, gpt-4o-mini kept (in new list), o3 added
	names := map[string]bool{}
	for _, m := range models {
		names[m.ModelName] = true
	}
	if !names["gpt-4o"] || !names["gpt-4o-mini"] || !names["o3"] {
		t.Fatalf("models after replace=%+v", names)
	}
	// delete one
	if err := DeleteModel(d, models[0].ID); err != nil {
		t.Fatal(err)
	}
	models, _ = ListModels(d, uid)
	if len(models) != 2 {
		t.Fatalf("after delete len=%d", len(models))
	}
}

func TestDeleteUpstreamCascadesModels(t *testing.T) {
	d := testDB(t)
	uid, _ := CreateUpstream(d, &Upstream{Name: "u", BaseURL: "b", APIKey: "k", Format: "openai"})
	AddModel(d, uid, "m1", false, 0, 0)
	if err := DeleteUpstream(d, uid); err != nil {
		t.Fatal(err)
	}
	models, err := ListModels(d, uid)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 0 {
		t.Fatalf("cascade failed, models=%d", len(models))
	}
}
