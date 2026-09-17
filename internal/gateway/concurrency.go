package gateway

import (
	"sync"

	"github.com/great-magician-01/any-llm/internal/model"
)

// concManager 跟踪各上游的在途请求数：按上游 ID 一个信号量（带缓冲 channel
// 实现）。容量取自请求解析到的上游行的 MaxConcurrent——网关每请求重新读库，
// 管理端改上限后下一个请求即生效。上限变化时整体重建信号量：旧信号量上未
// 结束的请求仍释放回旧通道，故调低上限的瞬间在途数可能短暂超过新上限
// （不抢占中断在途请求）。
type concManager struct {
	mu   sync.Mutex
	sems map[int64]*concSem
}

type concSem struct {
	limit int
	ch    chan struct{}
}

func newConcManager() *concManager {
	return &concManager{sems: make(map[int64]*concSem)}
}

// tryAcquire 尝试为上游占一个并发槽，不阻塞：满则立即失败，调用方故障转移到
// 下一候选或回 429（与 token 限额「超限即拒」的口径一致）。MaxConcurrent <= 0
// 表示不限，直接成功。成功时返回释放函数；释放幂等，重复调用是空操作。
// 流式请求的槽位持有到上游流结束（连接存活期间都占并发数），非流式持有到
// Call 返回（响应体已完整读取）。
func (m *concManager) tryAcquire(u *model.Upstream) (release func(), ok bool) {
	if u.MaxConcurrent <= 0 {
		return func() {}, true
	}
	m.mu.Lock()
	s := m.sems[u.ID]
	if s == nil || s.limit != u.MaxConcurrent {
		s = &concSem{limit: u.MaxConcurrent, ch: make(chan struct{}, u.MaxConcurrent)}
		m.sems[u.ID] = s
	}
	m.mu.Unlock()
	select {
	case s.ch <- struct{}{}:
	default:
		return nil, false
	}
	var once sync.Once
	return func() { once.Do(func() { <-s.ch }) }, true
}
