package adminapi

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/great-magician-01/any-llm/internal/store"
)

func TestUsageSummary(t *testing.T) {
	a, d := setupAPI(t)
	uid, _ := store.CreateUpstream(d, &store.Upstream{Name: "u", BaseURL: "b", APIKey: "k", Format: "openai"})
	store.InsertUsage(d, &store.UsageRecord{UpstreamID: &uid, UpstreamName: "u", Model: "gpt-4o", InFormat: "openai", UpFormat: "openai", TotalTokens: 10, Status: "ok"})
	store.InsertUsage(d, &store.UsageRecord{UpstreamID: &uid, UpstreamName: "u", Model: "gpt-4o", InFormat: "openai", UpFormat: "openai", TotalTokens: 20, Status: "ok"})
	store.InsertUsage(d, &store.UsageRecord{UpstreamID: &uid, UpstreamName: "u", Model: "gpt-4o-mini", InFormat: "openai", UpFormat: "openai", TotalTokens: 5, Status: "ok"})

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
	uid, _ := store.CreateUpstream(d, &store.Upstream{Name: "u", BaseURL: "b", APIKey: "k", Format: "openai"})
	for i := 0; i < 5; i++ {
		store.InsertUsage(d, &store.UsageRecord{UpstreamID: &uid, UpstreamName: "u", Model: "m", InFormat: "openai", UpFormat: "openai", TotalTokens: i + 1, Status: "ok"})
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

// 业务规则：请求明细按模型筛选——total 必须是筛选后的行数（前端分页靠它翻
// 页），且返回行只含该模型；/usage/models 给出去重后的可选模型清单。
func TestUsageRecordsModelFilterAndModels(t *testing.T) {
	a, d := setupAPI(t)
	for _, m := range []string{"deepseek-chat", "deepseek-chat", "gpt-4o"} {
		store.InsertUsage(d, &store.UsageRecord{Model: m, InFormat: "openai", UpFormat: "openai", TotalTokens: 1, Status: "ok"})
	}

	// 筛选：total=2，行内模型全为 deepseek-chat
	req := httptest.NewRequest("GET", "/api/admin/usage/records?model=deepseek-chat", nil)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("records status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Data  []map[string]any `json:"data"`
		Total int              `json:"total"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Total != 2 || len(resp.Data) != 2 {
		t.Fatalf("filtered total=%d len=%d want 2/2", resp.Total, len(resp.Data))
	}
	for _, row := range resp.Data {
		if row["model"] != "deepseek-chat" {
			t.Fatalf("filtered row model=%v want deepseek-chat", row["model"])
		}
	}

	// 无 model 参数 = 不过滤
	req = httptest.NewRequest("GET", "/api/admin/usage/records", nil)
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	resp = struct {
		Data  []map[string]any `json:"data"`
		Total int              `json:"total"`
	}{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Total != 3 {
		t.Fatalf("unfiltered total=%d want 3", resp.Total)
	}

	// 选项清单：去重有序
	req = httptest.NewRequest("GET", "/api/admin/usage/models", nil)
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("models status=%d body=%s", w.Code, w.Body.String())
	}
	var mresp struct {
		Data []string `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &mresp)
	want := []string{"deepseek-chat", "gpt-4o"}
	if len(mresp.Data) != len(want) {
		t.Fatalf("models=%v want %v", mresp.Data, want)
	}
	for i := range want {
		if mresp.Data[i] != want[i] {
			t.Fatalf("models=%v want %v", mresp.Data, want)
		}
	}
}
