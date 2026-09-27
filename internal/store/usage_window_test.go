package store

import (
	"testing"
	"time"
)

// 本文件钉住配额窗口的边界语义。网关（internal/gateway/router.go）与管理端
// （internal/adminapi/usage.go）都用同一套公式算窗口：
//
//	dayStart   = time.Date(now.Y, now.M, now.D, 0,0,0,0, time.Local)
//	dayEnd     = dayStart.Add(24h)          // 注意不是次日 0 点
//	monthStart = time.Date(now.Y, now.M, 1, 0,0,0,0, time.Local)
//	monthEnd   = monthStart.AddDate(0, 1, 0)
//
// 而 store.SumTokens 的契约是**半开区间 [from, to)**。两侧合起来的业务规则是：
//   - 昨天的用量不计入今天的日窗口，但（只要昨天还在本月）计入本月月窗口；
//   - 窗口边界「含起点、不含终点」：今天 0 点算今天，下月 1 日 0 点不算本月；
//   - 日窗口宽度固定 24 小时（dayStart.Add(24h)），在夏令时切换日会与「本地
//     午夜到本地午夜」差 1 小时——见 TestSumTokensDayWindowDSTSkew。

// uwWindows 复刻网关/管理端的窗口公式。生产代码用 time.Local；这里用
// now.Location()，让 DST 用例把 time.Local 换成换时区后仍然一致。
func uwWindows(now time.Time) (dayStart, dayEnd, monthStart, monthEnd time.Time) {
	dayStart = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	monthStart = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	return dayStart, dayStart.Add(24 * time.Hour), monthStart, monthStart.AddDate(0, 1, 0)
}

// 固定日期（2026-03-15）的日/月窗口边界：上月最后一天、本月第一天、昨天、
// 今天 0 点、今天末刻、次日 0 点、本月最后一天、下月 1 日 0 点。
// 固定日期而不是 time.Now()，才能确定地断言「月首/月末/跨月」三处边界。
func TestSumTokensWindowMonthBoundaries(t *testing.T) {
	d := testDB(t)
	k, err := CreateExtKey(d, "l", "", 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}

	ref := time.Date(2026, 3, 15, 12, 0, 0, 0, time.Local)
	dayStart, dayEnd, monthStart, monthEnd := uwWindows(ref)
	if got := dayStart.Format("2006-01-02 15:04"); got != "2026-03-15 00:00" {
		t.Fatalf("dayStart=%s", got)
	}
	if got := dayEnd.Format("2006-01-02 15:04"); got != "2026-03-16 00:00" {
		t.Fatalf("dayEnd=%s（与次日 0 点相同才说明无 DST 干扰）", got)
	}

	type row struct {
		ts     time.Time
		tokens int
	}
	rows := []row{
		{time.Date(2026, 2, 28, 23, 59, 59, 0, time.Local), 1},  // 上月最后一天（月窗口外）
		{time.Date(2026, 3, 1, 0, 0, 0, 0, time.Local), 2},      // 月首边界：含
		{time.Date(2026, 3, 14, 23, 59, 59, 0, time.Local), 4},  // 昨天：日窗口外、月窗口内
		{time.Date(2026, 3, 15, 0, 0, 0, 0, time.Local), 8},     // 日首边界：含
		{time.Date(2026, 3, 15, 23, 59, 59, 0, time.Local), 16}, // 日末刻：含
		{time.Date(2026, 3, 16, 0, 0, 0, 0, time.Local), 32},    // 次日 0 点：日窗口外、月窗口内
		{time.Date(2026, 3, 31, 23, 59, 59, 0, time.Local), 64}, // 本月最后一天：含
		{time.Date(2026, 4, 1, 0, 0, 0, 0, time.Local), 128},    // 下月 1 日 0 点：月窗口外
	}
	for _, r := range rows {
		if err := InsertUsage(d, &UsageRecord{ExtKeyID: &k.ID, UpstreamName: "u", Model: "m",
			InFormat: "openai", UpFormat: "openai", TotalTokens: r.tokens, Status: "ok", CreatedAt: r.ts}); err != nil {
			t.Fatal(err)
		}
	}

	// 日窗口 = 今天 0 点（含）到次日 0 点（不含）：8 + 16
	got, err := SumTokens(d, &k.ID, nil, dayStart, dayEnd)
	if err != nil {
		t.Fatal(err)
	}
	if got != 24 {
		t.Fatalf("日窗口 sum=%d，期望 24（只含 03-15 的两条，边界含起点不含终点）", got)
	}

	// 月窗口 = 本月 1 日 0 点（含）到下月 1 日 0 点（不含）：2+4+8+16+32+64
	got, err = SumTokens(d, &k.ID, nil, monthStart, monthEnd)
	if err != nil {
		t.Fatal(err)
	}
	if got != 126 {
		t.Fatalf("月窗口 sum=%d，期望 126（含昨天与次日 0 点，不含上月最后一天与下月 1 日）", got)
	}

	// 昨天的窗口单独算：证明 03-14 那条确实存在且被日窗口排除（而不是压根没插进去）
	got, err = SumTokens(d, &k.ID, nil, dayStart.Add(-24*time.Hour), dayStart)
	if err != nil {
		t.Fatal(err)
	}
	if got != 4 {
		t.Fatalf("昨日窗口 sum=%d，期望 4（昨天的用量不计入今天的日窗口）", got)
	}

	// 上月的窗口单独算：月首边界从另一侧钉住（02-28 只属于 2 月）
	got, err = SumTokens(d, &k.ID, nil, time.Date(2026, 2, 1, 0, 0, 0, 0, time.Local), monthStart)
	if err != nil {
		t.Fatal(err)
	}
	if got != 1 {
		t.Fatalf("上月窗口 sum=%d，期望 1", got)
	}

	// 全部 8 条的总和：255（窗口计算没有把记录吞掉）
	got, err = SumTokens(d, &k.ID, nil, time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local), time.Date(2027, 1, 1, 0, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatal(err)
	}
	if got != 255 {
		t.Fatalf("全年 sum=%d，期望 255", got)
	}
}

// 「昨天的用量不计入今天的日窗口、但（只要昨天还在本月）计入本月月窗口」。
// 参考时刻取 time.Now() 加几个固定的月首/月末时刻，每个参考时刻一个独立库，
// 这样用例在任何一天跑都成立，并顺带覆盖「1 号那天昨天属于上月」的退化情形。
func TestSumTokensWindowTodayVersusYesterday(t *testing.T) {
	refs := []struct {
		name string
		now  time.Time
	}{
		{"今天（真实时钟）", time.Now()},
		{"月首 09:00", time.Date(2026, 10, 1, 9, 0, 0, 0, time.Local)},
		{"月首 00:00 整", time.Date(2026, 3, 1, 0, 0, 0, 0, time.Local)},
		{"月末 23:00", time.Date(2026, 3, 31, 23, 0, 0, 0, time.Local)},
		{"平年 2 月末", time.Date(2026, 2, 28, 12, 0, 0, 0, time.Local)},
	}
	for _, ref := range refs {
		t.Run(ref.name, func(t *testing.T) {
			d := testDB(t)
			k, err := CreateExtKey(d, "l", "", 0, 0, nil)
			if err != nil {
				t.Fatal(err)
			}

			dayStart, dayEnd, monthStart, monthEnd := uwWindows(ref.now)
			// 两条记录相隔 24 小时，任何一天跑都不会撞在同一时刻（月首那天
			// 「昨天」属于上月，规则退化为「两个窗口都不计」，下面显式分支）。
			todayNoon := dayStart.Add(12 * time.Hour)
			yesterdayNoon := todayNoon.AddDate(0, 0, -1)
			yesterdayStart := dayStart.AddDate(0, 0, -1)

			if err := InsertUsage(d, &UsageRecord{ExtKeyID: &k.ID, UpstreamName: "u", Model: "m",
				InFormat: "openai", UpFormat: "openai", TotalTokens: 5, Status: "ok",
				CreatedAt: todayNoon}); err != nil {
				t.Fatal(err)
			}
			if err := InsertUsage(d, &UsageRecord{ExtKeyID: &k.ID, UpstreamName: "u", Model: "m",
				InFormat: "openai", UpFormat: "openai", TotalTokens: 7, Status: "ok",
				CreatedAt: yesterdayNoon}); err != nil {
				t.Fatal(err)
			}

			// 日窗口：只有今天的 5；昨天的那条绝不属于今天的日窗口
			day, err := SumTokens(d, &k.ID, nil, dayStart, dayEnd)
			if err != nil {
				t.Fatal(err)
			}
			if day != 5 {
				t.Fatalf("日窗口 sum=%d，期望 5（昨天的用量不得计入今天的日窗口）", day)
			}

			// 月窗口：今天 5 +（昨天仍在本月时）昨天 7
			sameMonth := yesterdayNoon.Month() == dayStart.Month() && yesterdayNoon.Year() == dayStart.Year()
			wantMonth := 5
			if sameMonth {
				wantMonth += 7
			}
			month, err := SumTokens(d, &k.ID, nil, monthStart, monthEnd)
			if err != nil {
				t.Fatal(err)
			}
			if month != wantMonth {
				if sameMonth {
					t.Fatalf("月窗口 sum=%d，期望 %d（昨天在月内：不计入日窗口但要计入月窗口）", month, wantMonth)
				}
				t.Fatalf("月窗口 sum=%d，期望 %d（今天 1 号，昨天属于上月，两个窗口都不计）", month, wantMonth)
			}

			// 昨天的窗口单独算：证明那条记录确实存在、只是被今天的日窗口排除
			prevDay, err := SumTokens(d, &k.ID, nil, yesterdayStart, dayStart)
			if err != nil {
				t.Fatal(err)
			}
			if prevDay != 7 {
				t.Fatalf("昨日窗口 sum=%d，期望 7（昨天的用量落在昨天的窗口里）", prevDay)
			}
			// 上月+本月 = 全部记录：窗口之间没有吞掉任何一条
			all, err := SumTokens(d, &k.ID, nil, monthStart.AddDate(0, -1, 0), monthEnd)
			if err != nil {
				t.Fatal(err)
			}
			if all != 12 {
				t.Fatalf("上月+本月 sum=%d，期望 12（5+7：两条记录都不该丢）", all)
			}
		})
	}
}

// 夏令时切换日的窗口行为（记录现状，不是期望的修正）：
// 生产公式把日窗口宽度固定为 24h（dayStart.Add(24h)），而不是「到次日本地 0 点」。
// 于是拨快那天窗口多出 1 小时（次日 00:00–01:00 的用量算进「今天」），拨慢那天
// 少 1 小时（当天 23:00–24:00 的用量既不算今天也不算明天）。
func TestSumTokensDayWindowDSTSkew(t *testing.T) {
	old := time.Local
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("no tzdata: %v", err)
	}
	time.Local = loc
	t.Cleanup(func() { time.Local = old })

	d := testDB(t)
	k, err := CreateExtKey(d, "l", "", 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	insert := func(ts time.Time, tokens int) {
		t.Helper()
		if err := InsertUsage(d, &UsageRecord{ExtKeyID: &k.ID, UpstreamName: "u", Model: "m",
			InFormat: "openai", UpFormat: "openai", TotalTokens: tokens, Status: "ok", CreatedAt: ts}); err != nil {
			t.Fatal(err)
		}
	}

	// --- 拨快日：2026-03-08 美东 02:00 → 03:00 ---
	spring := time.Date(2026, 3, 8, 12, 0, 0, 0, time.Local)
	dayStart, dayEnd, _, _ := uwWindows(spring)
	if got := dayStart.Format("2006-01-02 15:04 -0700"); got != "2026-03-08 00:00 -0500" {
		t.Fatalf("拨快日 dayStart=%s", got)
	}
	if got := dayEnd.Format("2006-01-02 15:04 -0700"); got != "2026-03-09 01:00 -0400" {
		t.Fatalf("拨快日 dayEnd=%s，期望次日 01:00（dayStart.Add(24h) 的结果）", got)
	}
	if nextMidnight := time.Date(2026, 3, 9, 0, 0, 0, 0, time.Local); dayEnd.Equal(nextMidnight) {
		t.Fatal("dayEnd 落在次日 0 点：公式或时区数据与预期不符")
	}
	insert(time.Date(2026, 3, 8, 12, 0, 0, 0, time.Local), 5)  // 日窗口内
	insert(time.Date(2026, 3, 8, 23, 30, 0, 0, time.Local), 7) // 日窗口内
	insert(time.Date(2026, 3, 9, 0, 30, 0, 0, time.Local), 11) // 次日 0:30：本地已是「明天」，但仍在 24h 窗口内
	insert(time.Date(2026, 3, 9, 1, 30, 0, 0, time.Local), 13) // 窗口外
	got, err := SumTokens(d, &k.ID, nil, dayStart, dayEnd)
	if err != nil {
		t.Fatal(err)
	}
	if got != 23 {
		t.Fatalf("拨快日日窗口 sum=%d，期望 23（5+7+11）：固定 24h 窗口会多算次日 00:00–01:00", got)
	}

	// --- 拨慢日：2026-11-01 美东 02:00 → 01:00 ---
	fall := time.Date(2026, 11, 1, 12, 0, 0, 0, time.Local)
	fStart, fEnd, _, _ := uwWindows(fall)
	if got := fStart.Format("2006-01-02 15:04 -0700"); got != "2026-11-01 00:00 -0400" {
		t.Fatalf("拨慢日 dayStart=%s", got)
	}
	if got := fEnd.Format("2006-01-02 15:04 -0700"); got != "2026-11-01 23:00 -0500" {
		t.Fatalf("拨慢日 dayEnd=%s，期望当天 23:00", got)
	}
	insert(time.Date(2026, 11, 1, 12, 0, 0, 0, time.Local), 100)
	insert(time.Date(2026, 11, 1, 22, 30, 0, 0, time.Local), 200)
	// 本地 23:30 仍是「今天」，但窗口已在 23:00 结束 → 少算一小时
	insert(time.Date(2026, 11, 1, 23, 30, 0, 0, time.Local), 400)
	got, err = SumTokens(d, &k.ID, nil, fStart, fEnd)
	if err != nil {
		t.Fatal(err)
	}
	if got != 300 {
		t.Fatalf("拨慢日日窗口 sum=%d，期望 300（100+200）：固定 24h 窗口会漏掉当天 23:00–24:00", got)
	}
	// 这一小时的用量在「明天」的窗口里也不会出现（明天从次日 0 点开始）
	nextDay := fStart.AddDate(0, 0, 1)
	got, err = SumTokens(d, &k.ID, nil, nextDay, nextDay.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Fatalf("次日窗口 sum=%d，期望 0：拨慢日 23:00–24:00 的用量两头都不算", got)
	}
}
