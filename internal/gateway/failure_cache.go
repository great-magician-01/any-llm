package gateway

import (
	"errors"
	"sync"
	"time"

	"github.com/great-magician-01/any-llm/internal/logger"
	"github.com/great-magician-01/any-llm/internal/store"
	"github.com/great-magician-01/any-llm/internal/upstream"
)

// failureCacheTTL 是故障候选的冷却时长：候选（上游+模型）发生一次可缓存的
// 失败后在这段时间内被跳过，请求直接故障转移到下一候选，避免故障上游在每个
// 请求上都被重打一遍。
const failureCacheTTL = 60 * time.Second

// allCandidatesDownMessage 是全部候选都处于故障冷却期时的 503 文案（与并发
// 上限的 429 文案同款口径：提示稍后重试，SDK 会自动重试）。
const allCandidatesDownMessage = "all upstream candidates recently failed and are in failure cooldown, please retry later"

// failKey 标识一个可故障转移的候选。粒度是「上游+模型」而不是整个上游：
// 同一上游的不同模型互不影响（单个模型下线/限流不代表上游整体不可用）。
type failKey struct {
	upstreamID int64
	model      string
}

// failureCache 记录各候选最近一次调度失败的冷却截止时刻（纯内存态，进程
// 重启即清空，与并发信号量同为 Gateway 级）。命中冷却的候选在派发时被跳过，
// 跳过本身不发上游调用、不记 usage（与并发跳过一致）。
//
// 条目惰性过期：冷却结束后的下一个请求照常尝试该候选——成功即 clear 恢复
// 全量流量，失败则重新冷却一个 TTL。因此上游持续故障时每个 TTL 只被真实
// 命中一次；全部候选都在冷却中时直接回 503，不穿透到上游。
type failureCache struct {
	mu    sync.Mutex
	until map[failKey]time.Time
}

func newFailureCache() *failureCache {
	return &failureCache{until: make(map[failKey]time.Time)}
}

// failureCacheable 判定一次候选调用失败是否值得进冷却缓存。传输层错误
// （超时/DNS/连接拒绝等非 UpstreamError）、5xx、以及 401/402/403/429（密钥
// 失效、欠费、封禁、上游限流）都是「该候选对后续请求同样不可用」的信号；
// 其余 4xx（400/404/422…）是请求级错误，换一条请求可能成功，不缓存，
// 否则一条畸形请求会把候选链整体关掉一个 TTL。
func failureCacheable(err error) bool {
	var ue *upstream.UpstreamError
	if !errors.As(err, &ue) {
		return true
	}
	if ue.StatusCode >= 500 {
		return true
	}
	switch ue.StatusCode {
	case 401, 402, 403, 429:
		return true
	}
	return false
}

// mark 在候选发生可缓存失败时记录冷却（再次失败顺延一个 TTL）。
func (c *failureCache) mark(t *store.AliasTarget, err error, now time.Time) {
	if !failureCacheable(err) {
		return
	}
	k := failKey{upstreamID: t.Upstream.ID, model: t.ModelName}
	c.mu.Lock()
	c.until[k] = now.Add(failureCacheTTL)
	c.mu.Unlock()
	logger.Warn("candidate marked failed, entering failure cooldown",
		"upstream", t.Upstream.Name, "model", t.ModelName, "ttl", failureCacheTTL.String(), "err", truncate(err.Error(), 200))
}

// clear 在候选调用成功后清除其冷却记录：冷却到期后的探测一旦成功，立即
// 恢复全量流量，不再等 TTL 自然衰减。
func (c *failureCache) clear(t *store.AliasTarget) {
	k := failKey{upstreamID: t.Upstream.ID, model: t.ModelName}
	c.mu.Lock()
	delete(c.until, k)
	c.mu.Unlock()
}

// down 报告候选当前是否处于冷却期；顺带删掉已自然过期的条目，避免 map 随
// 上游×模型的组合数只增不减。
func (c *failureCache) down(t *store.AliasTarget, now time.Time) bool {
	k := failKey{upstreamID: t.Upstream.ID, model: t.ModelName}
	c.mu.Lock()
	u, ok := c.until[k]
	if ok && now.Before(u) {
		c.mu.Unlock()
		return true
	}
	if ok {
		delete(c.until, k)
	}
	c.mu.Unlock()
	return false
}

// logFailureSkip 记录一次因故障冷却被跳过的候选：非流式派发、流式预占、
// 流式失败转移三处共用同一组日志字段（与 logConcurrencySkip 对齐）。
func logFailureSkip(msg string, i int, t *store.AliasTarget) {
	logger.Info(msg, "alias_candidate", i, "upstream", t.Upstream.Name, "model", t.ModelName, "cooldown", failureCacheTTL.String())
}
