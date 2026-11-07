package gateway

import (
	"context"
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

// failureCacheTTLLabel 是冷却时长的固定文本（日志字段用），避免每次跳过/
// 记录都重新格式化 duration。
var failureCacheTTLLabel = failureCacheTTL.String()

// coolable 报告候选 i（共 n 个）是否参与故障冷却：「末尾候选始终实际调用、
// 透传真实上游异常」这条规则只有一个家——冷却的查询（down）与记录（mark）
// 都以它为准。直连路由与别名链末尾不受冷却影响，每个请求都会真实调用。
func coolable(i, n int) bool { return i < n-1 }

// failKey 标识一个可故障转移的候选。粒度是「上游+模型」而不是整个上游：
// 同一上游的不同模型互不影响（单个模型下线/限流不代表上游整体不可用）。
type failKey struct {
	upstreamID int64
	model      string
}

// failureCache 记录各候选最近一次调度失败的冷却截止时刻（纯内存态，进程
// 重启即清空，与并发信号量同为 Gateway 级）。命中冷却的非末尾候选在派发时
// 被跳过，跳过本身不发上游调用、不记 usage（与并发跳过一致）。
//
// 条目惰性过期：冷却结束后的下一个请求照常尝试该候选——成功即 clear 恢复
// 全量流量，失败则重新冷却一个 TTL。因此持续故障的非末尾候选每个 TTL 只被
// 真实命中一次；末尾候选不受冷却影响（coolable 为 false），每个请求都会
// 真实调用并透传其原始异常。
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
// 否则一条畸形请求会把候选链整体关掉一个 TTL。客户端主动终止也不在此列：
// 它随请求上下文取消冒出来（context.Canceled），是调用方原因而非候选故障，
// 不排除的话一次非流式断开会把整条候选链打进冷却（上下文已取消，后续候选
// 的调用只会立即失败、逐个被 mark）。
func failureCacheable(err error) bool {
	// 只有本侧取消才产生 context.Canceled（上游回不了它；网关自身的上游
	// 超时是 DeadlineExceeded，仍按传输层错误进冷却），故可精确识别断开。
	if errors.Is(err, context.Canceled) {
		return false
	}
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

// mark 在候选发生可缓存失败时记录冷却（再次失败顺延一个 TTL）。只为
// hasSuccessor（coolable）为真的候选记录：末尾候选的冷却从不被 down() 查询，
// 记了也只会沉淀成永不过期的死条目和无人响应的 WARN——直连路由的 model 名
// 来自客户端输入，无条件记录会让 map 随任意 model 名无界增长。
func (c *failureCache) mark(t *store.AliasTarget, err error, now time.Time, hasSuccessor bool) {
	if !hasSuccessor || !failureCacheable(err) {
		return
	}
	k := failKey{upstreamID: t.Upstream.ID, model: t.ModelName}
	c.mu.Lock()
	c.until[k] = now.Add(failureCacheTTL)
	c.mu.Unlock()
	logger.Warn("candidate marked failed, entering failure cooldown",
		"upstream", t.Upstream.Name, "model", t.ModelName, "ttl", failureCacheTTLLabel, "err", truncate(err.Error(), 200))
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
	logger.Info(msg, "alias_candidate", i, "upstream", t.Upstream.Name, "model", t.ModelName, "cooldown", failureCacheTTLLabel)
}
