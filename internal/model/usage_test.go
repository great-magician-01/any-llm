package model

import (
	"testing"
	"time"
)

func TestInsertUsageAndSummary(t *testing.T) {
	d := testDB(t)
	uid, _ := CreateUpstream(d, &Upstream{Name: "u", BaseURL: "b", APIKey: "k", Format: "openai"})
	k, _ := CreateExtKey(d, "l", 0, 0)

	rec := &UsageRecord{
		ExtKeyID:         &k.ID,
		UpstreamID:       &uid,
		UpstreamName:     "u",
		Model:            "gpt-4o",
		InFormat:         "openai",
		UpFormat:         "openai",
		PromptTokens:     10,
		CompletionTokens: 5,
		TotalTokens:      15,
		Stream:           false,
		Status:           "ok",
	}
	if err := InsertUsage(d, rec); err != nil {
		t.Fatal(err)
	}

	// insert another with different model
	rec.Model = "gpt-4o-mini"
	rec.PromptTokens = 20
	rec.CompletionTokens = 10
	rec.TotalTokens = 30
	InsertUsage(d, rec)

	// summary by model
	summaries, err := UsageSummaryByGroup(d, "model", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 2 {
		t.Fatalf("summaries len=%d", len(summaries))
	}
	var totalTokens int
	for _, s := range summaries {
		totalTokens += s.TotalTokens
	}
	if totalTokens != 45 {
		t.Fatalf("total tokens=%d want 45", totalTokens)
	}
}

func TestUsageSummaryTimeWindow(t *testing.T) {
	d := testDB(t)
	uid, _ := CreateUpstream(d, &Upstream{Name: "u", BaseURL: "b", APIKey: "k", Format: "openai"})

	now := time.Now()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	yesterday := dayStart.Add(-24 * time.Hour)

	// one record today, one record yesterday
	InsertUsage(d, &UsageRecord{UpstreamID: &uid, UpstreamName: "u", Model: "m",
		InFormat: "openai", UpFormat: "openai", TotalTokens: 100, Status: "ok"})
	InsertUsage(d, &UsageRecord{UpstreamID: &uid, UpstreamName: "u", Model: "m",
		InFormat: "openai", UpFormat: "openai", TotalTokens: 999, Status: "ok", CreatedAt: yesterday})

	sumTokens := func(list []UsageSummary) int {
		n := 0
		for _, s := range list {
			n += s.TotalTokens
		}
		return n
	}

	// from = today 00:00, naive local format sent by the frontend (regression:
	// string params used to compare incorrectly against created_at, yielding
	// empty results for same-day windows)
	sums, err := UsageSummaryByGroup(d, "model", dayStart.Format("2006-01-02T15:04:05"), "")
	if err != nil {
		t.Fatal(err)
	}
	if got := sumTokens(sums); got != 100 {
		t.Fatalf("today window tokens=%d want 100", got)
	}

	// same, but RFC3339 with timezone offset
	sums, err = UsageSummaryByGroup(d, "model", dayStart.Format(time.RFC3339), "")
	if err != nil {
		t.Fatal(err)
	}
	if got := sumTokens(sums); got != 100 {
		t.Fatalf("today window (RFC3339) tokens=%d want 100", got)
	}

	// from = tomorrow -> nothing
	sums, err = UsageSummaryByGroup(d, "model", dayStart.Add(24*time.Hour).Format("2006-01-02T15:04:05"), "")
	if err != nil {
		t.Fatal(err)
	}
	if got := sumTokens(sums); got != 0 {
		t.Fatalf("tomorrow window tokens=%d want 0", got)
	}

	// from/to range covering only today (to = today 23:59:59, Usage page shape)
	sums, err = UsageSummaryByGroup(d, "model",
		dayStart.Format("2006-01-02T15:04:05"), dayStart.Add(24*time.Hour-time.Second).Format("2006-01-02T15:04:05"))
	if err != nil {
		t.Fatal(err)
	}
	if got := sumTokens(sums); got != 100 {
		t.Fatalf("today range tokens=%d want 100", got)
	}

	// invalid from -> error
	if _, err = UsageSummaryByGroup(d, "model", "not-a-time", ""); err == nil {
		t.Fatal("expected error for invalid from")
	}
}

func TestUsageDailyStats(t *testing.T) {
	d := testDB(t)
	uid, _ := CreateUpstream(d, &Upstream{Name: "u", BaseURL: "b", APIKey: "k", Format: "openai"})

	now := time.Now()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	yesterday := dayStart.Add(-24 * time.Hour)
	weekAgo := dayStart.AddDate(0, 0, -7)

	// today: one ok + one error
	InsertUsage(d, &UsageRecord{UpstreamID: &uid, UpstreamName: "u", Model: "m",
		InFormat: "openai", UpFormat: "openai", TotalTokens: 100, PromptTokens: 60, CompletionTokens: 40,
		CacheReadTokens: 10, ReasoningTokens: 5, Status: "ok"})
	InsertUsage(d, &UsageRecord{UpstreamID: &uid, UpstreamName: "u", Model: "m",
		InFormat: "openai", UpFormat: "openai", TotalTokens: 20, Status: "error"})
	// yesterday: one ok
	InsertUsage(d, &UsageRecord{UpstreamID: &uid, UpstreamName: "u", Model: "m",
		InFormat: "openai", UpFormat: "openai", TotalTokens: 50, Status: "ok", CreatedAt: yesterday.Add(12 * time.Hour)})
	// 7 days ago: outside a 7-day window, inside a 14-day window
	InsertUsage(d, &UsageRecord{UpstreamID: &uid, UpstreamName: "u", Model: "m",
		InFormat: "openai", UpFormat: "openai", TotalTokens: 999, Status: "ok", CreatedAt: weekAgo.Add(12 * time.Hour)})

	stats, err := UsageDailyStats(d, 7, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 7 {
		t.Fatalf("stats len=%d want 7", len(stats))
	}
	// buckets are consecutive local days ending today
	for i, s := range stats {
		want := dayStart.AddDate(0, 0, -(6 - i))
		if !s.Day.Equal(want) {
			t.Fatalf("stats[%d].Day=%v want %v", i, s.Day, want)
		}
	}
	todayStat := stats[6]
	if todayStat.RequestCount != 2 || todayStat.OkCount != 1 || todayStat.ErrorCount != 1 {
		t.Fatalf("today stat=%+v", todayStat)
	}
	if todayStat.TotalTokens != 120 || todayStat.PromptTokens != 60 || todayStat.CompletionTokens != 40 {
		t.Fatalf("today tokens=%+v", todayStat)
	}
	if todayStat.CacheReadTokens != 10 || todayStat.ReasoningTokens != 5 {
		t.Fatalf("today cache/reasoning=%+v", todayStat)
	}
	if stats[5].TotalTokens != 50 || stats[5].RequestCount != 1 {
		t.Fatalf("yesterday stat=%+v", stats[5])
	}
	// 7-day window excludes the week-ago record (bucket 0 is 6 days ago)
	for _, s := range stats {
		if s.TotalTokens == 999 {
			t.Fatal("7-day window should exclude the week-ago record")
		}
	}

	stats14, err := UsageDailyStats(d, 14, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(stats14) != 14 {
		t.Fatalf("stats14 len=%d want 14", len(stats14))
	}
	var found bool
	for _, s := range stats14 {
		if s.TotalTokens == 999 {
			found = true
		}
	}
	if !found {
		t.Fatal("14-day window should include the week-ago record")
	}

	// days is clamped
	if s, _ := UsageDailyStats(d, 0, "", ""); len(s) != 14 {
		t.Fatalf("days=0 len=%d want default 14", len(s))
	}
	if s, _ := UsageDailyStats(d, 365, "", ""); len(s) != 90 {
		t.Fatalf("days=365 len=%d want clamp 90", len(s))
	}

	// explicit from/to window: yesterday only
	ystart := dayStart.Add(-24 * time.Hour)
	statsWin, err := UsageDailyStats(d, 90,
		ystart.Format("2006-01-02T15:04:05"), dayStart.Add(24*time.Hour-time.Second).Format("2006-01-02T15:04:05"))
	if err != nil {
		t.Fatal(err)
	}
	if len(statsWin) != 2 {
		t.Fatalf("window len=%d want 2", len(statsWin))
	}
	if statsWin[0].TotalTokens != 50 || statsWin[1].TotalTokens != 120 {
		t.Fatalf("window stats=%+v %+v", statsWin[0], statsWin[1])
	}
	// invalid from -> error
	if _, err := UsageDailyStats(d, 14, "nope", dayStart.Format("2006-01-02T15:04:05")); err == nil {
		t.Fatal("expected error for invalid from")
	}
}

func TestUsageRecordsList(t *testing.T) {
	d := testDB(t)
	uid, _ := CreateUpstream(d, &Upstream{Name: "u", BaseURL: "b", APIKey: "k", Format: "openai"})
	for i := 0; i < 5; i++ {
		InsertUsage(d, &UsageRecord{
			UpstreamID: &uid, UpstreamName: "u", Model: "m",
			InFormat: "openai", UpFormat: "openai", TotalTokens: i + 1, Status: "ok",
		})
	}
	records, total, err := UsageRecordsList(d, 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 {
		t.Fatalf("total=%d want 5", total)
	}
	if len(records) != 3 {
		t.Fatalf("page len=%d want 3", len(records))
	}
	records2, _, _ := UsageRecordsList(d, 2, 3)
	if len(records2) != 2 {
		t.Fatalf("page 2 len=%d want 2", len(records2))
	}
}

func TestSumTokens(t *testing.T) {
	d := testDB(t)
	uid, _ := CreateUpstream(d, &Upstream{Name: "u", BaseURL: "b", APIKey: "k", Format: "openai"})
	k, _ := CreateExtKey(d, "l", 0, 0)

	now := time.Now()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	yesterday := dayStart.Add(-24 * time.Hour)
	tomorrow := dayStart.Add(24 * time.Hour)

	// today, ext key
	InsertUsage(d, &UsageRecord{ExtKeyID: &k.ID, UpstreamID: &uid, UpstreamName: "u", Model: "m",
		InFormat: "openai", UpFormat: "openai", TotalTokens: 100, Status: "ok"})
	// today, ext key again
	InsertUsage(d, &UsageRecord{ExtKeyID: &k.ID, UpstreamID: &uid, UpstreamName: "u", Model: "m",
		InFormat: "openai", UpFormat: "openai", TotalTokens: 30, Status: "ok"})
	// yesterday, ext key (should be excluded from today's window)
	InsertUsage(d, &UsageRecord{ExtKeyID: &k.ID, UpstreamID: &uid, UpstreamName: "u", Model: "m",
		InFormat: "openai", UpFormat: "openai", TotalTokens: 999, Status: "ok", CreatedAt: yesterday})

	// ext key, today window
	got, err := SumTokens(d, &k.ID, nil, dayStart, tomorrow)
	if err != nil {
		t.Fatal(err)
	}
	if got != 130 {
		t.Fatalf("ext key day sum=%d want 130", got)
	}
	// upstream, today window
	got, err = SumTokens(d, nil, &uid, dayStart, tomorrow)
	if err != nil {
		t.Fatal(err)
	}
	if got != 130 {
		t.Fatalf("upstream day sum=%d want 130", got)
	}
	// upstream, yesterday-only window should exclude today's rows
	got, err = SumTokens(d, nil, &uid, yesterday, dayStart)
	if err != nil {
		t.Fatal(err)
	}
	if got != 999 {
		t.Fatalf("upstream yesterday sum=%d want 999", got)
	}
	// both nil -> 0, no error
	got, err = SumTokens(d, nil, nil, dayStart, tomorrow)
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Fatalf("nil filter sum=%d want 0", got)
	}
}
