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
