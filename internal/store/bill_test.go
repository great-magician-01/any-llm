package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func billDay(t string) time.Time {
	ts, err := time.ParseInLocation("2006-01-02 15:04", t+" 00:00", time.Local)
	if err != nil {
		panic(err)
	}
	return ts
}

func TestParseAmountMicros(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		err  bool
	}{
		{"110.00", 110_000_000, false},
		{"12.34", 12_340_000, false},
		{"0.34", 340_000, false},
		{".34", 340_000, false},
		{"12.", 12_000_000, false},
		{"12", 12_000_000, false},
		{"-5.5", -5_500_000, false},
		{"+7", 7_000_000, false},
		{" 110.00 ", 110_000_000, false},
		// 第 7 位四舍五入到 micros
		{"1.0000004", 1_000_000, false},
		{"1.0000005", 1_000_001, false},
		{"1.00000049", 1_000_000, false},
		{"", 0, true},
		{".", 0, true},
		{"abc", 0, true},
		{"12a", 0, true},
		{"1.2.3", 0, true},
		{"1 2", 0, true},
	}
	for _, c := range cases {
		got, err := ParseAmountMicros(c.in)
		if c.err {
			if err == nil {
				t.Errorf("ParseAmountMicros(%q) = %d, want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseAmountMicros(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseAmountMicros(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestBillCRUDAndFilters(t *testing.T) {
	d := testDB(t)
	uid1, uid2 := int64(1), int64(2)
	bills := []*Bill{
		{UpstreamID: uid1, UpstreamName: "ds", Kind: BillKindAuto, AmountMicros: 1_650_000, Currency: "CNY", BilledAt: billDay("2026-03-05")},
		{UpstreamID: uid1, UpstreamName: "ds", Kind: BillKindManual, AmountMicros: 30_000_000, Currency: "CNY", BilledAt: billDay("2026-03-10"), Note: "月费"},
		{UpstreamID: uid2, UpstreamName: "kimi", Kind: BillKindManual, AmountMicros: 99_000_000, Currency: "CNY", BilledAt: billDay("2026-04-02")},
	}
	for i, b := range bills {
		if err := InsertBill(d, b); err != nil {
			t.Fatal(err)
		}
		if b.ID == 0 {
			t.Fatalf("bill %d: id not set", i)
		}
	}
	// CreatedAt 自动补齐
	if bills[0].CreatedAt.IsZero() {
		t.Fatal("created_at not filled")
	}

	// GetBill 往返
	got, err := GetBill(d, bills[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != BillKindManual || got.AmountMicros != 30_000_000 || got.Note != "月费" || got.Currency != "CNY" {
		t.Fatalf("got=%+v", got)
	}
	if !got.BilledAt.Equal(billDay("2026-03-10")) {
		t.Fatalf("billed_at=%v", got.BilledAt)
	}

	// 列表：默认 billed_at 倒序
	list, total, err := BillsList(d, nil, time.Time{}, time.Time{}, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(list) != 3 {
		t.Fatalf("total=%d len=%d", total, len(list))
	}
	if list[0].ID != bills[2].ID {
		t.Fatalf("order wrong: first=%d want %d", list[0].ID, bills[2].ID)
	}

	// 按 upstream 过滤
	if _, total, err = BillsList(d, &uid1, time.Time{}, time.Time{}, 1, 10); err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("upstream filter total=%d", total)
	}

	// 时间窗口 [from, to)：只含 3 月
	if _, total, err = BillsList(d, nil, billDay("2026-03-01"), billDay("2026-04-01"), 1, 10); err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("window total=%d", total)
	}

	// 分页
	list, total, err = BillsList(d, nil, time.Time{}, time.Time{}, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(list) != 1 {
		t.Fatalf("page total=%d len=%d", total, len(list))
	}

	// Update 只改可编辑字段（kind/detail/created_at 不动）
	upd := *got
	upd.AmountMicros = 45_000_000
	upd.Note = "改过"
	if err := UpdateBill(d, &upd); err != nil {
		t.Fatal(err)
	}
	got2, _ := GetBill(d, bills[1].ID)
	if got2.AmountMicros != 45_000_000 || got2.Note != "改过" {
		t.Fatalf("updated=%+v", got2)
	}
	if got2.Kind != BillKindManual || !got2.CreatedAt.Equal(got.CreatedAt) {
		t.Fatalf("immutable fields changed: %+v vs %+v", got2, got)
	}

	// Delete 后查不到
	if err := DeleteBill(d, bills[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := GetBill(d, bills[0].ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected ErrNoRows, got %v", err)
	}
	if _, total, _ = BillsList(d, nil, time.Time{}, time.Time{}, 1, 10); total != 2 {
		t.Fatalf("after delete total=%d", total)
	}
}

func TestBillTrend(t *testing.T) {
	d := testDB(t)
	seed := []*Bill{
		{UpstreamID: 1, UpstreamName: "ds", Kind: BillKindAuto, AmountMicros: 1_000_000, Currency: "CNY", BilledAt: billDay("2026-01-10")},
		{UpstreamID: 1, UpstreamName: "ds", Kind: BillKindAuto, AmountMicros: 500_000, Currency: "CNY", BilledAt: billDay("2026-01-10").Add(3 * time.Hour)},
		{UpstreamID: 2, UpstreamName: "manual", Kind: BillKindManual, AmountMicros: 20_000_000, Currency: "CNY", BilledAt: billDay("2026-01-15")},
		{UpstreamID: 1, UpstreamName: "ds", Kind: BillKindAuto, AmountMicros: 2_000_000, Currency: "CNY", BilledAt: billDay("2026-02-20")},
		{UpstreamID: 1, UpstreamName: "ds", Kind: BillKindManual, AmountMicros: 50_000_000, Currency: "CNY", BilledAt: billDay("2025-12-31")},
	}
	for _, b := range seed {
		if err := InsertBill(d, b); err != nil {
			t.Fatal(err)
		}
	}

	// 日桶：空桶零填充
	out, err := BillTrend(d, "day", nil, "2026-01-09", "2026-01-11")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 {
		t.Fatalf("day buckets=%d", len(out))
	}
	if out[0].Bucket != "2026-01-09" || out[0].AmountMicros != 0 || out[0].Count != 0 {
		t.Fatalf("zero bucket=%+v", out[0])
	}
	if out[1].AmountMicros != 1_500_000 || out[1].Count != 2 {
		t.Fatalf("10th bucket=%+v", out[1])
	}
	if out[2].Bucket != "2026-01-11" || out[2].AmountMicros != 0 {
		t.Fatalf("11th bucket=%+v", out[2])
	}

	// 月桶：跨月
	out, err = BillTrend(d, "month", nil, "2026-01-01", "2026-02-28")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0].Bucket != "2026-01" || out[1].Bucket != "2026-02" {
		t.Fatalf("month buckets=%+v", out)
	}
	if out[0].AmountMicros != 21_500_000 || out[0].Count != 3 {
		t.Fatalf("jan=%+v", out[0])
	}
	if out[1].AmountMicros != 2_000_000 || out[1].Count != 1 {
		t.Fatalf("feb=%+v", out[1])
	}

	// 年桶
	out, err = BillTrend(d, "year", nil, "2025-01-01", "2026-06-30")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0].Bucket != "2025" || out[1].Bucket != "2026" {
		t.Fatalf("year buckets=%+v", out)
	}
	if out[0].AmountMicros != 50_000_000 || out[1].AmountMicros != 23_500_000 {
		t.Fatalf("year sums=%+v", out)
	}

	// 按上游过滤
	uid2 := int64(2)
	out, err = BillTrend(d, "month", &uid2, "2026-01-01", "2026-01-31")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].AmountMicros != 20_000_000 {
		t.Fatalf("upstream filter=%+v", out)
	}

	// 缺省窗口（不传 from/to）也不报错
	out, err = BillTrend(d, "year", nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 5 {
		t.Fatalf("default year span=%d", len(out))
	}

	// 非法 bucket 响亮失败
	if _, err := BillTrend(d, "week", nil, "", ""); !errors.Is(err, ErrInvalidBucket) {
		t.Fatalf("err=%v", err)
	}

	// 超宽窗口截断到上限
	out, err = BillTrend(d, "day", nil, "2024-01-01", "2026-01-01")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != maxBillDayBuckets {
		t.Fatalf("capped=%d want %d", len(out), maxBillDayBuckets)
	}
}

func TestBillUpstreamTotals(t *testing.T) {
	d := testDB(t)
	seed := []*Bill{
		{UpstreamID: 1, UpstreamName: "ds", Kind: BillKindAuto, AmountMicros: 1_000_000, Currency: "CNY", BilledAt: billDay("2026-03-01")},
		{UpstreamID: 1, UpstreamName: "ds", Kind: BillKindAuto, AmountMicros: 2_000_000, Currency: "CNY", BilledAt: billDay("2026-03-05")},
		{UpstreamID: 2, UpstreamName: "kimi", Kind: BillKindManual, AmountMicros: 10_000_000, Currency: "CNY", BilledAt: billDay("2026-03-07")},
		{UpstreamID: 2, UpstreamName: "kimi", Kind: BillKindManual, AmountMicros: 5_000_000, Currency: "USD", BilledAt: billDay("2026-03-08")},
		{UpstreamID: 1, UpstreamName: "ds", Kind: BillKindManual, AmountMicros: 999_000_000, Currency: "CNY", BilledAt: billDay("2026-05-01")},
	}
	for _, b := range seed {
		if err := InsertBill(d, b); err != nil {
			t.Fatal(err)
		}
	}
	out, err := BillUpstreamTotals(d, nil, billDay("2026-03-01"), billDay("2026-04-01"))
	if err != nil {
		t.Fatal(err)
	}
	// 金额降序；同上游同币种合并
	if len(out) != 3 {
		t.Fatalf("len=%d %+v", len(out), out)
	}
	if out[0].UpstreamID != 2 || out[0].Currency != "CNY" || out[0].AmountMicros != 10_000_000 {
		t.Fatalf("out[0]=%+v", out[0])
	}
	if out[1].UpstreamID != 2 || out[1].Currency != "USD" || out[1].AmountMicros != 5_000_000 || out[1].Count != 1 {
		t.Fatalf("out[1]=%+v", out[1])
	}
	if out[2].UpstreamID != 1 || out[2].Currency != "CNY" || out[2].AmountMicros != 3_000_000 || out[2].Count != 2 {
		t.Fatalf("out[2]=%+v", out[2])
	}
	// 上游过滤
	uid1 := int64(1)
	out, err = BillUpstreamTotals(d, &uid1, billDay("2026-03-01"), billDay("2026-04-01"))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].AmountMicros != 3_000_000 {
		t.Fatalf("filtered=%+v", out)
	}
}

func insertSnap(t *testing.T, d *sql.DB, uid int64, name, payload string, at time.Time) *BalanceSnapshot {
	t.Helper()
	s := &BalanceSnapshot{UpstreamID: uid, UpstreamName: name, Vendor: "deepseek", Payload: json.RawMessage(payload), CreatedAt: at}
	if err := InsertBalanceSnapshot(d, s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRecordBillFromBalanceDelta(t *testing.T) {
	d := testDB(t)
	const balance = `{"kind":"balance","is_available":true,"balances":[{"currency":"CNY","total":"%s","granted":"0","topped_up":"%s"}]}`

	// 首条快照没有基线：不记账
	s1 := insertSnap(t, d, 1, "ds", `{"kind":"balance","is_available":true,"balances":[{"currency":"CNY","total":"110.00","granted":"10.00","topped_up":"100.00"}]}`, billDay("2026-03-01"))
	if n, err := RecordBillFromBalanceDelta(d, s1); err != nil || n != 0 {
		t.Fatalf("first snapshot: n=%d err=%v", n, err)
	}

	// 余额下降 → 记一笔 auto 账单，金额 = 差额
	s2 := insertSnap(t, d, 1, "ds", `{"kind":"balance","is_available":true,"balances":[{"currency":"CNY","total":"108.35","granted":"10.00","topped_up":"98.35"}]}`, billDay("2026-03-02"))
	n, err := RecordBillFromBalanceDelta(d, s2)
	if err != nil || n != 1 {
		t.Fatalf("spend: n=%d err=%v", n, err)
	}
	list, total, _ := BillsList(d, nil, time.Time{}, time.Time{}, 1, 10)
	if total != 1 {
		t.Fatalf("total=%d", total)
	}
	b := list[0]
	if b.Kind != BillKindAuto || b.AmountMicros != 1_650_000 || b.Currency != "CNY" || b.UpstreamID != 1 {
		t.Fatalf("bill=%+v", b)
	}
	if !b.BilledAt.Equal(billDay("2026-03-02")) {
		t.Fatalf("billed_at=%v want snapshot time", b.BilledAt)
	}
	// detail 留了推导依据
	if !strings.Contains(b.Detail, `"prev_total":"110.00"`) || !strings.Contains(b.Detail, `"new_total":"108.35"`) {
		t.Fatalf("detail=%s", b.Detail)
	}

	// 余额上升（充值）→ 不记负账单
	s3 := insertSnap(t, d, 1, "ds", `{"kind":"balance","is_available":true,"balances":[{"currency":"CNY","total":"208.35","granted":"10.00","topped_up":"198.35"}]}`, billDay("2026-03-03"))
	if n, err := RecordBillFromBalanceDelta(d, s3); err != nil || n != 0 {
		t.Fatalf("recharge: n=%d err=%v", n, err)
	}

	// quota 快照（kimi）没有金额语义 → 不记账
	s4 := insertSnap(t, d, 1, "ds", `{"kind":"quota","windows":[{"id":"five_hour","used_percent":"12.0"}]}`, billDay("2026-03-04"))
	if n, err := RecordBillFromBalanceDelta(d, s4); err != nil || n != 0 {
		t.Fatalf("quota: n=%d err=%v", n, err)
	}
	// quota 之后再来 balance：上一条非 balance 类，无同口径基线 → 跳过
	s5 := insertSnap(t, d, 1, "ds", `{"kind":"balance","is_available":true,"balances":[{"currency":"CNY","total":"200.00","granted":"10.00","topped_up":"190.00"}]}`, billDay("2026-03-05"))
	if n, err := RecordBillFromBalanceDelta(d, s5); err != nil || n != 0 {
		t.Fatalf("after quota: n=%d err=%v", n, err)
	}

	// 多币种：CNY 下降记账，USD 上升跳过，新币种无基线跳过
	insertSnap(t, d, 2, "mix", `{"kind":"balance","is_available":true,"balances":[
		{"currency":"CNY","total":"100.00","granted":"0","topped_up":"100.00"},
		{"currency":"USD","total":"50.00","granted":"0","topped_up":"50.00"}]}`, billDay("2026-03-06"))
	s7 := insertSnap(t, d, 2, "mix", `{"kind":"balance","is_available":true,"balances":[
		{"currency":"CNY","total":"99.00","granted":"0","topped_up":"99.00"},
		{"currency":"USD","total":"52.00","granted":"0","topped_up":"52.00"},
		{"currency":"EUR","total":"10.00","granted":"0","topped_up":"10.00"}]}`, billDay("2026-03-07"))
	n, err = RecordBillFromBalanceDelta(d, s7)
	if err != nil || n != 1 {
		t.Fatalf("multi-currency: n=%d err=%v", n, err)
	}
	list, total, _ = BillsList(d, &[]int64{2}[0], time.Time{}, time.Time{}, 1, 10)
	if total != 1 || list[0].AmountMicros != 1_000_000 || list[0].Currency != "CNY" {
		t.Fatalf("multi-currency bill=%+v", list)
	}

	// 金额串解析不了的币种跳过，不拖垮其他条目
	insertSnap(t, d, 3, "bad", `{"kind":"balance","is_available":true,"balances":[
		{"currency":"CNY","total":"oops","granted":"0","topped_up":"0"},
		{"currency":"USD","total":"10.00","granted":"0","topped_up":"10.00"}]}`, billDay("2026-03-08"))
	s9 := insertSnap(t, d, 3, "bad", `{"kind":"balance","is_available":true,"balances":[
		{"currency":"CNY","total":"5.00","granted":"0","topped_up":"5.00"},
		{"currency":"USD","total":"9.00","granted":"0","topped_up":"9.00"}]}`, billDay("2026-03-09"))
	n, err = RecordBillFromBalanceDelta(d, s9)
	if err != nil || n != 1 {
		t.Fatalf("bad amount: n=%d err=%v", n, err)
	}
	list, _, _ = BillsList(d, &[]int64{3}[0], time.Time{}, time.Time{}, 1, 10)
	if list[0].Currency != "USD" {
		t.Fatalf("expected only USD bill, got %+v", list)
	}
}
