package model

import (
	"testing"
)

func TestCreateAndGetAlias(t *testing.T) {
	d := testDB(t)
	uid1, _ := CreateUpstream(d, &Upstream{Name: "u1", BaseURL: "b", APIKey: "k", Format: "openai"})
	uid2, _ := CreateUpstream(d, &Upstream{Name: "u2", BaseURL: "b", APIKey: "k", Format: "anthropic"})

	id, err := CreateAlias(d, &ModelAlias{Name: "fixed-gpt", Bindings: []AliasBinding{
		{UpstreamID: uid1, ModelName: "gpt-4o"},
		{UpstreamID: uid2, ModelName: "claude-sonnet"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := GetAliasByID(d, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "fixed-gpt" || len(got.Bindings) != 2 {
		t.Fatalf("got=%+v", got)
	}
	// priority 按数组顺序归一化为 0,1；联查上游名
	if got.Bindings[0].Priority != 0 || got.Bindings[0].UpstreamName != "u1" || got.Bindings[0].ModelName != "gpt-4o" {
		t.Fatalf("binding[0]=%+v", got.Bindings[0])
	}
	if got.Bindings[1].Priority != 1 || got.Bindings[1].UpstreamName != "u2" {
		t.Fatalf("binding[1]=%+v", got.Bindings[1])
	}
}

func TestAliasUniqueName(t *testing.T) {
	d := testDB(t)
	uid, _ := CreateUpstream(d, &Upstream{Name: "u", BaseURL: "b", APIKey: "k", Format: "openai"})
	_, err := CreateAlias(d, &ModelAlias{Name: "dup", Bindings: []AliasBinding{{UpstreamID: uid, ModelName: "m"}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = CreateAlias(d, &ModelAlias{Name: "dup", Bindings: []AliasBinding{{UpstreamID: uid, ModelName: "m"}}})
	if err == nil {
		t.Fatal("expected duplicate alias name error")
	}
}

func TestUpdateAliasReplaceBindings(t *testing.T) {
	d := testDB(t)
	uid1, _ := CreateUpstream(d, &Upstream{Name: "u1", BaseURL: "b", APIKey: "k", Format: "openai"})
	uid2, _ := CreateUpstream(d, &Upstream{Name: "u2", BaseURL: "b", APIKey: "k", Format: "openai"})

	id, _ := CreateAlias(d, &ModelAlias{Name: "a", Bindings: []AliasBinding{
		{UpstreamID: uid1, ModelName: "m1"},
		{UpstreamID: uid2, ModelName: "m2"},
	}})
	err := UpdateAlias(d, &ModelAlias{ID: id, Name: "a2", Bindings: []AliasBinding{
		{UpstreamID: uid2, ModelName: "m3"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := GetAliasByID(d, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "a2" || len(got.Bindings) != 1 || got.Bindings[0].UpstreamID != uid2 || got.Bindings[0].ModelName != "m3" || got.Bindings[0].Priority != 0 {
		t.Fatalf("got=%+v", got)
	}
	// 替换后旧绑定已软删，可再次整体替换而不撞 (alias_id, priority) 唯一索引
	if err := UpdateAlias(d, &ModelAlias{ID: id, Name: "a2", Bindings: []AliasBinding{{UpstreamID: uid1, ModelName: "m1"}}}); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteAliasAndRecreate(t *testing.T) {
	d := testDB(t)
	uid, _ := CreateUpstream(d, &Upstream{Name: "u", BaseURL: "b", APIKey: "k", Format: "openai"})
	id, _ := CreateAlias(d, &ModelAlias{Name: "gone", Bindings: []AliasBinding{{UpstreamID: uid, ModelName: "m"}}})
	if err := DeleteAlias(d, id); err != nil {
		t.Fatal(err)
	}
	if _, err := GetAliasByID(d, id); err == nil {
		t.Fatal("deleted alias still readable")
	}
	// 软删不占唯一名额：同名可重建
	if _, err := CreateAlias(d, &ModelAlias{Name: "gone", Bindings: []AliasBinding{{UpstreamID: uid, ModelName: "m"}}}); err != nil {
		t.Fatalf("recreate after delete: %v", err)
	}
}

func TestResolveAliasTargets(t *testing.T) {
	d := testDB(t)
	uid1, _ := CreateUpstream(d, &Upstream{Name: "u1", BaseURL: "b1", APIKey: "k", Format: "openai"})
	uid2, _ := CreateUpstream(d, &Upstream{Name: "u2", BaseURL: "b2", APIKey: "k", Format: "anthropic"})
	CreateAlias(d, &ModelAlias{Name: "fixed", Bindings: []AliasBinding{
		{UpstreamID: uid1, ModelName: "m1"},
		{UpstreamID: uid2, ModelName: "m2"},
	}})

	found, targets, err := ResolveAliasTargets(d, "fixed")
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if len(targets) != 2 || targets[0].Upstream.Name != "u1" || targets[0].ModelName != "m1" || targets[1].Upstream.Name != "u2" || targets[1].ModelName != "m2" {
		t.Fatalf("targets=%+v", targets)
	}

	// 未知名称：found=false，调用方回落直连拆分
	found, targets, err = ResolveAliasTargets(d, "nope")
	if err != nil || found || targets != nil {
		t.Fatalf("unknown: found=%v targets=%v err=%v", found, targets, err)
	}

	// 上游删除后其绑定被级联软删 → 解析时跳过
	if err := DeleteUpstream(d, uid1); err != nil {
		t.Fatal(err)
	}
	found, targets, err = ResolveAliasTargets(d, "fixed")
	if err != nil || !found {
		t.Fatalf("after cascade: found=%v err=%v", found, err)
	}
	if len(targets) != 1 || targets[0].Upstream.Name != "u2" {
		t.Fatalf("after cascade targets=%+v", targets)
	}

	// 两个上游都删 → 别名存在但无可用绑定
	DeleteUpstream(d, uid2)
	found, targets, err = ResolveAliasTargets(d, "fixed")
	if err != nil || !found || len(targets) != 0 {
		t.Fatalf("empty: found=%v targets=%v err=%v", found, targets, err)
	}
}
