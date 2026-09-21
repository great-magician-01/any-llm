package model

import (
	"sync"
	"testing"
	"time"
)

// 配置读缓存的行为契约：
//   - 首次读查库并进内存，之后走内存（库关了照样读到）；
//   - 绕过写路径改库不会反映到缓存——这正是「纯写时失效」的代价，测试把它钉住，
//     防止后人随手加个 TTL 或反向同步把语义改糊；
//   - 走本包写路径改库，下一个读立刻看到新值；
//   - 返回的是副本，调用方改不到缓存里的条目。

// ext key：读穿透、副本隔离、写路径刷新、删除后未命中。
func TestCachedExtKey(t *testing.T) {
	d := testDB(t)
	k, err := CreateExtKey(d, "k1", "remark", 0, 0, []string{"oai/gpt-4o"})
	if err != nil {
		t.Fatal(err)
	}

	got, err := CachedExtKey(d, k.Key)
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	if got.ID != k.ID || got.Label != "k1" || len(got.AllowedModels) != 1 {
		t.Fatalf("got=%+v", got)
	}

	// 副本隔离：改返回值不能污染缓存（网关是并发读的）。
	got.Enabled = false
	got.AllowedModels[0] = "hacked"
	again, _ := CachedExtKey(d, k.Key)
	if !again.Enabled || again.AllowedModels[0] != "oai/gpt-4o" {
		t.Fatalf("cache entry mutated through returned copy: %+v", again)
	}

	// 绕过写路径直接改库：缓存必须仍是旧值，证明读真的走了内存。
	if _, err := d.Exec(`UPDATE ext_keys SET enabled=0, label='out-of-band' WHERE id=?`, k.ID); err != nil {
		t.Fatal(err)
	}
	stale, _ := CachedExtKey(d, k.Key)
	if !stale.Enabled || stale.Label != "k1" {
		t.Fatalf("out-of-band DB change leaked into cache: %+v", stale)
	}

	// 走写路径更新：下一个缓存读立刻是新值。
	if err := UpdateExtKey(d, k.ID, "k1-renamed", "remark", false, 0, 0, nil); err != nil {
		t.Fatal(err)
	}
	fresh, _ := CachedExtKey(d, k.Key)
	if fresh.Enabled || fresh.Label != "k1-renamed" || fresh.AllowedModels != nil {
		t.Fatalf("UpdateExtKey did not refresh cache: %+v", fresh)
	}

	// 删除后未命中（回到查库，自然落空）。
	if err := DeleteExtKey(d, k.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := CachedExtKey(d, k.Key); err == nil {
		t.Fatal("deleted key still resolvable")
	}
}

// 新建的 key 落库即入缓存：不需要第二个请求来暖缓存。
func TestCachedExtKeyWarmOnCreate(t *testing.T) {
	d := testDB(t)
	k, err := CreateExtKey(d, "warm", "", 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 关掉数据库：缓存命中不该碰库。
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := CachedExtKey(d, k.Key)
	if err != nil {
		t.Fatalf("cache miss after create: %v", err)
	}
	if got.Label != "warm" {
		t.Fatalf("got=%+v", got)
	}
}

// 上游行：读穿透、副本隔离、改名后旧名未命中、删除后重建同名必须拿到新行。
func TestCachedUpstreamByName(t *testing.T) {
	d := testDB(t)
	id, err := CreateUpstream(d, &Upstream{Name: "u1", BaseURL: "https://a", APIKey: "k", Format: "openai", MaxConcurrent: 7})
	if err != nil {
		t.Fatal(err)
	}

	got, err := CachedUpstreamByName(d, "u1")
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	if got.MaxConcurrent != 7 || got.Format != "openai" {
		t.Fatalf("got=%+v", got)
	}
	got.MaxConcurrent = 999
	again, _ := CachedUpstreamByName(d, "u1")
	if again.MaxConcurrent != 7 {
		t.Fatalf("cache entry mutated through returned copy: %+v", again)
	}

	// 改名：旧名未命中，新名命中
	nu, err := GetUpstreamByID(d, id)
	if err != nil {
		t.Fatal(err)
	}
	nu.Name = "u1-new"
	if err := UpdateUpstream(d, nu); err != nil {
		t.Fatal(err)
	}
	if _, err := CachedUpstreamByName(d, "u1"); err == nil {
		t.Fatal("old name still cached after rename")
	}
	if _, err := CachedUpstreamByName(d, "u1-new"); err != nil {
		t.Fatalf("new name not resolvable: %v", err)
	}

	// 禁用：缓存行同步反映
	cur, _ := GetUpstreamByID(d, id)
	cur.Enabled = false
	if err := UpdateUpstream(d, cur); err != nil {
		t.Fatal(err)
	}
	off, _ := CachedUpstreamByName(d, "u1-new")
	if off.Enabled {
		t.Fatal("disabled upstream still enabled in cache")
	}

	// 删除后重建同名：必须拿到新行（旧条目不逐出会把新行整个遮蔽掉）
	if err := DeleteUpstream(d, id); err != nil {
		t.Fatal(err)
	}
	id2, err := CreateUpstream(d, &Upstream{Name: "u1-new", BaseURL: "https://b", APIKey: "k2", Format: "anthropic"})
	if err != nil {
		t.Fatal(err)
	}
	rebuilt, err := CachedUpstreamByName(d, "u1-new")
	if err != nil {
		t.Fatalf("recreated upstream not resolvable: %v", err)
	}
	if rebuilt.ID != id2 || rebuilt.APIKey != "k2" || rebuilt.Format != "anthropic" {
		t.Fatalf("stale row shadowed the recreated upstream: %+v", rebuilt)
	}
}

// 别名：读穿透、过期在读时判定、上游改动整份刷新、改名后旧名未命中。
func TestCachedAliasTargets(t *testing.T) {
	d := testDB(t)
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)
	u1, err := CreateUpstream(d, &Upstream{Name: "u1", BaseURL: "https://a", APIKey: "k", Format: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	u2, err := CreateUpstream(d, &Upstream{Name: "u2", BaseURL: "https://b", APIKey: "k", Format: "anthropic", ExpiresAt: &past})
	if err != nil {
		t.Fatal(err)
	}
	u3, err := CreateUpstream(d, &Upstream{Name: "u3", BaseURL: "https://c", APIKey: "k", Format: "responses", ExpiresAt: &future})
	if err != nil {
		t.Fatal(err)
	}
	aliasID, err := CreateAlias(d, &ModelAlias{Name: "fixed", Bindings: []AliasBinding{
		{UpstreamID: u1, ModelName: "m1"},
		{UpstreamID: u2, ModelName: "m2"},
		{UpstreamID: u3, ModelName: "m3"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	// 另一个只绑 u1 的别名，用来验证「候选一个都不剩」时 found 仍为 true。
	if _, err := CreateAlias(d, &ModelAlias{Name: "solo", Bindings: []AliasBinding{{UpstreamID: u1, ModelName: "m1"}}}); err != nil {
		t.Fatal(err)
	}

	// u2 已过有效期：读时滤掉，只剩 u1 与 u3
	found, targets, err := CachedAliasTargets(d, "fixed", time.Now())
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if len(targets) != 2 || targets[0].Upstream.Name != "u1" || targets[1].Upstream.Name != "u3" {
		t.Fatalf("targets=%+v", targets)
	}

	// 同一条缓存条目，调用方传一个更晚的 now → u3 也被滤掉。过期判定在读时按
	// now 做，所以「时间走过 expires_at」不需要失效任何条目。
	// 同时钉住命中路径的 found：缓存条目存的就是「别名存在」，与候选数无关。
	hitFound, targets, err := CachedAliasTargets(d, "fixed", future.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !hitFound {
		t.Fatal("cache hit lost found=true")
	}
	if len(targets) != 1 || targets[0].Upstream.Name != "u1" {
		t.Fatalf("expiry should be evaluated at read time: %+v", targets)
	}

	// 副本隔离（同样是命中路径）
	hitFound, targets, _ = CachedAliasTargets(d, "fixed", time.Now())
	if !hitFound {
		t.Fatal("cache hit lost found=true")
	}
	targets[0].Upstream.Name = "hacked"
	_, targets, _ = CachedAliasTargets(d, "fixed", time.Now())
	if targets[0].Upstream.Name != "u1" {
		t.Fatalf("cache entry mutated through returned copy: %+v", targets[0])
	}

	// 禁用 u1：别名缓存整份刷新，solo 一个候选都不剩（found 仍为 true → 网关报
	// 「has no available bindings」而非 not found）
	cur, _ := GetUpstreamByID(d, u1)
	cur.Enabled = false
	if err := UpdateUpstream(d, cur); err != nil {
		t.Fatal(err)
	}
	found, targets, err = CachedAliasTargets(d, "solo", time.Now())
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if len(targets) != 0 {
		t.Fatalf("disabled upstream should drop out of the chain: %+v", targets)
	}

	// 未知别名：found=false，且不入库
	found, targets, err = CachedAliasTargets(d, "nope", time.Now())
	if err != nil || found || targets != nil {
		t.Fatalf("unknown alias: found=%v targets=%v err=%v", found, targets, err)
	}

	// 改名：旧名未命中（缓存以名称为键，旧名只能按 ID 逐出），新名命中
	// （此时只剩 u3 可用：u1 已禁用、u2 已过期）
	al, err := GetAliasByID(d, aliasID)
	if err != nil {
		t.Fatal(err)
	}
	al.Name = "fixed-renamed"
	if err := UpdateAlias(d, al); err != nil {
		t.Fatal(err)
	}
	if found, _, _ := CachedAliasTargets(d, "fixed", time.Now()); found {
		t.Fatal("old alias name still cached after rename")
	}
	if found, targets, _ := CachedAliasTargets(d, "fixed-renamed", time.Now()); !found || len(targets) != 1 || targets[0].ModelName != "m3" {
		t.Fatalf("renamed alias: found=%v targets=%+v", found, targets)
	}

	// 删除后未命中
	if err := DeleteAlias(d, aliasID); err != nil {
		t.Fatal(err)
	}
	if found, _, _ := CachedAliasTargets(d, "fixed-renamed", time.Now()); found {
		t.Fatal("deleted alias still resolvable")
	}
}

// 并发读 + 写路径刷新：-race 下必须干净（锁纪律与副本的正确性在此）。
func TestConfigCacheConcurrentReadWrite(t *testing.T) {
	d := testDB(t)
	uid, _ := CreateUpstream(d, &Upstream{Name: "u1", BaseURL: "https://a", APIKey: "k", Format: "openai"})
	k, _ := CreateExtKey(d, "l", "", 0, 0, nil)
	CreateAlias(d, &ModelAlias{Name: "fixed", Bindings: []AliasBinding{{UpstreamID: uid, ModelName: "m1"}}})

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					CachedExtKey(d, k.Key)
					CachedUpstreamByName(d, "u1")
					CachedAliasTargets(d, "fixed", time.Now())
				}
			}
		}()
	}
	// 写路径单独一个 goroutine 串着改：SQLite 不耐并发写，这里只关心缓存锁。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 0; ; n++ {
			select {
			case <-stop:
				return
			default:
				UpdateExtKey(d, k.ID, "l", "", true, n, 0, nil)
				u, err := GetUpstreamByID(d, uid)
				if err != nil {
					continue
				}
				u.MaxConcurrent = n
				UpdateUpstream(d, u)
			}
		}
	}()
	time.Sleep(100 * time.Millisecond)
	close(stop)
	wg.Wait()
}
