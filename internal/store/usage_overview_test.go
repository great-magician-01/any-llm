package store

import (
	"fmt"
	"testing"
	"time"
)

func TestUsageOverviewStatsWindowsAndUpstreamBatch(t *testing.T) {
	d := testDB(t)
	uid1, _ := CreateUpstream(d, &Upstream{Name: "u1", BaseURL: "b", APIKey: "k", Format: "openai"})
	uid2, _ := CreateUpstream(d, &Upstream{Name: "u2", BaseURL: "b", APIKey: "k", Format: "openai"})

	now := time.Now()
	dayStart, _, monthStart, _ := TokenWindows(now)
	previousMonth := monthStart.Add(-time.Hour)
	archive := dayStart.AddDate(0, 0, -400)
	insert := func(model string, total int, status string, upstreamID int64, createdAt time.Time) {
		t.Helper()
		if err := InsertUsage(d, &UsageRecord{
			UpstreamID: &upstreamID, UpstreamName: "u", Model: model,
			InFormat: "openai", UpFormat: "openai", TotalTokens: total,
			Status: status, CreatedAt: createdAt,
		}); err != nil {
			t.Fatal(err)
		}
	}
	insert("model-a", 10, "ok", uid1, now)
	insert("model-a", 5, "error", uid1, now)
	insert("model-b", 20, "ok", uid2, now)
	insert("last-month", 100, "ok", uid1, previousMonth)
	insert("archive", 200, "ok", uid2, archive)

	// 非每月 1 号时补一条“本月但不是今天”的记录，证明批量上游查询的
	// monthly 不是简单复用 daily；1 号当天两个窗口天然相同。
	hasEarlierThisMonth := now.Day() > 1
	if hasEarlierThisMonth {
		insert("earlier-month", 7, "ok", uid1, dayStart.Add(-time.Hour))
	}

	got, err := UsageOverviewStats(d)
	if err != nil {
		t.Fatal(err)
	}
	wantToday := UsageOverviewTotals{RequestCount: 3, TotalTokens: 35, OkCount: 2, ErrorCount: 1}
	if got.Today != wantToday {
		t.Fatalf("today=%+v want %+v", got.Today, wantToday)
	}
	wantMonth := wantToday
	wantAll := UsageOverviewTotals{RequestCount: 5, TotalTokens: 335, OkCount: 4, ErrorCount: 1}
	if hasEarlierThisMonth {
		wantMonth.RequestCount++
		wantMonth.TotalTokens += 7
		wantMonth.OkCount++
		wantAll.RequestCount++
		wantAll.TotalTokens += 7
		wantAll.OkCount++
	}
	if got.Month != wantMonth {
		t.Fatalf("month=%+v want %+v", got.Month, wantMonth)
	}
	if got.AllTime != wantAll {
		t.Fatalf("all_time=%+v want %+v", got.AllTime, wantAll)
	}

	wantModels := []UsageOverviewModel{
		{GroupKey: "model-b", RequestCount: 1, TotalTokens: 20, OkCount: 1},
		{GroupKey: "model-a", RequestCount: 2, TotalTokens: 15, OkCount: 1, ErrorCount: 1},
	}
	if hasEarlierThisMonth {
		wantModels = append(wantModels, UsageOverviewModel{GroupKey: "earlier-month", RequestCount: 1, TotalTokens: 7, OkCount: 1})
	}
	if len(got.Models) != len(wantModels) {
		t.Fatalf("models=%+v want %+v (历史模型不能混入本月 Top)", got.Models, wantModels)
	}
	for i := range wantModels {
		if got.Models[i] != wantModels[i] {
			t.Errorf("models[%d]=%+v want %+v", i, got.Models[i], wantModels[i])
		}
	}

	if len(got.Daily) != maxUsageDailyDays {
		t.Fatalf("daily len=%d want %d", len(got.Daily), maxUsageDailyDays)
	}
	today := got.Daily[len(got.Daily)-1]
	if today.RequestCount != wantToday.RequestCount || today.TotalTokens != wantToday.TotalTokens ||
		today.OkCount != wantToday.OkCount || today.ErrorCount != wantToday.ErrorCount {
		t.Fatalf("daily today=%+v want counts from %+v", today, wantToday)
	}

	if len(got.UpstreamTotals) != 2 {
		t.Fatalf("upstream_totals=%+v want two rows", got.UpstreamTotals)
	}
	wantUpstream1Monthly := 15
	if hasEarlierThisMonth {
		wantUpstream1Monthly += 7
	}
	if row := got.UpstreamTotals[0]; row.UpstreamID != uid1 || row.DailyTokens != 15 || row.MonthlyTokens != wantUpstream1Monthly {
		t.Errorf("upstream 1 totals=%+v want daily=15 monthly=%d", row, wantUpstream1Monthly)
	}
	if row := got.UpstreamTotals[1]; row.UpstreamID != uid2 || row.DailyTokens != 20 || row.MonthlyTokens != 20 {
		t.Errorf("upstream 2 totals=%+v want daily=20 monthly=20", row)
	}
}

func TestUsageOverviewStatsTopModelsLimitAndTieOrder(t *testing.T) {
	d := testDB(t)
	for i := 0; i < 10; i++ {
		if err := InsertUsage(d, &UsageRecord{
			Model: fmt.Sprintf("m%02d", i), TotalTokens: i/2 + 1, Status: "ok", CreatedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := UsageOverviewStats(d)
	if err != nil {
		t.Fatal(err)
	}
	// token 相同的模型按名称升序，保证不同 SQL GROUP BY 返回顺序下 UI 仍稳定。
	wantNames := []string{"m08", "m09", "m06", "m07", "m04", "m05", "m02", "m03"}
	if len(got.Models) != len(wantNames) {
		t.Fatalf("top models len=%d want %d: %+v", len(got.Models), len(wantNames), got.Models)
	}
	for i, want := range wantNames {
		if got.Models[i].GroupKey != want {
			t.Errorf("models[%d].group_key=%q want %q (all=%+v)", i, got.Models[i].GroupKey, want, got.Models)
		}
	}
}

func TestUsageOverviewStatsEmptyArrays(t *testing.T) {
	got, err := UsageOverviewStats(testDB(t))
	if err != nil {
		t.Fatal(err)
	}
	if got.Models == nil || got.UpstreamTotals == nil || got.Daily == nil {
		t.Fatalf("overview lists must encode as [] rather than null: %+v", got)
	}
	if len(got.Models) != 0 || len(got.UpstreamTotals) != 0 || len(got.Daily) != maxUsageDailyDays {
		t.Fatalf("empty overview sizes models=%d upstreams=%d daily=%d", len(got.Models), len(got.UpstreamTotals), len(got.Daily))
	}
}
