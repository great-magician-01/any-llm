package model

import (
	"testing"
)

func TestInsertUsageAndSummary(t *testing.T) {
	d := testDB(t)
	uid, _ := CreateUpstream(d, &Upstream{Name: "u", BaseURL: "b", APIKey: "k", Format: "openai"})
	k, _ := CreateExtKey(d, "l")

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
