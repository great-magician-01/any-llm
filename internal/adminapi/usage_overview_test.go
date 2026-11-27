package adminapi

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/great-magician-01/any-llm/internal/store"
)

func TestUsageOverviewHTTP(t *testing.T) {
	a, d := setupAPI(t)
	uid, _ := store.CreateUpstream(d, &store.Upstream{Name: "u", BaseURL: "b", APIKey: "k", Format: "openai"})
	if err := store.InsertUsage(d, &store.UsageRecord{
		UpstreamID: &uid, UpstreamName: "u", Model: "m", TotalTokens: 12,
		Status: "ok", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	w := doAliasReq(t, a, "GET", "/api/admin/usage/overview", nil)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var got store.UsageOverview
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Today.RequestCount != 1 || got.Today.TotalTokens != 12 || got.Month != got.Today || got.AllTime != got.Today {
		t.Fatalf("overview totals today=%+v month=%+v all=%+v", got.Today, got.Month, got.AllTime)
	}
	if len(got.Models) != 1 || got.Models[0].GroupKey != "m" || got.Models[0].TotalTokens != 12 {
		t.Fatalf("models=%+v", got.Models)
	}
	if len(got.Daily) != 370 || got.Daily[len(got.Daily)-1].TotalTokens != 12 {
		t.Fatalf("daily len=%d last=%+v", len(got.Daily), got.Daily[len(got.Daily)-1])
	}
	if len(got.UpstreamTotals) != 1 || got.UpstreamTotals[0].UpstreamID != uid ||
		got.UpstreamTotals[0].DailyTokens != 12 || got.UpstreamTotals[0].MonthlyTokens != 12 {
		t.Fatalf("upstream_totals=%+v", got.UpstreamTotals)
	}
}
