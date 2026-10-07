package store

import (
	"database/sql"
	"testing"
	"time"
)

// 本文件钉住配置读缓存的**写路径失效矩阵**：哪些写必须让缓存失效/刷新，哪些
// 写故意不碰缓存。一致性是写时优先的——管理端改完，下一个请求就必须看到新值；
// 只有 TouchExtKey（last_used_at）是刻意的例外，它为它失效等于每请求都回源。

// cacheGens 读三个命名空间的写路径代数（回填守卫的基线）。
func cacheGens() (keys, ups, aliases uint64) {
	cfgCache.RLock()
	defer cfgCache.RUnlock()
	return cfgCache.keysGen, cfgCache.upsGen, cfgCache.aliasesGen
}

// aliasEntryCount 当前缓存里的别名条目数。上游写操作会**整表重置**别名命名空间
// （map 被就地换成新的），这条断言看的就是「整份作废」而不是逐条逐出。
func aliasEntryCount() int {
	cfgCache.RLock()
	defer cfgCache.RUnlock()
	return len(cfgCache.aliases.entries)
}

// upsEntryCount 当前缓存里的上游条目数。
func upsEntryCount() int {
	cfgCache.RLock()
	defer cfgCache.RUnlock()
	return len(cfgCache.ups.entries)
}

// 规则：TouchExtKey 只写 last_used_at，该列不参与任何热路径判定（鉴权看
// key/enabled，路由看 allowed_models），因此**不得**让缓存失效——否则每个
// 网关请求都会把 ext key 缓存打掉，读缓存等于不存在。
func TestCacheInvalidationTouchExtKeyKeepsCacheWarm(t *testing.T) {
	pinTTL(t, time.Hour)
	d := testDB(t)

	k, err := CreateExtKey(d, "k1", "r", 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 预热（CreateExtKey 已经 putExtKey，这里再读一次确认命中路径）
	if _, err := CachedExtKey(d, k.Key); err != nil {
		t.Fatal(err)
	}
	entryBefore, ok := cacheEntryOf(&cfgCache.keys, k.Key)
	if !ok {
		t.Fatal("新建的 key 应在缓存里")
	}
	keysGenBefore, _, _ := cacheGens()

	// 库外改 label：如果接下来的读回源了，就会看到这个值。
	if _, err := d.Exec(`UPDATE ext_keys SET label='out-of-band' WHERE id=?`, k.ID); err != nil {
		t.Fatal(err)
	}

	if err := TouchExtKey(d, k.ID); err != nil {
		t.Fatal(err)
	}

	// 库里确实写进了 last_used_at（Touch 的效果）
	var lastUsed sql.NullTime
	if err := d.QueryRow(`SELECT last_used_at FROM ext_keys WHERE id=?`, k.ID).Scan(&lastUsed); err != nil {
		t.Fatal(err)
	}
	if !lastUsed.Valid {
		t.Fatal("TouchExtKey 应写入 last_used_at")
	}

	// 缓存条目与代数都没被碰过：失效的可见标志就是 loadedAt 变新 / 代数递增
	entryAfter, ok := cacheEntryOf(&cfgCache.keys, k.Key)
	if !ok {
		t.Fatal("TouchExtKey 把缓存条目逐出了；它不该影响缓存")
	}
	if !entryAfter.loadedAt.Equal(entryBefore.loadedAt) {
		t.Fatalf("loadedAt 变了（%v → %v）：TouchExtKey 使缓存失效了", entryBefore.loadedAt, entryAfter.loadedAt)
	}
	if keysGenAfter, _, _ := cacheGens(); keysGenAfter != keysGenBefore {
		t.Fatalf("keys 代数 %d → %d：TouchExtKey 递增了代数（等于声明条目已作废）", keysGenBefore, keysGenAfter)
	}

	// 命中路径：既没有回源（label 仍是旧的），LastUsedAt 也保持入库时的值（nil）。
	got, err := CachedExtKey(d, k.Key)
	if err != nil {
		t.Fatal(err)
	}
	if got.Label != "k1" {
		t.Fatalf("label=%q：Touch 之后读回源了（应为缓存命中，看到入库时的 k1）", got.Label)
	}
	if got.LastUsedAt != nil {
		t.Fatalf("LastUsedAt=%v：Touch 不该刷新缓存条目里的 last_used_at", got.LastUsedAt)
	}
}

// 规则：UpdateExtKey 之后下一次读必须拿到新值，且**不回源**——refreshExtKey 在
// 写库成功后读回整行并回填，所以关掉数据库也能读到新行。若改成只逐出，这个
// 断言会以「查库失败」暴露出来。
func TestCacheInvalidationUpdateExtKeyRefreshesWithoutRequiringDB(t *testing.T) {
	pinTTL(t, time.Hour)
	d := testDB(t)

	k, err := CreateExtKey(d, "k1", "r", 0, 0, []string{"oai/gpt-4o"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CachedExtKey(d, k.Key); err != nil {
		t.Fatal(err)
	}
	keysGenBefore, _, _ := cacheGens()

	if err := UpdateExtKey(d, k.ID, "k1-renamed", "note", false, 11, 22, []string{"oai/gpt-5"}); err != nil {
		t.Fatal(err)
	}
	// 写路径必须递增代数：并发回填守卫靠它拦住「查询期间写库」的旧值。
	if keysGenAfter, _, _ := cacheGens(); keysGenAfter == keysGenBefore {
		t.Fatal("UpdateExtKey 未递增 keys 代数，回填守卫会放行查询期间的旧行")
	}

	// 关库后读：只有「缓存里已经是新行」才可能成功。
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := CachedExtKey(d, k.Key)
	if err != nil {
		t.Fatalf("UpdateExtKey 之后必须能从缓存直接读出新行（不回源），实际: %v", err)
	}
	if got.Label != "k1-renamed" || got.Enabled || got.DailyTokenLimit != 11 || got.MonthlyTokenLimit != 22 ||
		got.Remark != "note" || len(got.AllowedModels) != 1 || got.AllowedModels[0] != "oai/gpt-5" {
		t.Fatalf("缓存里的行不是更新后的值: %+v", got)
	}
}

// 规则：UpdateUpstream / DeleteUpstream 之后
//   - 该上游按名的缓存条目立即消失（改名时旧名与新名都不能命中旧行）；
//   - 别名命名空间**整表重置**：别名候选里内嵌的是完整的上游行，上游一变，
//     所有已解析的绑定链都作废（逐条按 ID 找不足以覆盖改名与级联软删）。
func TestCacheInvalidationUpstreamWritesResetAliasNamespace(t *testing.T) {
	pinTTL(t, time.Hour)
	d := testDB(t)

	past := time.Now().Add(-time.Hour)
	u1, err := CreateUpstream(d, &Upstream{Name: "u1", BaseURL: "https://a", APIKey: "k1", Format: "openai", MaxConcurrent: 7, ExpiresAt: &past})
	if err != nil {
		t.Fatal(err)
	}
	aliasID, err := CreateAlias(d, &ModelAlias{Name: "fixed", Bindings: []AliasBinding{{UpstreamID: u1, ModelName: "m1"}}})
	if err != nil {
		t.Fatal(err)
	}

	// 预热三类缓存（别名条目必须真的存在，否则「整表重置」断言无意义）
	if _, err := CachedUpstreamByName(d, "u1"); err != nil {
		t.Fatal(err)
	}
	found, targets, err := CachedAliasTargets(d, "fixed", time.Now())
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	// u1 已过有效期：候选被读时滤掉，但**缓存条目本身**仍在（未过滤的原始候选）。
	if len(targets) != 0 {
		t.Fatalf("过期上游的候选应被读时滤掉: %+v", targets)
	}
	if aliasEntryCount() == 0 {
		t.Fatal("别名缓存条目未建立，无法验证整表重置")
	}

	// --- UpdateUpstream：改名 + 续期 + 改并发上限 ---
	cur, err := GetUpstreamByID(d, u1)
	if err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	cur.APIKey = "k2"
	cur.MaxConcurrent = 42
	cur.ExpiresAt = &future
	if err := UpdateUpstream(d, cur); err != nil {
		t.Fatal(err)
	}
	if n := aliasEntryCount(); n != 0 {
		t.Fatalf("UpdateUpstream 后别名缓存仍有 %d 条：上游改动必须整份作废候选链", n)
	}
	if n := upsEntryCount(); n != 0 {
		t.Fatalf("UpdateUpstream 后上游缓存仍有 %d 条：旧行必须逐出", n)
	}
	// 下一个读必须看到新行（旧条目若还在，会返回旧 key/旧上限/旧有效期）
	u, err := CachedUpstreamByName(d, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if u.APIKey != "k2" || u.MaxConcurrent != 42 || u.Expired(time.Now()) {
		t.Fatalf("UpdateUpstream 后读到旧行: %+v", u)
	}
	found, targets, err = CachedAliasTargets(d, "fixed", time.Now())
	if err != nil || !found || len(targets) != 1 {
		t.Fatalf("found=%v targets=%+v err=%v", found, targets, err)
	}
	if targets[0].Upstream.APIKey != "k2" || targets[0].Upstream.MaxConcurrent != 42 {
		t.Fatalf("别名重新解析后仍拿到旧上游行: %+v", targets[0].Upstream)
	}
	if targets[0].ModelName != "m1" {
		t.Fatalf("绑定模型名=%q，期望 m1", targets[0].ModelName)
	}

	// 改名场景：旧名不能再命中缓存（按 ID 扫才能找到旧条目）
	cur, _ = GetUpstreamByID(d, u1)
	cur.Name = "u1-renamed"
	if err := UpdateUpstream(d, cur); err != nil {
		t.Fatal(err)
	}
	if _, err := CachedUpstreamByName(d, "u1"); err == nil {
		t.Fatal("改名后旧名仍能命中缓存")
	}
	if _, err := CachedUpstreamByName(d, "u1-renamed"); err != nil {
		t.Fatalf("改名后新名不可解析: %v", err)
	}

	// --- DeleteUpstream：别名条目连带作废（绑定被级联软删）---
	if _, _, err := CachedAliasTargets(d, "fixed", time.Now()); err != nil {
		t.Fatal(err)
	}
	if aliasEntryCount() == 0 {
		t.Fatal("别名缓存条目未重建")
	}
	if err := DeleteUpstream(d, u1); err != nil {
		t.Fatal(err)
	}
	if n := aliasEntryCount(); n != 0 {
		t.Fatalf("DeleteUpstream 后别名缓存仍有 %d 条", n)
	}
	if _, err := CachedUpstreamByName(d, "u1-renamed"); err == nil {
		t.Fatal("软删除的上游仍能按名命中缓存")
	}
	// 别名行还在，绑定被级联软删 → found=true 但候选为空（网关据此回
	// 「has no available bindings」而不是 not found）
	found, targets, err = CachedAliasTargets(d, "fixed", time.Now())
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if len(targets) != 0 {
		t.Fatalf("上游删除后候选链应为空: %+v", targets)
	}
	_ = aliasID
}

// 规则：CreateAlias / UpdateAlias / DeleteAlias 之后下一个读必须反映新状态，
// 尤其是「删掉同名别名再重建」——旧条目还在就会把新别名整个遮蔽掉。
func TestCacheInvalidationAliasWrites(t *testing.T) {
	pinTTL(t, time.Hour)
	d := testDB(t)

	u1, _ := CreateUpstream(d, &Upstream{Name: "u1", BaseURL: "https://a", APIKey: "k1", Format: "openai"})
	u2, _ := CreateUpstream(d, &Upstream{Name: "u2", BaseURL: "https://b", APIKey: "k2", Format: "anthropic"})

	// 第一次创建：写路径按名逐出（否定结果本就不入缓存，这条是防御性断言）
	id, err := CreateAlias(d, &ModelAlias{Name: "fixed", Bindings: []AliasBinding{{UpstreamID: u1, ModelName: "m1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if n := aliasEntryCount(); n != 0 {
		t.Fatalf("CreateAlias 后应立即逐出同名条目，实际仍有 %d 条", n)
	}
	if found, targets, err := CachedAliasTargets(d, "fixed", time.Now()); err != nil || !found ||
		len(targets) != 1 || targets[0].Upstream.Name != "u1" {
		t.Fatalf("创建后解析: found=%v targets=%+v err=%v", found, targets, err)
	}

	// 删除 → 立即未命中
	if err := DeleteAlias(d, id); err != nil {
		t.Fatal(err)
	}
	if n := aliasEntryCount(); n != 0 {
		t.Fatalf("DeleteAlias 后别名缓存仍有 %d 条", n)
	}
	if found, _, _ := CachedAliasTargets(d, "fixed", time.Now()); found {
		t.Fatal("删除后别名仍可解析（旧条目遮蔽）")
	}

	// 同名重建：必须拿到新别名的绑定，而不是删除前的旧条目
	id2, err := CreateAlias(d, &ModelAlias{Name: "fixed", Bindings: []AliasBinding{{UpstreamID: u2, ModelName: "m2"}}})
	if err != nil {
		t.Fatal(err)
	}
	found, targets, err := CachedAliasTargets(d, "fixed", time.Now())
	if err != nil || !found || len(targets) != 1 {
		t.Fatalf("重建后解析: found=%v targets=%+v err=%v", found, targets, err)
	}
	if targets[0].Upstream.Name != "u2" || targets[0].ModelName != "m2" {
		t.Fatalf("同名重建被旧缓存条目遮蔽: %+v", targets[0])
	}

	// UpdateAlias：改名 + 换绑定；旧名立即未命中，新名拿到新绑定
	al, err := GetAliasByID(d, id2)
	if err != nil {
		t.Fatal(err)
	}
	al.Name = "fixed-renamed"
	al.Bindings = []AliasBinding{{UpstreamID: u1, ModelName: "m3"}}
	if err := UpdateAlias(d, al); err != nil {
		t.Fatal(err)
	}
	if n := aliasEntryCount(); n != 0 {
		t.Fatalf("UpdateAlias 后别名缓存仍有 %d 条", n)
	}
	if found, _, _ := CachedAliasTargets(d, "fixed", time.Now()); found {
		t.Fatal("改名后旧名仍可解析")
	}
	found, targets, err = CachedAliasTargets(d, "fixed-renamed", time.Now())
	if err != nil || !found || len(targets) != 1 {
		t.Fatalf("改名后解析: found=%v targets=%+v err=%v", found, targets, err)
	}
	if targets[0].Upstream.Name != "u1" || targets[0].ModelName != "m3" {
		t.Fatalf("改名后的绑定不是新值: %+v", targets[0])
	}
}

// 规则：命中返回的必须是**深拷贝**——网关多 goroutine 共享这份缓存，调用方
// 通过返回的指针改到缓存内部就是数据竞争 + 配置漂移。这里专门覆盖指针字段
// （ExtKey.LastUsedAt、Upstream.ExpiresAt）与候选切片，它们是浅拷贝最常漏的地方。
func TestCacheHitDeepCopyIsolationOfPointerFields(t *testing.T) {
	pinTTL(t, time.Hour)
	d := testDB(t)

	// ext key：先让库里带上 last_used_at，再 Update（触发 refreshExtKey 回填整行）
	k, err := CreateExtKey(d, "k1", "r", 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := TouchExtKey(d, k.ID); err != nil {
		t.Fatal(err)
	}
	if err := UpdateExtKey(d, k.ID, "k1", "r", true, 0, 0, nil); err != nil {
		t.Fatal(err)
	}
	warm, err := CachedExtKey(d, k.Key)
	if err != nil {
		t.Fatal(err)
	}
	if warm.LastUsedAt == nil {
		t.Fatal("缓存行应带 last_used_at（库里已由 Touch 写入）")
	}
	moved := warm.LastUsedAt.Add(48 * time.Hour)
	*warm.LastUsedAt = moved // 通过返回值的指针改缓存内部数据
	warm.AllowedModels = append(warm.AllowedModels, "injected/model")
	again, err := CachedExtKey(d, k.Key)
	if err != nil {
		t.Fatal(err)
	}
	if again.LastUsedAt == nil || again.LastUsedAt.Equal(moved) {
		t.Fatalf("LastUsedAt 被调用方改到了缓存内部: %v", again.LastUsedAt)
	}
	if len(again.AllowedModels) != 0 {
		t.Fatalf("AllowedModels 被调用方改到了缓存内部: %v", again.AllowedModels)
	}

	// upstream：ExpiresAt 是指针，浅拷贝会让两个 goroutine 共享同一个 time
	exp := time.Now().Add(2 * time.Hour)
	uID, err := CreateUpstream(d, &Upstream{Name: "u1", BaseURL: "https://a", APIKey: "k", Format: "openai", ExpiresAt: &exp})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CachedUpstreamByName(d, "u1"); err != nil {
		t.Fatal(err)
	}
	gu, err := CachedUpstreamByName(d, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if gu.ExpiresAt == nil {
		t.Fatal("缓存行应带 expires_at")
	}
	*gu.ExpiresAt = time.Now().Add(240 * time.Hour)
	gu.Name = "hacked"
	gu2, err := CachedUpstreamByName(d, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if gu2.Name != "u1" {
		t.Fatalf("上游行被调用方改到了缓存内部: %+v", gu2)
	}
	if !gu2.ExpiresAt.Equal(exp) {
		t.Fatalf("ExpiresAt 被调用方改到了缓存内部: %v want %v", gu2.ExpiresAt, exp)
	}

	// 别名候选：liveTargets 返回的切片与其中内嵌的上游行都必须是副本
	if _, err := CreateAlias(d, &ModelAlias{Name: "fixed", Bindings: []AliasBinding{{UpstreamID: uID, ModelName: "m1"}}}); err != nil {
		t.Fatal(err)
	}
	found, targets, err := CachedAliasTargets(d, "fixed", time.Now())
	if err != nil || !found || len(targets) != 1 {
		t.Fatalf("found=%v targets=%+v err=%v", found, targets, err)
	}
	targets[0].ModelName = "hacked"
	targets[0].Upstream.Name = "hacked"
	*targets[0].Upstream.ExpiresAt = time.Now()
	found, targets2, err := CachedAliasTargets(d, "fixed", time.Now())
	if err != nil || !found || len(targets2) != 1 {
		t.Fatalf("found=%v targets=%+v err=%v", found, targets2, err)
	}
	if targets2[0].ModelName != "m1" || targets2[0].Upstream.Name != "u1" || !targets2[0].Upstream.ExpiresAt.Equal(exp) {
		t.Fatalf("别名候选被调用方改到了缓存内部: %+v", targets2[0])
	}
}
