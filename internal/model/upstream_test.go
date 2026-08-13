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

// TestSoftDeleteUpstream verifies deletion marks is_active=0 instead of
// removing rows: lookups fail, lists exclude, models are soft-deleted too,
// and the same name can be re-created afterwards.
func TestSoftDeleteUpstream(t *testing.T) {
	d := testDB(t)
	uid, _ := CreateUpstream(d, &Upstream{Name: "u", BaseURL: "b", APIKey: "k", Format: "openai"})
	AddModel(d, uid, "m1", false, 0, 0)

	if err := DeleteUpstream(d, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := GetUpstreamByName(d, "u"); err == nil {
		t.Fatal("deleted upstream still resolvable by name")
	}
	if _, err := GetUpstreamByID(d, uid); err == nil {
		t.Fatal("deleted upstream still resolvable by id")
	}
	list, _ := ListUpstreams(d)
	if len(list) != 0 {
		t.Fatalf("list after delete len=%d", len(list))
	}
	// 模型一并软删除
	models, _ := ListModels(d, uid)
	if len(models) != 0 {
		t.Fatalf("models after upstream delete len=%d", len(models))
	}
	// 行仍在（软删除）：可重新统计
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM upstreams WHERE name='u'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("row should be kept: n=%d err=%v", n, err)
	}
	// 同名可重建（部分唯一索引只约束活跃行）
	if _, err := CreateUpstream(d, &Upstream{Name: "u", BaseURL: "b2", APIKey: "k2", Format: "anthropic"}); err != nil {
		t.Fatalf("re-create same name after soft delete: %v", err)
	}
	// 活跃行唯一性仍生效
	if _, err := CreateUpstream(d, &Upstream{Name: "u", BaseURL: "b3", APIKey: "k3", Format: "openai"}); err == nil {
		t.Fatal("duplicate active name should be rejected")
	}
}

// TestSoftDeleteModelAndRevive verifies model soft delete and ReplaceModels
// revive: a model removed by sync can come back without creating a duplicate
// row (same id revived).
func TestSoftDeleteModelAndRevive(t *testing.T) {
	d := testDB(t)
	uid, _ := CreateUpstream(d, &Upstream{Name: "u", BaseURL: "b", APIKey: "k", Format: "openai"})
	AddModel(d, uid, "m1", false, 0, 0)
	AddModel(d, uid, "m2", false, 0, 0)

	// 同步只保留 m1：m2 软删除
	if err := ReplaceModels(d, uid, []string{"m1"}); err != nil {
		t.Fatal(err)
	}
	models, _ := ListModels(d, uid)
	if len(models) != 1 || models[0].ModelName != "m1" {
		t.Fatalf("after sync models=%+v", models)
	}
	var m2ID int64
	if err := d.QueryRow(`SELECT id FROM upstream_models WHERE model_name='m2'`).Scan(&m2ID); err != nil {
		t.Fatalf("m2 row should be kept: %v", err)
	}
	// m2 重新出现在上游列表：复活原行而不是插入重复行
	if err := ReplaceModels(d, uid, []string{"m1", "m2"}); err != nil {
		t.Fatal(err)
	}
	models, _ = ListModels(d, uid)
	if len(models) != 2 {
		t.Fatalf("after revive models=%d", len(models))
	}
	var revivedID int64
	if err := d.QueryRow(`SELECT id FROM upstream_models WHERE model_name='m2' AND is_active = 1`).Scan(&revivedID); err != nil {
		t.Fatalf("m2 revived row: %v", err)
	}
	if revivedID != m2ID {
		t.Fatalf("m2 should be revived with original id %d, got %d", m2ID, revivedID)
	}
	// 手动删除模型后同名可重建
	for _, m := range models {
		if m.ModelName == "m1" {
			if err := DeleteModel(d, m.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := AddModel(d, uid, "m1", true, 0, 0); err != nil {
		t.Fatalf("re-add same model name after soft delete: %v", err)
	}
}

// TestReplaceModelsSkipsManualConflict 回归：自动模型被管理员删除后又被手动
// 重建，再次同步时不得复活旧的自动行——否则新旧两行同活跃在部分唯一索引上
// 冲突，整个同步事务失败且之后每次同步都失败。
func TestReplaceModelsSkipsManualConflict(t *testing.T) {
	d := testDB(t)
	uid, _ := CreateUpstream(d, &Upstream{Name: "u", BaseURL: "b", APIKey: "k", Format: "openai"})
	// 1. 同步拉取到自动模型 m
	if err := ReplaceModels(d, uid, []string{"m"}); err != nil {
		t.Fatal(err)
	}
	// 2. 管理员删除 m
	models, _ := ListModels(d, uid)
	if err := DeleteModel(d, models[0].ID); err != nil {
		t.Fatal(err)
	}
	// 3. 管理员手动添加同名模型 m
	if err := AddModel(d, uid, "m", true, 0, 0); err != nil {
		t.Fatal(err)
	}
	// 4. 再次同步不得失败，手动行保持活跃且不累积重复行
	if err := ReplaceModels(d, uid, []string{"m"}); err != nil {
		t.Fatalf("ReplaceModels failed: %v", err)
	}
	models, _ = ListModels(d, uid)
	if len(models) != 1 || models[0].ModelName != "m" || !models[0].Manual {
		t.Fatalf("models=%+v", models)
	}
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM upstream_models WHERE model_name='m'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows accumulated: n=%d err=%v", n, err)
	}
}

// TestAddModelRevivesSoftDeleted verifies AddModel 优先复活同名软删除行
// （保留原 id、应用新的 manual 与长度），而不是插入重复行。
func TestAddModelRevivesSoftDeleted(t *testing.T) {
	d := testDB(t)
	uid, _ := CreateUpstream(d, &Upstream{Name: "u", BaseURL: "b", APIKey: "k", Format: "openai"})
	AddModel(d, uid, "m", false, 0, 0)
	models, _ := ListModels(d, uid)
	origID := models[0].ID
	if err := DeleteModel(d, origID); err != nil {
		t.Fatal(err)
	}
	if err := AddModel(d, uid, "m", true, 1000, 2000); err != nil {
		t.Fatal(err)
	}
	models, _ = ListModels(d, uid)
	if len(models) != 1 || models[0].ID != origID || !models[0].Manual {
		t.Fatalf("revived=%+v", models)
	}
	if models[0].ContextLength != 1000 || models[0].MaxOutputLength != 2000 {
		t.Fatalf("lengths not applied: %+v", models[0])
	}
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM upstream_models WHERE model_name='m'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("duplicate rows: n=%d err=%v", n, err)
	}
}

// TestUpdateIgnoresSoftDeleted verifies Update/Touch 对已软删除的行静默无效。
func TestUpdateIgnoresSoftDeleted(t *testing.T) {
	d := testDB(t)
	uid, _ := CreateUpstream(d, &Upstream{Name: "u", BaseURL: "b", APIKey: "k", Format: "openai"})
	if err := DeleteUpstream(d, uid); err != nil {
		t.Fatal(err)
	}
	if err := UpdateUpstream(d, &Upstream{ID: uid, Name: "x", BaseURL: "y", APIKey: "z", Format: "openai"}); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := d.QueryRow(`SELECT name FROM upstreams WHERE id=?`, uid).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "u" {
		t.Fatalf("soft-deleted row was updated: %s", name)
	}
}
