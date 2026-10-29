package upstream

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/great-magician-01/any-llm/internal/db"
	"github.com/great-magician-01/any-llm/internal/store"
)

// 契约测试：store.RecordBalanceSnapshot 按「本包 FetchBalance 归一化的 payload」
// 记账。balancePayload/balanceEntry 是本包的非导出类型，store 侧解析用的是自己
// 的最小结构——任一侧改了字段名或 kind 字面量（如 "balance" 改名），自动记账会
// 静默停摆且各自的单测都发现不了（两边的测试各自手写 JSON 字面量）。这里用
// 真实的归一化输出喂给 store，跨包契约一断就红。
func TestBalancePayloadContractWithStoreBilling(t *testing.T) {
	store.ResetConfigCache()
	t.Cleanup(store.ResetConfigCache)
	d, err := db.OpenSQLite(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })

	const (
		prevBody = `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"110.00","granted_balance":"10.00","topped_up_balance":"100.00"}]}`
		newBody  = `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"106.90","granted_balance":"10.00","topped_up_balance":"96.90"}]}`
	)
	prevPayload, err := normalizeDeepSeekBalance([]byte(prevBody))
	if err != nil {
		t.Fatal(err)
	}
	newPayload, err := normalizeDeepSeekBalance([]byte(newBody))
	if err != nil {
		t.Fatal(err)
	}

	// 基线：直插（不触发记账）
	prev := &store.BalanceSnapshot{UpstreamID: 1, UpstreamName: "ds", Vendor: VendorDeepSeek, Payload: prevPayload, CreatedAt: time.Now().Add(-time.Hour)}
	if err := store.InsertBalanceSnapshot(d, prev); err != nil {
		t.Fatal(err)
	}
	// 新快照走记账路径：3.10 的消费必须被记出来
	snap := &store.BalanceSnapshot{UpstreamID: 1, UpstreamName: "ds", Vendor: VendorDeepSeek, Payload: newPayload, CreatedAt: time.Now()}
	n, err := store.RecordBalanceSnapshot(d, snap)
	if err != nil || n != 1 {
		t.Fatalf("contract broken: normalized payload not billed (n=%d err=%v)", n, err)
	}
	bills, total, err := store.BillsList(d, nil, time.Time{}, time.Time{}, 1, 10)
	if err != nil || total != 1 {
		t.Fatalf("bills total=%d err=%v", total, err)
	}
	if bills[0].AmountMicros != 3_100_000 || bills[0].Currency != "CNY" || bills[0].Kind != store.BillKindAuto {
		t.Fatalf("bill=%+v", bills[0])
	}
}
