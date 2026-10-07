package db

import (
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 本文件钉住 db.Writer 的三条边界契约（它们共同决定关服/积压时管理端写请求的
// 可见行为）：
//
//  1. 缓冲（容量 512）满时 DoAsync 静默丢弃且**不阻塞调用方**。DoAsync 的调用方
//     是网关请求线程与密钥触碰路径，丢一条用量记录可以接受，把请求线程挂在写
//     队列上不可接受。
//  2. DoSync 与 Stop 的竞争必须是确定的：抢在 closed 置位之前入队的 DoSync 一定
//     拿到自己的执行结果（成功或业务错误），Stop 之后提交的一定拿到
//     ErrWriterStopped（→ HTTP 503）。不能用「ok 的数量是 0..32 的任意分布」
//     来模糊覆盖。
//  3. 所有写任务严格串行（任何时刻至多一个在执行）且按入队顺序执行。

// wbTestWriter 建一个连着临时 SQLite 的 writer 并启动。前缀 wb 避免与同包既有
// 的 newTestWriter 重名。
func wbTestWriter(t *testing.T, bufSize int) *Writer {
	t.Helper()
	d, err := OpenSQLite(filepath.Join(t.TempDir(), "wb.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	w := NewWriter(d, bufSize)
	w.Start()
	return w
}

// wbWaitFor 轮询等待条件成立（仅用于「等某个确定会发生的状态」，不做时序假设）。
func wbWaitFor(t *testing.T, cond func() bool, timeout time.Duration, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("等待超时：%s", msg)
}

// 规则 1：容量 512 的缓冲被填满后，DoAsync 必须静默丢弃、立即返回，且队列不再
// 增长（不能被改写成阻塞式投递，也不能无界排队）。
func TestWriterDoAsyncDropsSilentlyWhenBufferFull(t *testing.T) {
	const capacity = 512
	w := wbTestWriter(t, capacity)

	var ran atomic.Int64
	noop := func(*sql.DB) error { ran.Add(1); return nil }

	// 占住唯一的写 goroutine：缓冲从此不再被消费，队列长度完全由测试控制。
	gate := make(chan struct{})
	started := make(chan struct{})
	w.DoAsync(func(*sql.DB) error {
		close(started)
		<-gate
		ran.Add(1)
		return nil
	})
	<-started

	// 填满缓冲：前 capacity 个任务都装得下（有空间 → 不阻塞）。
	for i := 0; i < capacity; i++ {
		w.DoAsync(noop)
	}
	if n := len(w.ch); n != capacity {
		t.Fatalf("填满后队列长度=%d，期望 %d", n, capacity)
	}

	// 第 capacity+1 个：必须走 default 静默丢弃。用超时守望它的返回——阻塞在
	// 满缓冲上就是网关被写队列拖死的那个故障。
	returned := make(chan struct{})
	go func() {
		w.DoAsync(noop)
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("DoAsync 在缓冲满时阻塞了；契约是静默丢弃并立即返回")
	}
	if n := len(w.ch); n != capacity {
		t.Fatalf("溢出提交后队列长度=%d，期望仍为 %d（应丢弃而不是排队）", n, capacity)
	}

	// 放行并排空：执行过的任务必须恰好是 1（占位）+ capacity（缓冲里的），
	// 溢出的那个从未入队，因此永远不会被执行。
	close(gate)
	w.Stop()
	if got := ran.Load(); got != 1+capacity {
		t.Fatalf("执行任务数=%d，期望 %d（溢出的那次提交必须被丢弃）", got, 1+capacity)
	}
}

// 规则 1（后半个）：Stop 之后 DoAsync 不 panic、不执行、不入队。它跑在关服
// 尾部，任何 panic 都会把优雅退出变成崩溃。
func TestWriterDoAsyncAfterStopDropsWithoutPanic(t *testing.T) {
	w := wbTestWriter(t, 8)
	w.Stop()

	var ran atomic.Bool
	w.DoAsync(func(*sql.DB) error { ran.Store(true); return nil })
	if ran.Load() {
		t.Fatal("Stop 之后 DoAsync 不应执行任务")
	}
	if n := len(w.ch); n != 0 {
		t.Fatalf("Stop 之后队列长度=%d，期望 0（不入队）", n)
	}
	// 再 Stop 一次也必须幂等无 panic
	w.Stop()
}

// 规则 2：Stop 与 in-flight DoSync 并发。用同包可见的内部通道把交错钉死：
// 先卡住 worker，再确定性地让一个 DoSync 排进队列，然后才调 Stop。
// 结论是确定的——这个 DoSync 必然成功返回 nil，Stop 必须等它做完；Stop 之后
// 的新 DoSync 必然是 ErrWriterStopped。
func TestWriterStopDuringInFlightSyncIsDeterministic(t *testing.T) {
	w := wbTestWriter(t, 4)

	// 卡住写 goroutine：它现在既不消费队列也不会退出。
	gate := make(chan struct{})
	started := make(chan struct{})
	w.DoAsync(func(*sql.DB) error {
		close(started)
		<-gate
		return nil
	})
	<-started

	// 直接往队列里塞一个已知任务，保证后面的 DoSync 一定排在它后面。
	// （worker 被 gate 卡住，不会消费；同包可直接操作 w.ch。）
	queued := make(chan struct{})
	w.ch <- writeReq{fn: func(*sql.DB) error { close(queued); return nil }}

	// 提交 DoSync 并等它真的入队（队列长度 2 = 已知任务 + 这次 DoSync）。
	// 此时它已经通过 closed 检查并登记进 inflight，处于「in flight」状态。
	inFlight := make(chan error, 1)
	go func() {
		inFlight <- w.DoSync(func(*sql.DB) error { return nil })
	}()
	wbWaitFor(t, func() bool { return len(w.ch) == 2 }, 5*time.Second,
		"DoSync 未把请求投进队列（无法确定它与 Stop 的交错）")

	stopDone := make(chan struct{})
	go func() {
		w.Stop()
		close(stopDone)
	}()

	// Stop 必须等待 in-flight 的 DoSync：worker 还卡在 gate 上，此刻它绝不能返回。
	select {
	case <-stopDone:
		t.Fatal("有 in-flight DoSync 时 Stop 提前返回了；它会遗留未决的写请求")
	case <-time.After(50 * time.Millisecond):
	}

	// 放行：先跑已知任务，再跑 in-flight 的 DoSync（它拿到 nil），Stop 随后收尾。
	close(gate)
	if err := <-inFlight; err != nil {
		t.Fatalf("in-flight DoSync 的返回值=%v，期望 nil（它是在 Stop 之前入队的）", err)
	}
	select {
	case <-stopDone:
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight DoSync 完成后 Stop 仍未返回")
	}
	select {
	case <-queued:
	case <-time.After(5 * time.Second):
		t.Fatal("排队在 DoSync 之前的任务没有执行；Stop 的排空把队列丢了")
	}

	// 关服后新提交的写请求：确定性 503 语义。
	if err := w.DoSync(func(*sql.DB) error { return nil }); !errors.Is(err, ErrWriterStopped) {
		t.Fatalf("Stop 之后 DoSync 的返回值=%v，期望 ErrWriterStopped", err)
	}
}

// 规则 3：并发提交的任务既不重叠（任何时刻至多一个在跑），也按入队顺序执行。
// 这是「所有写串行化」的核心契约——用量记录、密钥触碰与管理端写都靠它互斥。
func TestWriterSerializesInSubmitOrder(t *testing.T) {
	w := wbTestWriter(t, 512)
	defer w.Stop()

	const n = 200
	var (
		mu      sync.Mutex
		order   []int
		running atomic.Int32
		maxConc atomic.Int32
	)
	// 单 goroutine 顺序提交：入队顺序 = 0..n-1，执行顺序必须与之一致。
	for i := 0; i < n; i++ {
		i := i
		w.DoAsync(func(*sql.DB) error {
			if c := running.Add(1); c > maxConc.Load() {
				maxConc.Store(c)
			}
			time.Sleep(200 * time.Microsecond) // 实现若允许并发，这里必然重叠
			mu.Lock()
			order = append(order, i)
			mu.Unlock()
			running.Add(-1)
			return nil
		})
	}
	// 写屏障：DoSync 返回即表示此前入队的任务都已执行完（同一 goroutine 顺序消费）。
	if err := w.DoSync(func(*sql.DB) error { return nil }); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(order) != n {
		t.Fatalf("执行了 %d 个任务，期望 %d", len(order), n)
	}
	for i, v := range order {
		if v != i {
			t.Fatalf("第 %d 个执行的任务是 %d；writer 必须严格按入队顺序串行执行", i, v)
		}
	}
	if got := maxConc.Load(); got != 1 {
		t.Fatalf("最大并发=%d，期望 1（所有写必须串行化）", got)
	}
}

// 规则 3（多提交者版本）：来自多个 goroutine 的 DoSync 同样不得重叠执行。
// DoAsync 的调用方是并发的网关 goroutine，这条契约保证它们不会真的并发写库。
func TestWriterSerializesConcurrentSubmitters(t *testing.T) {
	w := wbTestWriter(t, 512)
	defer w.Stop()

	const goroutines, perGoroutine = 8, 25
	var (
		running atomic.Int32
		maxConc atomic.Int32
		done    atomic.Int64
	)
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				err := w.DoSync(func(*sql.DB) error {
					if c := running.Add(1); c > maxConc.Load() {
						maxConc.Store(c)
					}
					time.Sleep(100 * time.Microsecond)
					done.Add(1)
					running.Add(-1)
					return nil
				})
				if err != nil {
					t.Errorf("DoSync: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	if got := done.Load(); got != goroutines*perGoroutine {
		t.Fatalf("完成 %d 个任务，期望 %d", got, goroutines*perGoroutine)
	}
	if got := maxConc.Load(); got != 1 {
		t.Fatalf("最大并发=%d，期望 1（并发提交的写也必须串行化）", got)
	}
}
