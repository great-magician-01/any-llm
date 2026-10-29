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

func billBucketAmount(t *testing.T, b BillBucketStat, currency string) int64 {
	t.Helper()
	for _, a := range b.Amounts {
		if a.Currency == currency {
			return a.AmountMicros
		}
	}
	t.Fatalf("bucket %s has no %s amount: %+v", b.Bucket, currency, b.Amounts)
	return 0
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
		// int64 边界（裸整数按整单位 ×1e6 解释）：能装下的最大值合法，
		// 再大必须报错而不是回绕成垃圾值
		{"9223372036854", 9223372036854000000, false},
		{"9223372036854775807", 0, true},
		{"9223372036854775808", 0, true},
		{"9223372036854.7758075", 0, true}, // 四舍五入恰好溢出
		{"92233720368547758.07", 0, true},
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

	// Delete 返回受影响行数；删不存在的行回 0（handler 据此 404）
	n, err := DeleteBill(d, bills[0].ID)
	if err != nil || n != 1 {
		t.Fatalf("delete n=%d err=%v", n, err)
	}
	if _, err := GetBill(d, bills[0].ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected ErrNoRows, got %v", err)
	}
	if n, _ = DeleteBill(d, bills[0].ID); n != 0 {
		t.Fatalf("second delete n=%d, want 0", n)
	}
	if _, total, _ = BillsList(d, nil, time.Time{}, time.Time{}, 1, 10); total != 2 {
		t.Fatalf("after delete total=%d", total)
	}
}

func TestBillWindow(t *testing.T) {
	// 两端都给：日期闭区间 → 桶对齐的半开窗口
	s, e, err := BillWindow("day", "2026-01-09", "2026-01-11")
	if err != nil {
		t.Fatal(err)
	}
	if !s.Equal(billDay("2026-01-09")) || !e.Equal(billDay("2026-01-12")) {
		t.Fatalf("day window=%v..%v", s, e)
	}
	// 月桶只改变图表分桶，不把显式日期窗口扩到月边界。
	s, e, _ = BillWindow("month", "2026-01-15", "2026-02-20")
	if !s.Equal(billDay("2026-01-15")) || !e.Equal(billDay("2026-02-21")) {
		t.Fatalf("month window=%v..%v", s, e)
	}

	// 只给 to：近 N 桶截止到 to（起点跟随 to，不会出现 start > end）
	s, e, _ = BillWindow("day", "", "2026-01-31")
	if !e.Equal(billDay("2026-02-01")) || !s.Equal(billDay("2026-01-02")) {
		t.Fatalf("to-only window=%v..%v", s, e)
	}

	// 都不给：缺省回看跨度（day 30 桶：[start, end) 恰好 30 个整日）
	s, e, _ = BillWindow("day", "", "")
	if got := int(e.Sub(s).Hours() / 24); got != 30 {
		t.Fatalf("default day span=%d buckets, want 30", got)
	}
	s, e, _ = BillWindow("year", "", "")
	if s.Year() != time.Now().Year()-4 || e.Year() != time.Now().Year()+1 {
		t.Fatalf("default year window=%v..%v", s, e)
	}

	// 非法 bucket 响亮失败
	if _, _, err := BillWindow("week", "", ""); !errors.Is(err, ErrInvalidBucket) {
		t.Fatalf("err=%v", err)
	}
	// 非法日期响亮失败
	if _, _, err := BillWindow("day", "2026/01/01", ""); err == nil {
		t.Fatal("invalid from accepted")
	}
	// 超宽窗口响亮失败，不能静默截断后与明细列表使用不同口径。
	_, _, err = BillWindow("day", "2024-01-01", "2026-01-01")
	if !errors.Is(err, ErrBillWindowTooWide) {
		t.Fatalf("capped window err=%v, want ErrBillWindowTooWide", err)
	}
	// from 晚于 to：空窗口（调用方得空结果，不伪造桶）
	s, e, _ = BillWindow("day", "2026-05-01", "2026-01-01")
	if !s.Equal(e) {
		t.Fatalf("inverted window=%v..%v, want empty", s, e)
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
	window := func(t *testing.T, bucket, from, to string) (time.Time, time.Time) {
		t.Helper()
		s, e, err := BillWindow(bucket, from, to)
		if err != nil {
			t.Fatal(err)
		}
		return s, e
	}

	// 日桶：空桶零填充
	s, e := window(t, "day", "2026-01-09", "2026-01-11")
	out, err := BillTrend(d, "day", nil, s, e)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 {
		t.Fatalf("day buckets=%d", len(out))
	}
	if out[0].Bucket != "2026-01-09" || len(out[0].Amounts) != 0 || out[0].Count != 0 {
		t.Fatalf("zero bucket=%+v", out[0])
	}
	if got := billBucketAmount(t, out[1], "CNY"); got != 1_500_000 || out[1].Count != 2 {
		t.Fatalf("10th bucket=%+v", out[1])
	}
	if out[2].Bucket != "2026-01-11" || len(out[2].Amounts) != 0 {
		t.Fatalf("11th bucket=%+v", out[2])
	}

	// 月桶：跨月；桶对齐窗口把月初/月末整桶计入（图表与合计同口径的前提）
	s, e = window(t, "month", "2026-01-01", "2026-02-28")
	out, err = BillTrend(d, "month", nil, s, e)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0].Bucket != "2026-01" || out[1].Bucket != "2026-02" {
		t.Fatalf("month buckets=%+v", out)
	}
	if got := billBucketAmount(t, out[0], "CNY"); got != 21_500_000 || out[0].Count != 3 {
		t.Fatalf("jan=%+v", out[0])
	}
	if got := billBucketAmount(t, out[1], "CNY"); got != 2_000_000 || out[1].Count != 1 {
		t.Fatalf("feb=%+v", out[1])
	}

	// 年桶
	s, e = window(t, "year", "2025-01-01", "2026-06-30")
	out, err = BillTrend(d, "year", nil, s, e)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0].Bucket != "2025" || out[1].Bucket != "2026" {
		t.Fatalf("year buckets=%+v", out)
	}
	if got0, got1 := billBucketAmount(t, out[0], "CNY"), billBucketAmount(t, out[1], "CNY"); got0 != 50_000_000 || got1 != 23_500_000 {
		t.Fatalf("year sums=%+v", out)
	}

	// 按上游过滤
	uid2 := int64(2)
	s, e = window(t, "month", "2026-01-01", "2026-01-31")
	out, err = BillTrend(d, "month", &uid2, s, e)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || billBucketAmount(t, out[0], "CNY") != 20_000_000 {
		t.Fatalf("upstream filter=%+v", out)
	}

	// 空窗口（from 晚于 to）返回空切片而不是伪造的桶
	s, e = window(t, "day", "2026-05-01", "2026-01-01")
	out, err = BillTrend(d, "day", nil, s, e)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Fatalf("inverted window buckets=%+v, want empty", out)
	}

	// 非法 bucket 响亮失败
	if _, err := BillTrend(d, "week", nil, billDay("2026-01-01"), billDay("2026-01-02")); !errors.Is(err, ErrInvalidBucket) {
		t.Fatalf("err=%v", err)
	}
}

func TestBillTrendSplitsCurrenciesWithinExactWindow(t *testing.T) {
	d := testDB(t)
	seed := []*Bill{
		{UpstreamID: 1, UpstreamName: "a", Kind: BillKindManual, AmountMicros: 100_000_000, Currency: "CNY", BilledAt: billDay("2026-01-10")},
		{UpstreamID: 1, UpstreamName: "a", Kind: BillKindManual, AmountMicros: 1_000_000, Currency: "CNY", BilledAt: billDay("2026-01-16")},
		{UpstreamID: 1, UpstreamName: "a", Kind: BillKindManual, AmountMicros: 50_000_000, Currency: "USD", BilledAt: billDay("2026-01-16")},
	}
	for _, b := range seed {
		if err := InsertBill(d, b); err != nil {
			t.Fatal(err)
		}
	}
	// 按月只影响桶标签，不得把范围外的 1/10 账单扩进来。
	start, end, err := BillWindow("month", "2026-01-15", "2026-01-20")
	if err != nil {
		t.Fatal(err)
	}
	out, err := BillTrend(d, "month", nil, start, end)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Bucket != "2026-01" || out[0].Count != 2 {
		t.Fatalf("trend=%+v", out)
	}
	if len(out[0].Amounts) != 2 || billBucketAmount(t, out[0], "CNY") != 1_000_000 || billBucketAmount(t, out[0], "USD") != 50_000_000 {
		t.Fatalf("mixed-currency trend=%+v", out[0])
	}
	// 汇总与趋势使用同一精确窗口；币种之间没有 51 或 101 这种伪合计。
	totals, err := BillUpstreamTotals(d, nil, start, end)
	if err != nil {
		t.Fatal(err)
	}
	if len(totals) != 2 {
		t.Fatalf("upstream totals=%+v", totals)
	}
	for _, total := range totals {
		if total.Currency == "CNY" && total.AmountMicros != 1_000_000 {
			t.Fatalf("CNY total includes out-of-range bill: %+v", total)
		}
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

func TestBillKindTotalsCurrencySplit(t *testing.T) {
	d := testDB(t)
	seed := []*Bill{
		{UpstreamID: 1, UpstreamName: "a", Kind: BillKindManual, AmountMicros: 100_000_000, Currency: "CNY", BilledAt: billDay("2026-03-01")},
		{UpstreamID: 2, UpstreamName: "b", Kind: BillKindManual, AmountMicros: 50_000_000, Currency: "USD", BilledAt: billDay("2026-03-02")},
		{UpstreamID: 1, UpstreamName: "a", Kind: BillKindAuto, AmountMicros: 1_000_000, Currency: "CNY", BilledAt: billDay("2026-03-03")},
	}
	for _, b := range seed {
		if err := InsertBill(d, b); err != nil {
			t.Fatal(err)
		}
	}
	out, err := BillKindTotals(d, nil, billDay("2026-03-01"), billDay("2026-04-01"))
	if err != nil {
		t.Fatal(err)
	}
	// kind × 币种拆分：CNY 手动 100 + USD 手动 50，绝不加总成 150
	if len(out) != 3 {
		t.Fatalf("len=%d %+v", len(out), out)
	}
	byKey := map[string]BillKindTotal{}
	for _, k := range out {
		byKey[k.Kind+"/"+k.Currency] = k
	}
	if k := byKey["manual/CNY"]; k.Count != 1 || k.AmountMicros != 100_000_000 {
		t.Fatalf("manual/CNY=%+v", k)
	}
	if k := byKey["manual/USD"]; k.Count != 1 || k.AmountMicros != 50_000_000 {
		t.Fatalf("manual/USD=%+v", k)
	}
	if k := byKey["auto/CNY"]; k.Count != 1 || k.AmountMicros != 1_000_000 {
		t.Fatalf("auto/CNY=%+v", k)
	}
}

func insertSnap(t *testing.T, d *sql.DB, uid int64, name, vendor, payload string, at time.Time) *BalanceSnapshot {
	t.Helper()
	s := &BalanceSnapshot{UpstreamID: uid, UpstreamName: name, Vendor: vendor, Payload: json.RawMessage(payload), CreatedAt: at}
	if err := InsertBalanceSnapshot(d, s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRecordBalanceSnapshot_BillsFromDelta(t *testing.T) {
	d := testDB(t)

	// 基线快照（直插，不触发记账）
	insertSnap(t, d, 1, "ds", "deepseek", `{"kind":"balance","is_available":true,"balances":[{"currency":"CNY","total":"110.00","granted":"10.00","topped_up":"100.00"}]}`, billDay("2026-03-01"))

	// 余额下降 → 记一笔 auto 账单，金额 = 差额；快照本身也落库
	s2 := &BalanceSnapshot{UpstreamID: 1, UpstreamName: "ds", Vendor: "deepseek", Payload: json.RawMessage(`{"kind":"balance","is_available":true,"balances":[{"currency":"CNY","total":"108.35","granted":"10.00","topped_up":"98.35"}]}`), CreatedAt: billDay("2026-03-02")}
	if n, err := RecordBalanceSnapshot(d, s2); err != nil || n != 1 {
		t.Fatalf("spend: n=%d err=%v", n, err)
	}
	if s2.ID == 0 {
		t.Fatal("snapshot not archived")
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
	// detail 留了推导依据（含 vendor）
	if !strings.Contains(b.Detail, `"prev_total":"110.00"`) ||
		!strings.Contains(b.Detail, `"new_total":"108.35"`) ||
		!strings.Contains(b.Detail, `"vendor":"deepseek"`) {
		t.Fatalf("detail=%s", b.Detail)
	}

	// 余额上升（充值）→ 不记负账单，快照照常落库
	s3 := &BalanceSnapshot{UpstreamID: 1, UpstreamName: "ds", Vendor: "deepseek", Payload: json.RawMessage(`{"kind":"balance","is_available":true,"balances":[{"currency":"CNY","total":"208.35","granted":"10.00","topped_up":"198.35"}]}`), CreatedAt: billDay("2026-03-03")}
	if n, err := RecordBalanceSnapshot(d, s3); err != nil || n != 0 {
		t.Fatalf("recharge: n=%d err=%v", n, err)
	}
	if s3.ID == 0 {
		t.Fatal("recharge snapshot not archived")
	}

	// quota 快照（kimi）没有金额语义 → 不记账
	s4 := &BalanceSnapshot{UpstreamID: 1, UpstreamName: "ds", Vendor: "deepseek", Payload: json.RawMessage(`{"kind":"quota","windows":[{"id":"five_hour","used_percent":"12.0"}]}`), CreatedAt: billDay("2026-03-04")}
	if n, err := RecordBalanceSnapshot(d, s4); err != nil || n != 0 {
		t.Fatalf("quota: n=%d err=%v", n, err)
	}

	// 厂商换了（deepseek → stepfun，同为 balance 类）：口径不可比，不记账
	s5 := &BalanceSnapshot{UpstreamID: 1, UpstreamName: "ds", Vendor: "stepfun", Payload: json.RawMessage(`{"kind":"balance","is_available":true,"balances":[{"currency":"CNY","total":"8.00","granted":"0","topped_up":"8.00"}]}`), CreatedAt: billDay("2026-03-05")}
	if n, err := RecordBalanceSnapshot(d, s5); err != nil || n != 0 {
		t.Fatalf("vendor switch: n=%d err=%v", n, err)
	}
	// 换厂商后的快照照常落库，且成为新厂商的基线
	s6 := &BalanceSnapshot{UpstreamID: 1, UpstreamName: "ds", Vendor: "stepfun", Payload: json.RawMessage(`{"kind":"balance","is_available":true,"balances":[{"currency":"CNY","total":"6.00","granted":"0","topped_up":"6.00"}]}`), CreatedAt: billDay("2026-03-06")}
	if n, err := RecordBalanceSnapshot(d, s6); err != nil || n != 1 {
		t.Fatalf("after vendor switch: n=%d err=%v", n, err)
	}

	// 多币种：CNY 下降记账，USD 上升跳过，新币种无基线跳过
	insertSnap(t, d, 2, "mix", "deepseek", `{"kind":"balance","is_available":true,"balances":[
		{"currency":"CNY","total":"100.00","granted":"0","topped_up":"100.00"},
		{"currency":"USD","total":"50.00","granted":"0","topped_up":"50.00"}]}`, billDay("2026-03-06"))
	s7 := &BalanceSnapshot{UpstreamID: 2, UpstreamName: "mix", Vendor: "deepseek", Payload: json.RawMessage(`{"kind":"balance","is_available":true,"balances":[
		{"currency":"CNY","total":"99.00","granted":"0","topped_up":"99.00"},
		{"currency":"USD","total":"52.00","granted":"0","topped_up":"52.00"},
		{"currency":"EUR","total":"10.00","granted":"0","topped_up":"10.00"}]}`), CreatedAt: billDay("2026-03-07")}
	if n, err := RecordBalanceSnapshot(d, s7); err != nil || n != 1 {
		t.Fatalf("multi-currency: n=%d err=%v", n, err)
	}
	uid2 := int64(2)
	list, total, _ = BillsList(d, &uid2, time.Time{}, time.Time{}, 1, 10)
	if total != 1 || list[0].AmountMicros != 1_000_000 || list[0].Currency != "CNY" {
		t.Fatalf("multi-currency bill=%+v", list)
	}

	// 金额串解析不了的币种跳过（Warn），不拖垮其他条目
	insertSnap(t, d, 3, "bad", "deepseek", `{"kind":"balance","is_available":true,"balances":[
		{"currency":"CNY","total":"oops","granted":"0","topped_up":"0"},
		{"currency":"USD","total":"10.00","granted":"0","topped_up":"10.00"}]}`, billDay("2026-03-08"))
	s9 := &BalanceSnapshot{UpstreamID: 3, UpstreamName: "bad", Vendor: "deepseek", Payload: json.RawMessage(`{"kind":"balance","is_available":true,"balances":[
		{"currency":"CNY","total":"5.00","granted":"0","topped_up":"5.00"},
		{"currency":"USD","total":"9.00","granted":"0","topped_up":"9.00"}]}`), CreatedAt: billDay("2026-03-09")}
	if n, err := RecordBalanceSnapshot(d, s9); err != nil || n != 1 {
		t.Fatalf("bad amount: n=%d err=%v", n, err)
	}
	uid3 := int64(3)
	list, _, _ = BillsList(d, &uid3, time.Time{}, time.Time{}, 1, 10)
	if list[0].Currency != "USD" {
		t.Fatalf("expected only USD bill, got %+v", list)
	}
}
