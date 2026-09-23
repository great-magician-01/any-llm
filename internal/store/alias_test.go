package store

import (
	"testing"
	"time"
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

	found, targets, err := ResolveAliasTargets(d, "fixed", time.Now())
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if len(targets) != 2 || targets[0].Upstream.Name != "u1" || targets[0].ModelName != "m1" || targets[1].Upstream.Name != "u2" || targets[1].ModelName != "m2" {
		t.Fatalf("targets=%+v", targets)
	}

	// 未知名称：found=false，调用方回落直连拆分
	found, targets, err = ResolveAliasTargets(d, "nope", time.Now())
	if err != nil || found || targets != nil {
		t.Fatalf("unknown: found=%v targets=%v err=%v", found, targets, err)
	}

	// 上游删除后其绑定被级联软删 → 解析时跳过
	if err := DeleteUpstream(d, uid1); err != nil {
		t.Fatal(err)
	}
	found, targets, err = ResolveAliasTargets(d, "fixed", time.Now())
	if err != nil || !found {
		t.Fatalf("after cascade: found=%v err=%v", found, err)
	}
	if len(targets) != 1 || targets[0].Upstream.Name != "u2" {
		t.Fatalf("after cascade targets=%+v", targets)
	}

	// 两个上游都删 → 别名存在但无可用绑定
	DeleteUpstream(d, uid2)
	found, targets, err = ResolveAliasTargets(d, "fixed", time.Now())
	if err != nil || !found || len(targets) != 0 {
		t.Fatalf("empty: found=%v targets=%v err=%v", found, targets, err)
	}
}

// TestResolveAliasSkipsDisabledUpstream 禁用（非删除）上游的绑定在网关解析时
// 被跳过、故障转移到下一候选；listBindings 仍返回绑定与上游名（管理端展示用），
// 仅 UpstreamEnabled=false。
func TestResolveAliasSkipsDisabledUpstream(t *testing.T) {
	d := testDB(t)
	uid1, _ := CreateUpstream(d, &Upstream{Name: "u1", BaseURL: "b", APIKey: "k", Format: "openai"})
	uid2, _ := CreateUpstream(d, &Upstream{Name: "u2", BaseURL: "b", APIKey: "k", Format: "anthropic"})
	id, _ := CreateAlias(d, &ModelAlias{Name: "fixed", Bindings: []AliasBinding{
		{UpstreamID: uid1, ModelName: "m1"},
		{UpstreamID: uid2, ModelName: "m2"},
	}})

	u1, _ := GetUpstreamByID(d, uid1)
	u1.Enabled = false
	if err := UpdateUpstream(d, u1); err != nil {
		t.Fatal(err)
	}

	found, targets, err := ResolveAliasTargets(d, "fixed", time.Now())
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if len(targets) != 1 || targets[0].Upstream.Name != "u2" || !targets[0].Upstream.Enabled {
		t.Fatalf("targets=%+v", targets)
	}

	// 管理端视角：绑定仍在、上游名完好，仅 UpstreamEnabled 标记
	got, _ := GetAliasByID(d, id)
	if len(got.Bindings) != 2 {
		t.Fatalf("bindings=%+v", got.Bindings)
	}
	if got.Bindings[0].UpstreamName != "u1" || got.Bindings[0].UpstreamEnabled {
		t.Fatalf("binding[0]=%+v", got.Bindings[0])
	}
	if !got.Bindings[1].UpstreamEnabled {
		t.Fatalf("binding[1]=%+v", got.Bindings[1])
	}

	// 全部禁用 → found=true 但无可用绑定
	u2, _ := GetUpstreamByID(d, uid2)
	u2.Enabled = false
	if err := UpdateUpstream(d, u2); err != nil {
		t.Fatal(err)
	}
	found, targets, err = ResolveAliasTargets(d, "fixed", time.Now())
	if err != nil || !found || len(targets) != 0 {
		t.Fatalf("all disabled: found=%v targets=%v err=%v", found, targets, err)
	}
}

// TestUpstreamExpiryRoundTrip verifies 有效期在建/改/查三条路径上往返，
// nil（永久）与具体时刻两种都能存。
func TestUpstreamExpiryRoundTrip(t *testing.T) {
	d := testDB(t)
	// 不设有效期 → NULL
	perm, _ := CreateUpstream(d, &Upstream{Name: "perm", BaseURL: "b", APIKey: "k", Format: "openai"})
	if u, _ := GetUpstreamByID(d, perm); u.ExpiresAt != nil {
		t.Fatalf("ExpiresAt=%v, want nil", u.ExpiresAt)
	}

	// 设了有效期 → 读回同一时刻
	at := time.Now().Add(24 * time.Hour).Truncate(time.Second)
	tmp, _ := CreateUpstream(d, &Upstream{Name: "tmp", BaseURL: "b", APIKey: "k", Format: "openai", ExpiresAt: &at})
	got, _ := GetUpstreamByID(d, tmp)
	if got.ExpiresAt == nil || !got.ExpiresAt.Equal(at) {
		t.Fatalf("ExpiresAt=%v, want %v", got.ExpiresAt, at)
	}
	// 按名解析（网关直连路径）同样带得到
	byName, _ := GetUpstreamByName(d, "tmp")
	if byName.ExpiresAt == nil || !byName.ExpiresAt.Equal(at) {
		t.Fatalf("byName ExpiresAt=%v, want %v", byName.ExpiresAt, at)
	}
	// 列表路径
	list, _ := ListUpstreams(d, nil)
	for _, u := range list {
		switch u.Name {
		case "perm":
			if u.ExpiresAt != nil {
				t.Fatalf("perm ExpiresAt=%v, want nil", u.ExpiresAt)
			}
		case "tmp":
			if u.ExpiresAt == nil || !u.ExpiresAt.Equal(at) {
				t.Fatalf("tmp ExpiresAt=%v, want %v", u.ExpiresAt, at)
			}
		}
	}

	// 续期与清除都通过 UpdateUpstream 生效（它是全量覆盖）
	later := at.Add(48 * time.Hour)
	got.ExpiresAt = &later
	if err := UpdateUpstream(d, got); err != nil {
		t.Fatal(err)
	}
	if u, _ := GetUpstreamByID(d, tmp); u.ExpiresAt == nil || !u.ExpiresAt.Equal(later) {
		t.Fatalf("after extend ExpiresAt=%v, want %v", u.ExpiresAt, later)
	}
	got.ExpiresAt = nil
	if err := UpdateUpstream(d, got); err != nil {
		t.Fatal(err)
	}
	if u, _ := GetUpstreamByID(d, tmp); u.ExpiresAt != nil {
		t.Fatalf("after clear ExpiresAt=%v, want nil", u.ExpiresAt)
	}
}

// TestUpstreamExpired 覆盖 Expired 的边界：未设置永久有效，到点即失效
// （含等于截止时刻的那一刻）。
func TestUpstreamExpired(t *testing.T) {
	at := time.Date(2026, 9, 21, 12, 0, 0, 0, time.Local)
	u := Upstream{Name: "u"}
	if u.Expired(at) {
		t.Fatal("nil ExpiresAt should never expire")
	}
	u.ExpiresAt = &at
	if u.Expired(at.Add(-time.Nanosecond)) {
		t.Fatal("before expiry should be usable")
	}
	if !u.Expired(at) {
		t.Fatal("exactly at expiry should be expired")
	}
	if !u.Expired(at.Add(time.Second)) {
		t.Fatal("after expiry should be expired")
	}
}

// TestResolveAliasSkipsExpiredUpstream 与禁用对称：已过有效期的绑定在网关
// 解析时被跳过、故障转移到下一候选；全部过期则 found=true 但无可用绑定。
// 判定用的是传入的 now，与行上的截止时刻比较，不读系统时钟。
func TestResolveAliasSkipsExpiredUpstream(t *testing.T) {
	d := testDB(t)
	uid1, _ := CreateUpstream(d, &Upstream{Name: "u1", BaseURL: "b", APIKey: "k", Format: "openai"})
	uid2, _ := CreateUpstream(d, &Upstream{Name: "u2", BaseURL: "b", APIKey: "k", Format: "anthropic"})
	CreateAlias(d, &ModelAlias{Name: "fixed", Bindings: []AliasBinding{
		{UpstreamID: uid1, ModelName: "m1"},
		{UpstreamID: uid2, ModelName: "m2"},
	}})

	// u1 明天到期：此刻两个候选都可用
	tomorrow := time.Now().Add(24 * time.Hour).Truncate(time.Second)
	u1, _ := GetUpstreamByID(d, uid1)
	u1.ExpiresAt = &tomorrow
	if err := UpdateUpstream(d, u1); err != nil {
		t.Fatal(err)
	}
	if found, targets, err := ResolveAliasTargets(d, "fixed", time.Now()); err != nil || !found || len(targets) != 2 {
		t.Fatalf("before expiry: found=%v targets=%d err=%v", found, len(targets), err)
	}

	// 过期后：u1 从候选链消失，落到 u2
	found, targets, err := ResolveAliasTargets(d, "fixed", tomorrow.Add(time.Second))
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if len(targets) != 1 || targets[0].Upstream.Name != "u2" {
		t.Fatalf("targets=%+v", targets)
	}

	// 全部过期 → 无可用绑定
	u2, _ := GetUpstreamByID(d, uid2)
	u2.ExpiresAt = &tomorrow
	if err := UpdateUpstream(d, u2); err != nil {
		t.Fatal(err)
	}
	if found, targets, err := ResolveAliasTargets(d, "fixed", tomorrow.Add(time.Second)); err != nil || !found || len(targets) != 0 {
		t.Fatalf("all expired: found=%v targets=%v err=%v", found, targets, err)
	}

	// 到期判定独立于 enabled：两个上游都仍启用，改有效期即可恢复
	u1.ExpiresAt = nil
	if err := UpdateUpstream(d, u1); err != nil {
		t.Fatal(err)
	}
	if found, targets, err := ResolveAliasTargets(d, "fixed", tomorrow.Add(time.Second)); err != nil || !found || len(targets) != 1 {
		t.Fatalf("after renewal: found=%v targets=%d err=%v", found, len(targets), err)
	}
}
