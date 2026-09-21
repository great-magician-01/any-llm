package model

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// pinTTL 固定缓存 TTL 并在用例结束后还原。TTL 是包级 var（测试共用进程），
// 而「库外改动在 TTL 内不可见」「命中路径」这类断言依赖它，显式钉住。
func pinTTL(t *testing.T, d time.Duration) {
	t.Helper()
	old := configCacheTTL
	configCacheTTL = d
	t.Cleanup(func() { configCacheTTL = old })
}

// cacheEntryOf 读某个命名空间里 key 对应的条目（含 loadedAt），供断言缓存内部
// 状态——有些事实（计时有没有重置、失败后条目还在不在）只能这么看。
func cacheEntryOf[T any](n *ns[T], key string) (cacheEntry[T], bool) {
	cfgCache.RLock()
	defer cfgCache.RUnlock()
	e, ok := n.entries[key]
	return e, ok
}

// 配置读缓存的行为契约：
//   - 首次读查库并进内存，之后走内存（库关了照样读到）；
//   - 绕过写路径改库不会反映到缓存——这正是「纯写时失效」的代价，测试把它钉住，
//     防止后人随手加个 TTL 或反向同步把语义改糊；
//   - 走本包写路径改库，下一个读立刻看到新值；
//   - 返回的是副本，调用方改不到缓存里的条目。

// ext key：读穿透、副本隔离、写路径刷新、删除后未命中。
func TestCachedExtKey(t *testing.T) {
	pinTTL(t, time.Hour)
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
	pinTTL(t, time.Hour)
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
	pinTTL(t, time.Hour)
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

// TTL 到期后按未命中处理：重新查库、重新入库、重置计时。这是写时失效之外的第二
// 道保险——万一某条写路径漏了失效（或库被库外改动），最多陈旧一个 TTL 就自愈。
//
// 用 TTL=0 测「到期」（条目一读出来就已过期），全程不 sleep：CI 上机器很忙，
// 「sleep 120ms 对 50ms TTL」纯属碰运气，第一版就是这么红的。
func TestConfigCacheTTLRefresh(t *testing.T) {
	pinTTL(t, 0)

	d := testDB(t)
	uid, _ := CreateUpstream(d, &Upstream{Name: "u1", BaseURL: "https://a", APIKey: "k", Format: "openai"})
	k, _ := CreateExtKey(d, "l", "", 0, 0, nil)
	aliasID, _ := CreateAlias(d, &ModelAlias{Name: "fixed", Bindings: []AliasBinding{{UpstreamID: uid, ModelName: "m1"}}})

	// 预热三类缓存
	if _, err := CachedExtKey(d, k.Key); err != nil {
		t.Fatal(err)
	}
	if _, err := CachedUpstreamByName(d, "u1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := CachedAliasTargets(d, "fixed", time.Now()); err != nil {
		t.Fatal(err)
	}

	// 库外改动：TTL=0 意味着下一次读必然回库，立刻看到新值
	if _, err := d.Exec(`UPDATE ext_keys SET label='oob' WHERE id=?`, k.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE upstreams SET api_key='oob' WHERE id=?`, uid); err != nil {
		t.Fatal(err)
	}
	kk, _ := CachedExtKey(d, k.Key)
	if kk.Label != "oob" {
		t.Fatalf("ext key not refreshed after TTL: %q", kk.Label)
	}
	u, err := CachedUpstreamByName(d, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if u.APIKey != "oob" {
		t.Fatalf("upstream not refreshed after TTL: %q", u.APIKey)
	}

	// 别名也整条重查：把绑定就地置为不活跃（模拟上游删除时的级联软删），
	// 过期后应看到空链——这正是 TTL 作为安全网要兜住的场景。
	if _, err := d.Exec(`UPDATE model_alias_bindings SET is_active=0 WHERE alias_id=?`, aliasID); err != nil {
		t.Fatal(err)
	}
	found, targets, err := CachedAliasTargets(d, "fixed", time.Now())
	if err != nil || !found || len(targets) != 0 {
		t.Fatalf("alias not refreshed after TTL: found=%v targets=%+v err=%v", found, targets, err)
	}

	// 重查会重置计时：loadedAt 每次都往前推进。要是刷新时把旧时间戳留着，条目
	// 就永远停在「已过期」，每个请求都会白打一次库。
	before, ok := cacheEntryOf(&cfgCache.ups, "u1")
	if !ok {
		t.Fatal("upstream entry missing before refresh")
	}
	if _, err := CachedUpstreamByName(d, "u1"); err != nil {
		t.Fatal(err)
	}
	after, _ := cacheEntryOf(&cfgCache.ups, "u1")
	if !after.loadedAt.After(before.loadedAt) {
		t.Fatalf("refresh did not reset the TTL timer: before=%v after=%v", before.loadedAt, after.loadedAt)
	}
}

// TTL 到期但查库失败：保留旧条目，不把缓存打成空的（下一个请求自然重试）。
func TestConfigCacheTTLKeepsEntryOnDBError(t *testing.T) {
	pinTTL(t, 0)

	d := testDB(t)
	if _, err := CreateUpstream(d, &Upstream{Name: "u1", BaseURL: "https://a", APIKey: "k", Format: "openai"}); err != nil {
		t.Fatal(err)
	}
	if _, err := CachedUpstreamByName(d, "u1"); err != nil {
		t.Fatal(err)
	}
	// 关库：TTL=0 下任何读都必然回库，也就必然失败
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if u, err := CachedUpstreamByName(d, "u1"); err == nil {
		t.Fatalf("expected a DB error with a closed db, got %+v", u)
	}
	// 失败不能把好条目打掉：否则原本能命中的请求也要跟着失败
	e, ok := cacheEntryOf(&cfgCache.ups, "u1")
	if !ok || e.val.APIKey != "k" {
		t.Fatalf("failed refresh dropped the cached entry: ok=%v entry=%+v", ok, e)
	}
}

// 并发读 + 写路径刷新：-race 下必须干净（锁纪律与副本的正确性在此）。
func TestConfigCacheConcurrentReadWrite(t *testing.T) {
	// TTL 改小，让这一轮同时覆盖「过期重查 + singleflight + 写路径逐出」三条路
	old := configCacheTTL
	configCacheTTL = 30 * time.Millisecond
	t.Cleanup(func() { configCacheTTL = old })

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

// ---------------------------------------------------------------------------
// singleflight 与回填守卫：直接测 loadThrough 骨架，用可控的 resolve 精确制造交错
// ---------------------------------------------------------------------------

// 同一 key 同时未命中：只有一个 goroutine 真去查库，其余等 leader 回填后重取。
// 没有 singleflight 的话，这里会是 N 次查询（TTL 到期/冷启动瞬间的 herd）。
func TestLoadThroughSingleFlight(t *testing.T) {
	old := configCacheTTL
	configCacheTTL = time.Hour // 只用「未命中」路径，不掺 TTL
	t.Cleanup(func() { configCacheTTL = old })

	n := newNS[*ExtKey]()
	// 放一个已过期的条目，逼所有 goroutine 走未命中分支
	n.entries["k"] = cacheEntry[*ExtKey]{
		val:      &ExtKey{Key: "k", Label: "stale"},
		loadedAt: time.Now().Add(-2 * configCacheTTL),
	}

	var calls int64
	release := make(chan struct{})
	resolve := func() (*ExtKey, error) {
		atomic.AddInt64(&calls, 1)
		<-release // 卡住：保证 8 个 goroutine 都已排到队里再放行
		return &ExtKey{Key: "k", Label: "fresh"}, nil
	}

	const goroutines = 8
	got := make([]*ExtKey, goroutines)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			k, err := loadThrough(&n, "k", resolve, cloneExtKey)
			if err != nil {
				t.Errorf("loadThrough: %v", err)
				return
			}
			got[i] = k
		}(i)
	}
	time.Sleep(80 * time.Millisecond) // 等所有 goroutine 就位
	close(release)
	wg.Wait()

	if n := atomic.LoadInt64(&calls); n != 1 {
		t.Fatalf("resolve called %d times, want 1 (herd not collapsed)", n)
	}
	for i, k := range got {
		if k == nil || k.Label != "fresh" {
			t.Fatalf("goroutine %d got %+v, want the refreshed row", i, k)
		}
	}
	// in-flight 必须已清空：否则这个 key 的后续请求会永远堵在 <-ch 上
	if !inflightEmpty(&n) {
		t.Fatal("in-flight entry left behind after the leader finished")
	}
}

// leader 查库失败时，waiter 顺延成新 leader 自己重试——不会拿到假成功，也不会
// 永久堵住。
func TestLoadThroughLeaderFailureReleasesWaiters(t *testing.T) {
	old := configCacheTTL
	configCacheTTL = time.Hour
	t.Cleanup(func() { configCacheTTL = old })

	n := newNS[*ExtKey]()
	n.entries["k"] = cacheEntry[*ExtKey]{
		val:      &ExtKey{Key: "k", Label: "stale"},
		loadedAt: time.Now().Add(-2 * configCacheTTL),
	}

	var calls int64
	release := make(chan struct{})
	resolve := func() (*ExtKey, error) {
		if atomic.AddInt64(&calls, 1) == 1 {
			<-release // 第一个（leader）先卡住，让 waiter 排上队
			return nil, errors.New("db down")
		}
		return &ExtKey{Key: "k", Label: "fresh"}, nil
	}

	const goroutines = 4
	got := make([]*ExtKey, goroutines)
	errs := make([]error, goroutines)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got[i], errs[i] = loadThrough(&n, "k", resolve, cloneExtKey)
		}(i)
	}
	time.Sleep(80 * time.Millisecond)
	close(release)
	wg.Wait()

	// leader 自己必须把错误报回去（不能伪造成功）；waiter 顺延成新 leader 重试，
	// 拿到重查后的行——没有一个 goroutine 被永久堵住。
	var failed, ok int
	for i := range errs {
		switch {
		case errs[i] != nil:
			failed++
		case got[i] != nil && got[i].Label == "fresh":
			ok++
		default:
			t.Fatalf("goroutine %d got %+v err=%v, want either the error or the retried row", i, got[i], errs[i])
		}
	}
	if failed == 0 || ok == 0 {
		t.Fatalf("failed=%d ok=%d, want the leader to fail and the waiters to retry", failed, ok)
	}
	if n := atomic.LoadInt64(&calls); n < 2 {
		t.Fatalf("resolve called %d times, want the leader plus at least one retry", n)
	}
	if !inflightEmpty(&n) {
		t.Fatal("in-flight entry left behind after the leader failed")
	}
}

// CAS 守卫：查库期间条目被写路径改过，leader 读到的旧值不能盖回去。少了这道守卫，
// 「查库读到旧行 → 管理端写库 → 旧行才落缓存」会让一次改动沉寂整整一个 TTL。
func TestLoadThroughSkipsStaleStore(t *testing.T) {
	old := configCacheTTL
	configCacheTTL = time.Hour
	t.Cleanup(func() { configCacheTTL = old })

	for _, tc := range []struct {
		name    string
		preSeed bool // 事先有没有条目
		mutate  func(n *ns[*ExtKey])
	}{
		{
			name:    "写路径逐出后重建",
			preSeed: true,
			mutate: func(n *ns[*ExtKey]) {
				delete(n.entries, "k") // 逐出
				n.entries["k"] = cacheEntry[*ExtKey]{val: &ExtKey{Key: "k", Label: "written"}, loadedAt: time.Now()}
			},
		},
		{
			name:    "写路径原地刷新",
			preSeed: true,
			mutate: func(n *ns[*ExtKey]) {
				n.entries["k"] = cacheEntry[*ExtKey]{val: &ExtKey{Key: "k", Label: "written"}, loadedAt: time.Now()}
			},
		},
		{
			name:    "本来没有条目，查询期间被写出来",
			preSeed: false,
			mutate: func(n *ns[*ExtKey]) {
				n.entries["k"] = cacheEntry[*ExtKey]{val: &ExtKey{Key: "k", Label: "written"}, loadedAt: time.Now()}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := newNS[*ExtKey]()
			if tc.preSeed {
				n.entries["k"] = cacheEntry[*ExtKey]{
					val:      &ExtKey{Key: "k", Label: "stale"},
					loadedAt: time.Now().Add(-2 * configCacheTTL),
				}
			}
			queried := make(chan struct{})
			release := make(chan struct{})
			resolve := func() (*ExtKey, error) {
				close(queried)
				<-release // 让测试在这段时间里改缓存
				return &ExtKey{Key: "k", Label: "leader-read"}, nil
			}
			done := make(chan error, 1)
			go func() {
				_, err := loadThrough(&n, "k", resolve, cloneExtKey)
				done <- err
			}()
			<-queried
			tc.mutate(&n)
			close(release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			// 缓存里必须是写路径留下的值，而不是 leader 读到的旧值
			cfgCache.RLock()
			e, ok := n.entries["k"]
			cfgCache.RUnlock()
			if !ok || e.val.Label != "written" {
				t.Fatalf("stale store was not guarded: entry=%+v ok=%v", e, ok)
			}
		})
	}
}

// inflightEmpty 报告 ns 的 in-flight 表已清空。leader 无论正常返回还是 panic 都要
// 收尾，否则后续请求会永远堵在 <-ch 上。
func inflightEmpty[T any](n *ns[T]) bool {
	cfgCache.Lock()
	defer cfgCache.Unlock()
	return len(n.inflight) == 0
}
