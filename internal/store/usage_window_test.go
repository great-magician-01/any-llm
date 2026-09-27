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
//   - 日窗口的终点是**次日本地 0 点**（store.TokenWindows 用 AddDate(0,0,1)），
//     所以夏令时切换日（23/25 小时）也覆盖完整的一天——见
//     TestSumTokensDayWindowDSTSkew。
//
// 窗口公式只有一份（store.TokenWindows，网关配额与管理端展示共用），这里直接
// 调用它，不做复刻：复刻的副本无法在公式变化时报警。

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
	dayStart, dayEnd, monthStart, monthEnd := TokenWindows(ref)
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

			dayStart, dayEnd, monthStart, monthEnd := TokenWindows(ref.now)
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

// 夏令时切换日的窗口行为（回归位）：日窗口是「本地午夜到次日本地午夜」，
// 而不是固定 24 小时。固定 24h 会让拨快日的窗口多出一小时（次日 00:00–01:00
// 的用量被算进「今天」），拨慢日少一小时（当天 23:00–24:00 既不算今天也不算
// 明天 —— 那一小时的用量对日限额完全免费，可被用来绕过日限额）。
//
// 之前窗口公式在网关/管理端各写一遍、store 里只能复刻一份来测，所以这条
// 缺陷在三处同时存在且无人发现；现在三处都走 store.TokenWindows。
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

	// --- 拨快日：2026-03-08 美东 02:00 → 03:00（这一天只有 23 小时）---
	spring := time.Date(2026, 3, 8, 12, 0, 0, 0, time.Local)
	dayStart, dayEnd, _, _ := TokenWindows(spring)
	if got := dayStart.Format("2006-01-02 15:04 -0700"); got != "2026-03-08 00:00 -0500" {
		t.Fatalf("拨快日 dayStart=%s", got)
	}
	if got := dayEnd.Format("2006-01-02 15:04 -0700"); got != "2026-03-09 00:00 -0400" {
		t.Fatalf("拨快日 dayEnd=%s，期望次日本地 0 点", got)
	}
	insert(time.Date(2026, 3, 8, 12, 0, 0, 0, time.Local), 5)  // 日窗口内
	insert(time.Date(2026, 3, 8, 23, 30, 0, 0, time.Local), 7) // 日窗口内
	insert(time.Date(2026, 3, 9, 0, 30, 0, 0, time.Local), 11) // 已是次日：不在今天窗口内
	insert(time.Date(2026, 3, 9, 1, 30, 0, 0, time.Local), 13) // 更是在窗口外
	got, err := SumTokens(d, &k.ID, nil, dayStart, dayEnd)
	if err != nil {
		t.Fatal(err)
	}
	if got != 12 {
		t.Fatalf("拨快日日窗口 sum=%d，期望 12（5+7）：窗口终点是次日本地 0 点，不能多算次日 00:00–01:00", got)
	}
	// 次日 0:30 的用量属于次日窗口（窗口之间不重不漏）。
	nextStart, nextEnd, _, _ := TokenWindows(time.Date(2026, 3, 9, 12, 0, 0, 0, time.Local))
	got, err = SumTokens(d, &k.ID, nil, nextStart, nextEnd)
	if err != nil {
		t.Fatal(err)
	}
	if got != 24 {
		t.Fatalf("次日窗口 sum=%d，期望 24（11+13）", got)
	}

	// --- 拨慢日：2026-11-01 美东 02:00 → 01:00（这一天有 25 小时）---
	fall := time.Date(2026, 11, 1, 12, 0, 0, 0, time.Local)
	fStart, fEnd, _, _ := TokenWindows(fall)
	if got := fStart.Format("2006-01-02 15:04 -0700"); got != "2026-11-01 00:00 -0400" {
		t.Fatalf("拨慢日 dayStart=%s", got)
	}
	if got := fEnd.Format("2006-01-02 15:04 -0700"); got != "2026-11-02 00:00 -0500" {
		t.Fatalf("拨慢日 dayEnd=%s，期望次日本地 0 点（不是当天 23:00）", got)
	}
	insert(time.Date(2026, 11, 1, 12, 0, 0, 0, time.Local), 100)
	insert(time.Date(2026, 11, 1, 22, 30, 0, 0, time.Local), 200)
	insert(time.Date(2026, 11, 1, 23, 30, 0, 0, time.Local), 400) // 本地仍是当天，必须计入
	got, err = SumTokens(d, &k.ID, nil, fStart, fEnd)
	if err != nil {
		t.Fatal(err)
	}
	if got != 700 {
		t.Fatalf("拨慢日日窗口 sum=%d，期望 700（100+200+400）：不能漏掉当天 23:00–24:00（那一小时曾对日限额完全免费）", got)
	}
	// 那 25 小时被今天完整覆盖，次日窗口必须为空（不重不漏）。
	nextStart, nextEnd, _, _ = TokenWindows(time.Date(2026, 11, 2, 12, 0, 0, 0, time.Local))
	got, err = SumTokens(d, &k.ID, nil, nextStart, nextEnd)
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Fatalf("次日窗口 sum=%d，期望 0：拨慢日的 23:00–24:00 必须落在当天窗口里", got)
	}
}
