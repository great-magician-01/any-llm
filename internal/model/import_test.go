package model

import (
	"database/sql"
	"errors"
	"testing"
)

// TestGetAliasByName 验证按名称取活跃别名（导入时同名覆盖需要）。
func TestGetAliasByName(t *testing.T) {
	d := testDB(t)
	uid, _ := CreateUpstream(d, &Upstream{Name: "u1", BaseURL: "b", APIKey: "k", Format: "openai"})
	id, _ := CreateAlias(d, &ModelAlias{Name: "fixed", Bindings: []AliasBinding{{UpstreamID: uid, ModelName: "m1"}}})

	got, err := GetAliasByName(d, "fixed")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != id || got.Name != "fixed" || len(got.Bindings) != 1 || got.Bindings[0].UpstreamID != uid {
		t.Fatalf("got=%+v", got)
	}

	if _, err := GetAliasByName(d, "nope"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unknown name err=%v", err)
	}

	// 软删后同名即「不存在」
	DeleteAlias(d, id)
	if _, err := GetAliasByName(d, "fixed"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("after delete err=%v", err)
	}
}

// TestReplaceModelsExact 验证把上游的活跃模型列表精确替换为给定集合
// （导入覆盖用）：旧模型全部下线，新列表按名字复活或插入，长度与
// manual 标记以传入为准；空列表清空。多次替换不累积行、不撞唯一索引。
func TestReplaceModelsExact(t *testing.T) {
	d := testDB(t)
	uid, _ := CreateUpstream(d, &Upstream{Name: "u", BaseURL: "b", APIKey: "k", Format: "openai"})
	AddModel(d, uid, "m1", false, 100, 200)
	AddModel(d, uid, "m2", true, 1, 2)

	mustList := func() map[string]UpstreamModel {
		t.Helper()
		list, err := ListModels(d, uid)
		if err != nil {
			t.Fatal(err)
		}
		m := make(map[string]UpstreamModel, len(list))
		for _, x := range list {
			m[x.ModelName] = x
		}
		return m
	}

	// m1 下线、m2 换长度、m3 新增
	err := ReplaceModelsExact(d, uid, []UpstreamModel{
		{ModelName: "m2", Manual: true, ContextLength: 5, MaxOutputLength: 6},
		{ModelName: "m3", Manual: false, ContextLength: 7, MaxOutputLength: 8},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := mustList()
	if len(got) != 2 {
		t.Fatalf("after replace=%+v", got)
	}
	if m, ok := got["m2"]; !ok || m.Manual != true || m.ContextLength != 5 || m.MaxOutputLength != 6 {
		t.Fatalf("m2=%+v ok=%v", m, ok)
	}
	if m, ok := got["m3"]; !ok || m.Manual != false || m.ContextLength != 7 || m.MaxOutputLength != 8 {
		t.Fatalf("m3=%+v ok=%v", m, ok)
	}
	if _, ok := got["m1"]; ok {
		t.Fatalf("m1 should be gone: %+v", got)
	}

	// 再替换回含 m1 的列表：复活软删行而不是新增重复行
	if err := ReplaceModelsExact(d, uid, []UpstreamModel{{ModelName: "m1", Manual: false, ContextLength: 9, MaxOutputLength: 10}}); err != nil {
		t.Fatal(err)
	}
	got = mustList()
	if len(got) != 1 || got["m1"].ContextLength != 9 || got["m1"].MaxOutputLength != 10 {
		t.Fatalf("after revive=%+v", got)
	}
	var rows int
	if err := d.QueryRow(`SELECT COUNT(*) FROM upstream_models WHERE upstream_id=?`, uid).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 3 { // m1, m2, m3 各一行（软删行不重复累积）
		t.Fatalf("total rows=%d", rows)
	}

	// 空列表：清空活跃模型
	if err := ReplaceModelsExact(d, uid, nil); err != nil {
		t.Fatal(err)
	}
	if got = mustList(); len(got) != 0 {
		t.Fatalf("after clear=%+v", got)
	}
}
