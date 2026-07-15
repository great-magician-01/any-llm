package webapi

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/great-magician-01/any-llm/internal/model"
)

func TestUsageSummary(t *testing.T) {
	a, d := setupAPI(t)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "u", BaseURL: "b", APIKey: "k", Format: "openai"})
	model.InsertUsage(d, &model.UsageRecord{UpstreamID: &uid, UpstreamName: "u", Model: "gpt-4o", InFormat: "openai", UpFormat: "openai", TotalTokens: 10, Status: "ok"})
	model.InsertUsage(d, &model.UsageRecord{UpstreamID: &uid, UpstreamName: "u", Model: "gpt-4o", InFormat: "openai", UpFormat: "openai", TotalTokens: 20, Status: "ok"})
	model.InsertUsage(d, &model.UsageRecord{UpstreamID: &uid, UpstreamName: "u", Model: "gpt-4o-mini", InFormat: "openai", UpFormat: "openai", TotalTokens: 5, Status: "ok"})

	req := httptest.NewRequest("GET", "/api/admin/usage/summary?group_by=model", nil)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Data []map[string]any `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Data) != 2 {
		t.Fatalf("groups=%d", len(resp.Data))
	}
}

func TestUsageRecords(t *testing.T) {
	a, d := setupAPI(t)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "u", BaseURL: "b", APIKey: "k", Format: "openai"})
	for i := 0; i < 5; i++ {
		model.InsertUsage(d, &model.UsageRecord{UpstreamID: &uid, UpstreamName: "u", Model: "m", InFormat: "openai", UpFormat: "openai", TotalTokens: i + 1, Status: "ok"})
	}

	req := httptest.NewRequest("GET", "/api/admin/usage/records?page=1&size=3", nil)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d", w.Code)
	}
	var resp struct {
		Data  []map[string]any `json:"data"`
		Total int              `json:"total"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Total != 5 || len(resp.Data) != 3 {
		t.Fatalf("total=%d len=%d", resp.Total, len(resp.Data))
	}
}
