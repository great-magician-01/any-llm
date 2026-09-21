package model

import (
	"database/sql"
	"sync"
	"time"
)

// 网关配置读缓存（进程内、写时失效）。
//
// 背景：handleCompletion 每个请求都要读 ext key（鉴权）、别名绑定链、上游行，
// 而这些配置行在请求之间基本不变，却随 QPS 线性重复打库。这里把它们换成
// 「首次查库 → 进内存 → 后续走内存」的读缓存，写入侧完全不动。
//
// 设计约束（改这个文件前先读）：
//
//   - 一致性主要由写路径保证。所有配置写库都走本包的 Create/Update/Delete 系列
//     （管理端 CRUD 与配置导入全部复用它们），那些函数在写库成功后同步维护缓存，
//     且必然运行在 webapi 的 writeSync 闭包里——先落库、后改内存，中间没有窗口。
//     所以「管理端改完，下一个请求即生效」的语义与加缓存前一致。
//   - 查询绝不在持锁状态下执行：loadThrough 先 RLock 探一次，未命中就锁外查库。
//     争 leader 与回填各自只占锁一瞬间，查询本身不占。
//   - 并发未命中走 singleflight：同一个 key 同时未命中/过期时只有一个 goroutine
//     真去查库（leader），其余阻塞在 in-flight 上，leader 回填完再重取缓存。没有
//     这一层，TTL 到期或冷启动的瞬间会有 N 个并发请求各打一次库（thundering herd）。
//   - 回填带 CAS 式守卫：leader 动手前记下当时看到的条目，回填前比对——条目若在
//     查询期间被写过（写路径逐出/刷新，或别的 leader 已回填），说明库里已有更新的
//     值，leader 读到的旧值不能盖回去。否则「查库读到旧行 → 管理端写库 → 旧行才落
//     缓存」会让一次改动最多沉寂一个 TTL。守卫只依赖条目自身（每次 store 都写新的
//     loadedAt），不需要写路径配合，也就不存在「某个写函数忘了配合」的可能。
//   - 命中返回的是副本：网关多 goroutine 并发共享这份缓存，副本（含
//     AllowedModels slice 与 ExpiresAt 指向的 time）彻底消除别名与数据竞争。
//   - 不缓存否定结果：key / 上游名 / 别名未命中都不入库，否则任意随机字符串
//     就能把 map 撑大；未命中保持每请求查一次，与加缓存前一致。
//   - 别名候选缓存不过滤有效期：Upstream.Expired 判定在读时按调用方传入的 now
//     做，时间推进不需要失效任何条目。
//   - token 限额不走这里：checkKeyLimits / checkUpstreamLimits 仍每请求聚合
//     usage_records——那是用量数据，不是配置。
//   - 每条条目另有 1 小时 TTL（configCacheTTL）：到期后按未命中处理——重新查库、
//     重新入库、重置计时。写路径的刷新/逐出同样重置计时。TTL 是第二道保险：万一
//     某条写路径漏了失效（或库被库外改动），最多陈旧 1 小时就会自愈，而不是永久
//     错下去。查库失败时保留旧条目不动，下一个请求自然重试。
type aliasEntry struct {
	id         int64         // 别名行 ID：改名时旧名只有按 ID 扫才找得到
	found      bool          // 别名行存在且活跃（candidates 为空 = 存在但无可用绑定）
	candidates []AliasTarget // JOIN 出的原始候选，含已过有效期者；顺序即优先级
}

// configCacheTTL 缓存条目寿命。固定 1 小时，不做成可配置项——它只是写时失效之外
// 的第二道保险，调它只会让「陈旧窗口」变长变短，没有别的语义。测试把它临时改小
// （见 cache_test.go），故这里是 var 而非 const。
var configCacheTTL = time.Hour

// cacheEntry 缓存条目：值 + 入库时间。loadedAt 用 time.Now() 取（带单调时钟读数），
// 过期判断走 time.Since，墙钟被改也不会让条目提前/永不过期。它同时充当版本号：
// 每次 store 都写新的 loadedAt，loadThrough 靠比对它判断「条目被我之后的人动过」。
type cacheEntry[T any] struct {
	val      T
	loadedAt time.Time
}

// entryExpired 条目是否已过 TTL。
func entryExpired(loadedAt time.Time) bool {
	return time.Since(loadedAt) >= configCacheTTL
}

// ns 一类缓存条目（ext key / 上游 / 别名）的条目表与进行中的回填表。三者是独立
// 命名空间：一个 ext key 字符串和一个上游名撞车毫无意义，in-flight 也各管各的。
type ns[T any] struct {
	entries  map[string]cacheEntry[T]
	inflight map[string]chan struct{} // key → leader 干完时的唤醒信号
}

func newNS[T any]() ns[T] {
	return ns[T]{entries: make(map[string]cacheEntry[T]), inflight: make(map[string]chan struct{})}
}

// cfgCache 包级读缓存（与 convShardCache 同一模式）。三个 map 的规模分别不超过
// 活跃 key 数 / 上游数 / 被请求过的别名数，均由 DB 内容决定，删除即逐出，
// 所以内存有界、无需淘汰策略。
var cfgCache = struct {
	sync.RWMutex
	keys    ns[*ExtKey]
	ups     ns[*Upstream]
	aliases ns[*aliasEntry]
}{keys: newNS[*ExtKey](), ups: newNS[*Upstream](), aliases: newNS[*aliasEntry]()}

// loadThrough 读穿透 + singleflight 的通用骨架，三类条目共用：
//
//   - 命中且未过期 → 返回副本；
//   - 未命中/过期 → 争 leader：leader 锁外查库并回填，waiter 阻塞在 in-flight 上，
//     leader 干完后重取（那时通常已命中）；leader 查库失败则 waiter 顺延成新 leader
//     自己重试，不会拿到假成功也不会永久堵住；
//   - 回填前比对基线条目，查询期间被写过就不盖（见文件头「CAS 式守卫」）。
//
// resolve 返回 error 表示不入库；返回零值 + nil error 表示「查了，没有这一行」
// （别名未命中走这条），同样不入库。T 约束为 comparable 就是为了能跟零值比——
// 三类条目都是指针，零值即「没有」。
func loadThrough[T comparable](n *ns[T], key string, resolve func() (T, error), clone func(T) T) (T, error) {
	for {
		cfgCache.RLock()
		e, ok := n.entries[key]
		fresh := ok && !entryExpired(e.loadedAt)
		cfgCache.RUnlock()
		if fresh {
			return clone(e.val), nil
		}

		cfgCache.Lock()
		// 双检：抢锁期间可能已被 leader 或写路径填好
		e, ok = n.entries[key]
		if ok && !entryExpired(e.loadedAt) {
			v := e.val
			cfgCache.Unlock()
			return clone(v), nil
		}
		// 基线：回填前若条目变成了别的（loadedAt 变了，或从无到有/从有到无），
		// 说明查询期间有人写过，旧值不能盖回去。
		baseline := e
		if ch, busy := n.inflight[key]; busy {
			cfgCache.Unlock()
			<-ch // 等 leader 回填完，回环重取
			continue
		}
		ch := make(chan struct{})
		n.inflight[key] = ch
		cfgCache.Unlock()

		val, err := func() (T, error) {
			// 正常返回还是 panic，都要摘掉 in-flight 并唤醒 waiter，否则这个 key
			// 的后续请求会永远堵在 <-ch 上。
			defer func() {
				cfgCache.Lock()
				delete(n.inflight, key)
				cfgCache.Unlock()
				close(ch)
			}()
			val, err := resolve() // 锁外查库
			var zero T
			if err != nil || val == zero {
				return val, err
			}
			cfgCache.Lock()
			cur, curOK := n.entries[key]
			if curOK == (baseline.val != zero) && cur.loadedAt == baseline.loadedAt {
				n.entries[key] = cacheEntry[T]{val: clone(val), loadedAt: time.Now()}
			}
			cfgCache.Unlock()
			return val, nil
		}()
		if err != nil {
			var zero T
			return zero, err
		}
		return val, nil
	}
}

// CachedExtKey 按 key 字符串读 ext key（网关鉴权热路径）。未命中或已过 TTL 就回库
// 重填；行不存在时原样返回 DB 错误（调用方判 401），且不写缓存。
func CachedExtKey(d *sql.DB, key string) (*ExtKey, error) {
	return loadThrough(&cfgCache.keys, key,
		func() (*ExtKey, error) { return GetExtKey(d, key) },
		cloneExtKey)
}

// CachedUpstreamByName 按名称读上游行（网关直连路由 name/model）。语义同
// CachedExtKey：未命中或过期回库重填，不存在则返回 DB 错误且不缓存。
func CachedUpstreamByName(d *sql.DB, name string) (*Upstream, error) {
	return loadThrough(&cfgCache.ups, name,
		func() (*Upstream, error) { return GetUpstreamByName(d, name) },
		cloneUpstream)
}

// CachedAliasTargets 按对外名称解析别名候选链（网关别名路由）。返回值与
// ResolveAliasTargets 一致：found=false 表示没有这个别名（调用方回落直连拆分），
// found=true 但 targets 为空表示别名存在却无可用绑定。
//
// 缓存的是未过滤有效期的原始候选；过期判定（Upstream.Expired）在这里按 now 做，
// 所以一个已缓存的别名不需要因为「时间走过 expires_at」而被失效——那与条目自身
// 的 TTL 是两回事，后者到期会连候选一起重查。
func CachedAliasTargets(d *sql.DB, name string, now time.Time) (found bool, targets []AliasTarget, err error) {
	entry, err := loadThrough(&cfgCache.aliases, name,
		func() (*aliasEntry, error) {
			found, id, candidates, err := resolveAliasCandidates(d, name)
			if err != nil || !found {
				// 未命中不入库：调用方回落直连拆分，且不能给任意字符串占缓存
				return nil, err
			}
			return &aliasEntry{id: id, found: true, candidates: candidates}, nil
		},
		// 别名条目入库后不再改动，且 liveTargets 会把候选连上游一起拷成副本返回
		// 给调用方，所以这里不需要再拷一份。
		func(e *aliasEntry) *aliasEntry { return e })
	if err != nil {
		return false, nil, err
	}
	if entry == nil { // 别名不存在
		return false, nil, nil
	}
	return entry.found, liveTargets(entry.candidates, now), nil
}

// liveTargets 从候选链里滤掉已过有效期的，返回副本。候选按入库时的
// （priority, id）顺序排列，过滤不改变相对次序，故障转移语义不变。
func liveTargets(candidates []AliasTarget, now time.Time) []AliasTarget {
	out := make([]AliasTarget, 0, len(candidates))
	for _, t := range candidates {
		if t.Upstream.Expired(now) {
			continue
		}
		t.Upstream = cloneUpstream(t.Upstream)
		out = append(out, t)
	}
	return out
}

// ---------------------------------------------------------------------------
// 写路径维护（只被本包的 Create/Update/Delete 系列调用，均在写库成功后）
// ---------------------------------------------------------------------------

// putExtKey 把整行放入缓存。用于新建——行已在手，无需再查一次。
func putExtKey(k *ExtKey) {
	cfgCache.Lock()
	cfgCache.keys.entries[k.Key] = cacheEntry[*ExtKey]{val: cloneExtKey(k), loadedAt: time.Now()}
	cfgCache.Unlock()
}

// refreshExtKey 按 ID 读回整行并回填，用于更新（管理端 PATCH 路径，多一次
// SELECT 无所谓，换来缓存是热的）。读不回（并发删除、DB 错误）就逐出——
// 逐出永远安全，下一个请求自然回库。
func refreshExtKey(d *sql.DB, id int64) {
	k, err := GetExtKeyByID(d, id)
	cfgCache.Lock()
	defer cfgCache.Unlock()
	evictExtKeyIDLocked(id)
	if err != nil {
		return
	}
	cfgCache.keys.entries[k.Key] = cacheEntry[*ExtKey]{val: cloneExtKey(k), loadedAt: time.Now()}
}

// evictExtKeyByID 逐出该 ID 的缓存条目。缓存以 key 字符串为索引，而删除场景
// 手上只有 ID，故线性扫一遍——条目数不超过活跃 key 数，且只在管理端写入路径
// 上发生。
func evictExtKeyByID(id int64) {
	cfgCache.Lock()
	defer cfgCache.Unlock()
	evictExtKeyIDLocked(id)
}

func evictExtKeyIDLocked(id int64) {
	for key, e := range cfgCache.keys.entries {
		if e.val.ID == id {
			delete(cfgCache.keys.entries, key)
		}
	}
}

// evictUpstream 逐出上游缓存条目，并整份刷新别名缓存。上游写库后调用（改名 /
// 禁用 / 改 key / 改限额 / 删除）：别名候选里内嵌的就是完整的上游行，上游一变，
// 已解析的绑定链全部作废，整份刷新最不容易漏，而管理端写操作极低频、无所谓。
//
// id 与 name 至少有一个能定位到条目：改名时旧名只有按 ID 扫才找得到，删除时
// 则只有 ID。
func evictUpstream(id int64, name string) {
	cfgCache.Lock()
	defer cfgCache.Unlock()
	if name != "" {
		delete(cfgCache.ups.entries, name)
	}
	for n, e := range cfgCache.ups.entries {
		if e.val.ID == id {
			delete(cfgCache.ups.entries, n)
		}
	}
	cfgCache.aliases = newNS[*aliasEntry]()
}

// evictAliasByID 逐出该别名的缓存条目（按行 ID，覆盖改名前的旧名）。
func evictAliasByID(id int64) {
	cfgCache.Lock()
	defer cfgCache.Unlock()
	for name, e := range cfgCache.aliases.entries {
		if e.val.id == id {
			delete(cfgCache.aliases.entries, name)
		}
	}
}

// evictAliasByName 逐出该名称的缓存条目。
func evictAliasByName(name string) {
	cfgCache.Lock()
	defer cfgCache.Unlock()
	delete(cfgCache.aliases.entries, name)
}

// ---------------------------------------------------------------------------
// 副本
// ---------------------------------------------------------------------------

// cloneExtKey 深拷贝：AllowedModels 与 LastUsedAt 指向底层数据，共享会让调用方
// 有机会改到缓存里的条目（网关是并发读的）。
func cloneExtKey(k *ExtKey) *ExtKey {
	if k == nil {
		return nil
	}
	c := *k
	if k.AllowedModels != nil {
		c.AllowedModels = append([]string(nil), k.AllowedModels...)
	}
	if k.LastUsedAt != nil {
		t := *k.LastUsedAt
		c.LastUsedAt = &t
	}
	return &c
}

func cloneUpstream(u *Upstream) *Upstream {
	if u == nil {
		return nil
	}
	c := *u
	if u.ExpiresAt != nil {
		t := *u.ExpiresAt
		c.ExpiresAt = &t
	}
	return &c
}

func cloneTargets(targets []AliasTarget) []AliasTarget {
	if targets == nil {
		return nil
	}
	out := make([]AliasTarget, len(targets))
	for i, t := range targets {
		t.Upstream = cloneUpstream(t.Upstream)
		out[i] = t
	}
	return out
}

// ResetConfigCache 清空读缓存。测试隔离用：缓存是包级、进程内的，而每个测试
// 用例都是一个临时库，不清就会把上一个用例的条目带到下一个用例。生产代码不调用
// 它——一致性由写路径保证，缓存本就是惰性回填的。
func ResetConfigCache() {
	resetConfigCache()
}

// resetConfigCache 清空读缓存（内部实现）。
func resetConfigCache() {
	cfgCache.Lock()
	defer cfgCache.Unlock()
	cfgCache.keys = newNS[*ExtKey]()
	cfgCache.ups = newNS[*Upstream]()
	cfgCache.aliases = newNS[*aliasEntry]()
}
