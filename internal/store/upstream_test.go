package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/great-magician-01/any-llm/internal/db"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	// 配置读缓存是包级、进程内的，而每个用例都是一个临时库：不清就会把上一个
	// 用例的条目带到本用例（同名即遮蔽）。
	ResetConfigCache()
	t.Cleanup(ResetConfigCache)
	d, err := db.OpenSQLite(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestCreateAndGetUpstream(t *testing.T) {
	d := testDB(t)
	id, err := CreateUpstream(d, &Upstream{Name: "my-openai", BaseURL: "https://api.openai.com", APIKey: "sk-xxx", Format: "openai", MaxConcurrent: 42})
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
	if got.MaxConcurrent != 42 {
		t.Fatalf("max_concurrent=%d want 42", got.MaxConcurrent)
	}
	byName, err := GetUpstreamByName(d, "my-openai")
	if err != nil {
		t.Fatal(err)
	}
	if byName.ID != id || byName.MaxConcurrent != 42 {
		t.Fatalf("byName=%+v want id=%d", byName, id)
	}
	list, err := ListUpstreams(d, nil)
	if err != nil || len(list) != 1 || list[0].MaxConcurrent != 42 {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	// UpdateUpstream 全量覆盖：改其他字段不得清掉 max_concurrent（先 Get 再存约定）
	got.BaseURL = "https://changed"
	if err := UpdateUpstream(d, got); err != nil {
		t.Fatal(err)
	}
	again, _ := GetUpstreamByID(d, id)
	if again.MaxConcurrent != 42 || again.BaseURL != "https://changed" {
		t.Fatalf("after update=%+v", again)
	}
}

// TestUpstreamRemark 盯住 remark 的完整往返：创建带入、三个读取入口读回、
// 改别的字段再全量保存后仍在。最后一条是关键——UpdateUpstream 是全量覆盖，
// UPDATE 语句漏了 remark 就会在每次保存（含只改 enabled 的开关）时静默清空备注。
func TestUpstreamRemark(t *testing.T) {
	d := testDB(t)
	id, err := CreateUpstream(d, &Upstream{Name: "u", BaseURL: "b", APIKey: "k", Format: "openai", Remark: "月付 200 元"})
	if err != nil {
		t.Fatal(err)
	}
	// 未填备注的上游：空串而不是 NULL 扫描失败
	plain, err := CreateUpstream(d, &Upstream{Name: "plain", BaseURL: "b", APIKey: "k", Format: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	if u, _ := GetUpstreamByID(d, plain); u.Remark != "" {
		t.Fatalf("unset remark=%q, want empty", u.Remark)
	}

	for _, got := range []struct {
		name string
		u    *Upstream
	}{
		{"byID", mustGet(t, d, id)},
		{"byName", mustGetByName(t, d, "u")},
	} {
		if got.u.Remark != "月付 200 元" {
			t.Fatalf("%s remark=%q", got.name, got.u.Remark)
		}
	}
	list, err := ListUpstreams(d, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Remark != "月付 200 元" {
		t.Fatalf("list remark=%q (len=%d)", list[0].Remark, len(list))
	}

	// 全量保存不得抹掉备注
	u := mustGet(t, d, id)
	u.BaseURL = "https://changed"
	u.Enabled = false
	if err := UpdateUpstream(d, u); err != nil {
		t.Fatal(err)
	}
	again := mustGet(t, d, id)
	if again.Remark != "月付 200 元" || again.BaseURL != "https://changed" || again.Enabled {
		t.Fatalf("after update=%+v", again)
	}
	// 改备注本身也要落库
	again.Remark = "已停用"
	if err := UpdateUpstream(d, again); err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, d, id); got.Remark != "已停用" {
		t.Fatalf("remark update=%q", got.Remark)
	}
}

func mustGet(t *testing.T, d *sql.DB, id int64) *Upstream {
	t.Helper()
	u, err := GetUpstreamByID(d, id)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func mustGetByName(t *testing.T, d *sql.DB, name string) *Upstream {
	t.Helper()
	u, err := GetUpstreamByName(d, name)
	if err != nil {
		t.Fatal(err)
	}
	return u
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
	list, err := ListUpstreams(d, nil)
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
	list, _ = ListUpstreams(d, nil)
	if len(list) != 1 {
		t.Fatalf("after delete len=%d", len(list))
	}
}

func TestListUpstreamsEnabledFilter(t *testing.T) {
	d := testDB(t)
	CreateUpstream(d, &Upstream{Name: "on1", BaseURL: "b", APIKey: "k", Format: "openai"})
	CreateUpstream(d, &Upstream{Name: "on2", BaseURL: "b", APIKey: "k", Format: "anthropic"})
	offID, _ := CreateUpstream(d, &Upstream{Name: "off", BaseURL: "b", APIKey: "k", Format: "openai"})
	// CreateUpstream 的 INSERT 不含 enabled 列（DB 默认启用），创建即禁用需读回再改存。
	off, _ := GetUpstreamByID(d, offID)
	off.Enabled = false
	if err := UpdateUpstream(d, off); err != nil {
		t.Fatal(err)
	}
	// 给禁用行挂一个模型：验证过滤后 model_count 子查询仍然正确
	if err := AddModel(d, offID, UpstreamModel{ModelName: "gpt-4o", Manual: true}); err != nil {
		t.Fatal(err)
	}

	yes, no := true, false
	all, err := ListUpstreams(d, nil)
	if err != nil || len(all) != 3 {
		t.Fatalf("all len=%d err=%v", len(all), err)
	}
	enabled, err := ListUpstreams(d, &yes)
	if err != nil || len(enabled) != 2 {
		t.Fatalf("enabled len=%d err=%v", len(enabled), err)
	}
	for _, u := range enabled {
		if !u.Enabled {
			t.Fatalf("enabled filter returned disabled row: %+v", u)
		}
	}
	disabled, err := ListUpstreams(d, &no)
	if err != nil || len(disabled) != 1 || disabled[0].Name != "off" {
		t.Fatalf("disabled=%+v err=%v", disabled, err)
	}
	if disabled[0].ModelCount != 1 {
		t.Fatalf("model_count=%d want 1", disabled[0].ModelCount)
	}
}

func TestModelsCRUD(t *testing.T) {
	d := testDB(t)
	uid, _ := CreateUpstream(d, &Upstream{Name: "u", BaseURL: "b", APIKey: "k", Format: "openai"})
	if err := AddModel(d, uid, UpstreamModel{ModelName: "gpt-4o", Manual: true}); err != nil {
		t.Fatal(err)
	}
	if err := AddModel(d, uid, UpstreamModel{ModelName: "gpt-4o-mini"}); err != nil {
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
	AddModel(d, uid, UpstreamModel{ModelName: "m1"})
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
	AddModel(d, uid, UpstreamModel{ModelName: "m1"})

	if err := DeleteUpstream(d, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := GetUpstreamByName(d, "u"); err == nil {
		t.Fatal("deleted upstream still resolvable by name")
	}
	if _, err := GetUpstreamByID(d, uid); err == nil {
		t.Fatal("deleted upstream still resolvable by id")
	}
	list, _ := ListUpstreams(d, nil)
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
	AddModel(d, uid, UpstreamModel{ModelName: "m1"})
	AddModel(d, uid, UpstreamModel{ModelName: "m2"})

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
	if err := AddModel(d, uid, UpstreamModel{ModelName: "m1", Manual: true}); err != nil {
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
	if err := AddModel(d, uid, UpstreamModel{ModelName: "m", Manual: true}); err != nil {
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
	AddModel(d, uid, UpstreamModel{ModelName: "m"})
	models, _ := ListModels(d, uid)
	origID := models[0].ID
	if err := DeleteModel(d, origID); err != nil {
		t.Fatal(err)
	}
	if err := AddModel(d, uid, UpstreamModel{ModelName: "m", Manual: true, ContextLength: 1000, MaxOutputLength: 2000, Multimodal: true}); err != nil {
		t.Fatal(err)
	}
	models, _ = ListModels(d, uid)
	if len(models) != 1 || models[0].ID != origID || !models[0].Manual {
		t.Fatalf("revived=%+v", models)
	}
	if models[0].ContextLength != 1000 || models[0].MaxOutputLength != 2000 {
		t.Fatalf("lengths not applied: %+v", models[0])
	}
	if !models[0].Multimodal {
		t.Fatalf("multimodal not applied on revive: %+v", models[0])
	}
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM upstream_models WHERE model_name='m'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("duplicate rows: n=%d err=%v", n, err)
	}
}

// TestModelMultimodal 盯住 multimodal 字段的完整生命周期：默认否、UpdateModel
// 可改写、ReplaceModels 重新拉取时保留已配置的值、新出现的模型回落默认。
func TestModelMultimodal(t *testing.T) {
	d := testDB(t)
	uid, _ := CreateUpstream(d, &Upstream{Name: "u", BaseURL: "b", APIKey: "k", Format: "openai"})

	// 新增默认非多模态
	if err := AddModel(d, uid, UpstreamModel{ModelName: "m1"}); err != nil {
		t.Fatal(err)
	}
	models, _ := ListModels(d, uid)
	if len(models) != 1 || models[0].Multimodal {
		t.Fatalf("default should be non-multimodal: %+v", models)
	}

	// UpdateModel 打开开关
	if err := UpdateModel(d, uid, UpstreamModel{ID: models[0].ID, ContextLength: models[0].ContextLength, MaxOutputLength: models[0].MaxOutputLength, Multimodal: true}); err != nil {
		t.Fatal(err)
	}
	models, _ = ListModels(d, uid)
	if !models[0].Multimodal {
		t.Fatalf("after update: %+v", models[0])
	}

	// 重新拉取（m1 保留、m2 新增）：m1 的标记保留，m2 默认否
	if err := ReplaceModels(d, uid, []string{"m1", "m2"}); err != nil {
		t.Fatal(err)
	}
	models, _ = ListModels(d, uid)
	byName := map[string]UpstreamModel{}
	for _, m := range models {
		byName[m.ModelName] = m
	}
	if !byName["m1"].Multimodal {
		t.Fatalf("multimodal lost after re-sync: %+v", byName["m1"])
	}
	if byName["m2"].Multimodal {
		t.Fatalf("new synced model should default to non-multimodal: %+v", byName["m2"])
	}

	// 软删除后复活同样以新值为准（AddModel 复活路径）
	if err := DeleteModel(d, byName["m1"].ID); err != nil {
		t.Fatal(err)
	}
	if err := AddModel(d, uid, UpstreamModel{ModelName: "m1", Manual: true}); err != nil {
		t.Fatal(err)
	}
	models, _ = ListModels(d, uid)
	for _, m := range models {
		if m.ModelName == "m1" && m.Multimodal {
			t.Fatalf("revive via AddModel should apply the new value: %+v", m)
		}
	}
}

// 模型暂时从上游列表消失（被软删）再回来时，ReplaceModels 的复活要恢复它
// 最后的管理端配置（长度、multimodal），而不是落回默认值——快照只读活跃行
// 就丢了死行上的配置。
func TestReplaceModelsRevivePreservesConfig(t *testing.T) {
	d := testDB(t)
	uid, _ := CreateUpstream(d, &Upstream{Name: "u", BaseURL: "b", APIKey: "k", Format: "openai"})
	if err := AddModel(d, uid, UpstreamModel{ModelName: "m1"}); err != nil {
		t.Fatal(err)
	}
	// 管理员手改配置（走精确替换接口设值，避开与本测试无关的函数签名）
	if err := ReplaceModelsExact(d, uid, []UpstreamModel{
		{ModelName: "m1", ContextLength: 1111, MaxOutputLength: 222, Multimodal: true},
	}); err != nil {
		t.Fatal(err)
	}
	// 上游列表暂时不含 m1 → 软删
	if err := ReplaceModels(d, uid, []string{}); err != nil {
		t.Fatal(err)
	}
	// m1 回来 → 复活必须带回 1111/222/true
	if err := ReplaceModels(d, uid, []string{"m1"}); err != nil {
		t.Fatal(err)
	}
	models, _ := ListModels(d, uid)
	if len(models) != 1 {
		t.Fatalf("models=%+v", models)
	}
	m := models[0]
	if m.ContextLength != 1111 || m.MaxOutputLength != 222 || !m.Multimodal {
		t.Fatalf("revived model lost its curated config: %+v", m)
	}

	// 对照组：只被删除过的手动模型不挡复活路（死行 manual=1 不算「活跃手动行」）
	if err := AddModel(d, uid, UpstreamModel{ModelName: "m2", Manual: true, ContextLength: 500, MaxOutputLength: 50}); err != nil {
		t.Fatal(err)
	}
	ms, _ := ListModels(d, uid)
	for _, m := range ms {
		if m.ModelName == "m2" {
			if err := DeleteModel(d, m.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	// m2 出现在上游列表里：以自动行插入（死的手动行不阻止）
	if err := ReplaceModels(d, uid, []string{"m1", "m2"}); err != nil {
		t.Fatal(err)
	}
	models, _ = ListModels(d, uid)
	byName := map[string]UpstreamModel{}
	for _, m := range models {
		byName[m.ModelName] = m
	}
	if len(models) != 2 || byName["m2"].Manual {
		t.Fatalf("dead manual row should not block re-sync: %+v", models)
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

// TestUpstreamDisableEnable verifies 禁用与删除的差别：行与模型都保留
// （含按名解析，网关靠 Enabled 字段返回明确错误），重新启用即恢复。
func TestUpstreamDisableEnable(t *testing.T) {
	d := testDB(t)
	uid, _ := CreateUpstream(d, &Upstream{Name: "u", BaseURL: "b", APIKey: "k", Format: "openai"})
	AddModel(d, uid, UpstreamModel{ModelName: "m1"})

	// 新建默认启用
	u, _ := GetUpstreamByID(d, uid)
	if !u.Enabled {
		t.Fatal("new upstream should default to enabled")
	}

	// 禁用：行仍可查（含按名），模型不受影响（未 tombstone）
	u.Enabled = false
	if err := UpdateUpstream(d, u); err != nil {
		t.Fatal(err)
	}
	got, _ := GetUpstreamByID(d, uid)
	if got.Enabled {
		t.Fatal("upstream should be disabled")
	}
	byName, err := GetUpstreamByName(d, "u")
	if err != nil || byName.Enabled {
		t.Fatalf("byName=%+v err=%v", byName, err)
	}
	list, _ := ListUpstreams(d, nil)
	if len(list) != 1 || list[0].Enabled {
		t.Fatalf("list=%+v", list)
	}
	models, _ := ListModels(d, uid)
	if len(models) != 1 {
		t.Fatalf("models after disable len=%d", len(models))
	}

	// 重新启用恢复
	got.Enabled = true
	if err := UpdateUpstream(d, got); err != nil {
		t.Fatal(err)
	}
	again, _ := GetUpstreamByID(d, uid)
	if !again.Enabled {
		t.Fatal("upstream should be re-enabled")
	}
}

// 添加已存在的活跃模型返回 ErrModelExists：静默 200 会让管理员以为新配的字段
// （长度、多模态）生效了，实际什么都没改。软删行的同名复活不受影响（另一条
// 测试覆盖）。
func TestAddModelDuplicateRejected(t *testing.T) {
	d := testDB(t)
	uid, _ := CreateUpstream(d, &Upstream{Name: "u", BaseURL: "b", APIKey: "k", Format: "openai"})
	if err := AddModel(d, uid, UpstreamModel{ModelName: "m1"}); err != nil {
		t.Fatal(err)
	}
	if err := AddModel(d, uid, UpstreamModel{ModelName: "m1", Manual: true, ContextLength: 1000, MaxOutputLength: 100, Multimodal: true}); !errors.Is(err, ErrModelExists) {
		t.Fatalf("duplicate add: err=%v, want ErrModelExists", err)
	}
	// 原行未被改动
	models, _ := ListModels(d, uid)
	if len(models) != 1 || models[0].ContextLength != DefaultModelContextLength || models[0].Multimodal {
		t.Fatalf("duplicate add should not touch the existing row: %+v", models)
	}
}

// UpdateModel 限定 upstream_id：跨上游或不存在的模型返回 ErrNoRows 而不是假
// 成功（更新 0 行也 200 会让管理端以为保存成功）。
func TestUpdateModelScopedByUpstream(t *testing.T) {
	d := testDB(t)
	u1, _ := CreateUpstream(d, &Upstream{Name: "u1", BaseURL: "b", APIKey: "k", Format: "openai"})
	u2, _ := CreateUpstream(d, &Upstream{Name: "u2", BaseURL: "b", APIKey: "k", Format: "openai"})
	if err := AddModel(d, u1, UpstreamModel{ModelName: "m1"}); err != nil {
		t.Fatal(err)
	}
	models, _ := ListModels(d, u1)
	mid := models[0].ID

	if err := UpdateModel(d, u2, UpstreamModel{ID: mid, ContextLength: 1000, MaxOutputLength: 100, Multimodal: true}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-upstream update: err=%v, want ErrNoRows", err)
	}
	if err := UpdateModel(d, u1, UpstreamModel{ID: mid + 9999, ContextLength: 1000, MaxOutputLength: 100}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing model: err=%v, want ErrNoRows", err)
	}
	// 跨上游那次不能改动原行
	models, _ = ListModels(d, u1)
	if models[0].ContextLength != DefaultModelContextLength || models[0].Multimodal {
		t.Fatalf("cross-upstream update leaked: %+v", models[0])
	}
	// 正常更新仍然成功
	if err := UpdateModel(d, u1, UpstreamModel{ID: mid, ContextLength: 1000, MaxOutputLength: 100, Multimodal: true}); err != nil {
		t.Fatal(err)
	}
	models, _ = ListModels(d, u1)
	if models[0].ContextLength != 1000 || !models[0].Multimodal {
		t.Fatalf("legit update did not land: %+v", models[0])
	}
}
