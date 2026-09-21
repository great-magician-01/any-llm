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
//   - 一致性完全由写路径保证。所有配置写库都走本包的 Create/Update/Delete 系列
//     （管理端 CRUD 与配置导入全部复用它们），那些函数在写库成功后同步维护缓存，
//     且必然运行在 webapi 的 writeSync 闭包里——先落库、后改内存，中间没有窗口。
//     所以「管理端改完，下一个请求即生效」的语义与加缓存前一致。
//   - 代价：绕过 admin API 直接改库（手写 SQL、或将来的多实例共享一个库）不会
//     反映到缓存，需重启进程。纯写时失效，没有开关也没有 TTL，见 AGENTS.md。
//   - 查询绝不在持锁状态下执行：先 RLock 探一次，未命中则锁外查库，再 Lock 回填。
//     并发未命中会重复回填，内容相同、后写覆盖，无害。
//   - 命中返回的是副本：网关多 goroutine 并发共享这份缓存，副本（含
//     AllowedModels slice 与 ExpiresAt 指向的 time）彻底消除别名与数据竞争。
//   - 不缓存否定结果：key / 上游名 / 别名未命中都不入库，否则任意随机字符串
//     就能把 map 撑大；未命中保持每请求查一次，与加缓存前一致。
//   - 别名候选缓存不过滤有效期：Upstream.Expired 判定在读时按调用方传入的 now
//     做，时间推进不需要失效任何条目。
//   - token 限额不走这里：checkKeyLimits / checkUpstreamLimits 仍每请求聚合
//     usage_records——那是用量数据，不是配置。
type aliasEntry struct {
	id         int64         // 别名行 ID：改名时旧名只有按 ID 扫才找得到
	found      bool          // 别名行存在且活跃（candidates 为空 = 存在但无可用绑定）
	candidates []AliasTarget // JOIN 出的原始候选，含已过有效期者；顺序即优先级
}

// cfgCache 包级读缓存（与 convShardCache 同一模式）。三个 map 的规模分别不超过
// 活跃 key 数 / 上游数 / 被请求过的别名数，均由 DB 内容决定，删除即逐出，
// 所以内存有界、无需淘汰策略。
var cfgCache = struct {
	sync.RWMutex
	keys    map[string]*ExtKey
	ups     map[string]*Upstream
	aliases map[string]*aliasEntry
}{
	keys:    make(map[string]*ExtKey),
	ups:     make(map[string]*Upstream),
	aliases: make(map[string]*aliasEntry),
}

// CachedExtKey 按 key 字符串读 ext key（网关鉴权热路径）。未命中回库并缓存；
// 行不存在时原样返回 DB 错误（调用方判 401），且不写缓存。
func CachedExtKey(d *sql.DB, key string) (*ExtKey, error) {
	cfgCache.RLock()
	k, ok := cfgCache.keys[key]
	cfgCache.RUnlock()
	if ok {
		return cloneExtKey(k), nil
	}
	k, err := GetExtKey(d, key)
	if err != nil {
		return nil, err
	}
	cfgCache.Lock()
	cfgCache.keys[key] = cloneExtKey(k)
	cfgCache.Unlock()
	return k, nil
}

// CachedUpstreamByName 按名称读上游行（网关直连路由 name/model）。语义同
// CachedExtKey：未命中回库并缓存，不存在则返回 DB 错误且不缓存。
func CachedUpstreamByName(d *sql.DB, name string) (*Upstream, error) {
	cfgCache.RLock()
	u, ok := cfgCache.ups[name]
	cfgCache.RUnlock()
	if ok {
		return cloneUpstream(u), nil
	}
	u, err := GetUpstreamByName(d, name)
	if err != nil {
		return nil, err
	}
	cfgCache.Lock()
	cfgCache.ups[name] = cloneUpstream(u)
	cfgCache.Unlock()
	return u, nil
}

// CachedAliasTargets 按对外名称解析别名候选链（网关别名路由）。返回值与
// ResolveAliasTargets 一致：found=false 表示没有这个别名（调用方回落直连拆分），
// found=true 但 targets 为空表示别名存在却无可用绑定。
//
// 缓存的是未过滤有效期的原始候选；过期判定在这里按 now 做，所以一个已缓存的
// 别名不需要因为「时间走过 expires_at」而被失效。
func CachedAliasTargets(d *sql.DB, name string, now time.Time) (found bool, targets []AliasTarget, err error) {
	cfgCache.RLock()
	e, ok := cfgCache.aliases[name]
	cfgCache.RUnlock()
	if ok {
		return e.found, liveTargets(e.candidates, now), nil
	}
	found, id, candidates, err := resolveAliasCandidates(d, name)
	if err != nil || !found {
		return found, nil, err
	}
	cfgCache.Lock()
	cfgCache.aliases[name] = &aliasEntry{id: id, found: true, candidates: cloneTargets(candidates)}
	cfgCache.Unlock()
	return true, liveTargets(candidates, now), nil
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
	cfgCache.keys[k.Key] = cloneExtKey(k)
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
	cfgCache.keys[k.Key] = cloneExtKey(k)
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
	for key, k := range cfgCache.keys {
		if k.ID == id {
			delete(cfgCache.keys, key)
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
		delete(cfgCache.ups, name)
	}
	for n, u := range cfgCache.ups {
		if u.ID == id {
			delete(cfgCache.ups, n)
		}
	}
	cfgCache.aliases = make(map[string]*aliasEntry)
}

// evictAliasByID 逐出该别名的缓存条目（按行 ID，覆盖改名前的旧名）。
func evictAliasByID(id int64) {
	cfgCache.Lock()
	defer cfgCache.Unlock()
	for name, e := range cfgCache.aliases {
		if e.id == id {
			delete(cfgCache.aliases, name)
		}
	}
}

// evictAliasByName 逐出该名称的缓存条目。
func evictAliasByName(name string) {
	cfgCache.Lock()
	defer cfgCache.Unlock()
	delete(cfgCache.aliases, name)
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
	cfgCache.keys = make(map[string]*ExtKey)
	cfgCache.ups = make(map[string]*Upstream)
	cfgCache.aliases = make(map[string]*aliasEntry)
}
