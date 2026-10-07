package adminapi

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/great-magician-01/any-llm/internal/store"
)

func TestListLatestBalances(t *testing.T) {
	a, d := setupAPI(t)
	dsID, _ := store.CreateUpstream(d, &store.Upstream{Name: "ds", BaseURL: "https://api.deepseek.com", APIKey: "k", Format: "openai"})
	kimiID, _ := store.CreateUpstream(d, &store.Upstream{Name: "kimi", BaseURL: "https://api.kimi.com/coding", APIKey: "k", Format: "openai"})
	store.InsertBalanceSnapshot(d, &store.BalanceSnapshot{UpstreamID: dsID, UpstreamName: "ds", Vendor: "deepseek", Payload: json.RawMessage(`{"kind":"balance","is_available":true,"balances":[{"currency":"CNY","total":"110.00","granted":"10.00","topped_up":"100.00"}]}`)})
	store.InsertBalanceSnapshot(d, &store.BalanceSnapshot{UpstreamID: dsID, UpstreamName: "ds", Vendor: "deepseek", Payload: json.RawMessage(`{"kind":"balance","is_available":false,"balances":[]}`)})
	store.InsertBalanceSnapshot(d, &store.BalanceSnapshot{UpstreamID: kimiID, UpstreamName: "kimi", Vendor: "kimi-coding", Payload: json.RawMessage(`{"kind":"quota","windows":[{"id":"weekly","used_percent":"26.0"}]}`)})

	req := httptest.NewRequest("GET", "/api/admin/balances", nil)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Data []store.BalanceSnapshot `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("len=%d", len(resp.Data))
	}
	// Latest per upstream: the deepseek upstream's newest snapshot
	// (is_available=false).
	for _, s := range resp.Data {
		if s.UpstreamID == dsID {
			var p struct {
				IsAvailable bool `json:"is_available"`
			}
			if err := json.Unmarshal(s.Payload, &p); err != nil {
				t.Fatal(err)
			}
			if p.IsAvailable {
				t.Fatalf("deepseek latest payload=%s", s.Payload)
			}
		}
	}
}

// 业务规则：GET /api/admin/balances 只下发「当前仍支持余额抓取」的上游的快照。
// Step Plan 通道（/step_plan）的套餐月池与钱包余额是两套账，上游改指过去之后
// 旧钱包快照必须立刻消失，不能挂着误导；改回来则恢复显示（快照本身不删）。
func TestListLatestBalances_FiltersUnsupportedVendors(t *testing.T) {
	a, d := setupAPI(t)
	dsID, _ := store.CreateUpstream(d, &store.Upstream{Name: "ds", BaseURL: "https://api.deepseek.com", APIKey: "k", Format: "openai"})
	planID, _ := store.CreateUpstream(d, &store.Upstream{Name: "plan", BaseURL: "https://api.stepfun.com/step_plan", APIKey: "k", Format: "anthropic"})
	oaiID, _ := store.CreateUpstream(d, &store.Upstream{Name: "oai", BaseURL: "https://api.openai.com/v1", APIKey: "k", Format: "openai"})
	payload := json.RawMessage(`{"kind":"balance","is_available":true,"balances":[{"currency":"CNY","total":"1.00","granted":"0","topped_up":"1.00"}]}`)
	for _, id := range []int64{dsID, planID, oaiID} {
		store.InsertBalanceSnapshot(d, &store.BalanceSnapshot{UpstreamID: id, UpstreamName: "x", Vendor: "deepseek", Payload: payload})
	}

	latestIDs := func() map[int64]bool {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/admin/balances", nil)
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		var resp struct {
			Data []store.BalanceSnapshot `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		ids := make(map[int64]bool, len(resp.Data))
		for _, s := range resp.Data {
			ids[s.UpstreamID] = true
		}
		return ids
	}

	ids := latestIDs()
	if !ids[dsID] || ids[planID] || ids[oaiID] {
		t.Fatalf("ids=%v want only the deepseek upstream (step_plan/unsupported filtered)", ids)
	}

	// 上游改指 Step Plan 通道后，它的钱包余额快照也要藏起来。
	u, err := store.GetUpstreamByID(d, dsID)
	if err != nil {
		t.Fatal(err)
	}
	u.BaseURL = "https://api.stepfun.com/step_plan/v1"
	if err := store.UpdateUpstream(d, u); err != nil {
		t.Fatal(err)
	}
	if ids := latestIDs(); len(ids) != 0 {
		t.Fatalf("ids=%v want empty after repointing to step_plan", ids)
	}
}

func TestBalanceHistory(t *testing.T) {
	a, d := setupAPI(t)
	uid, _ := store.CreateUpstream(d, &store.Upstream{Name: "ds", BaseURL: "https://api.deepseek.com", APIKey: "k", Format: "openai"})
	for i := 0; i < 5; i++ {
		store.InsertBalanceSnapshot(d, &store.BalanceSnapshot{UpstreamID: uid, UpstreamName: "ds", Vendor: "deepseek", Payload: json.RawMessage(`{"kind":"balance","is_available":true,"balances":[]}`)})
	}

	req := httptest.NewRequest("GET", "/api/admin/upstreams/"+strconv.FormatInt(uid, 10)+"/balances?page=1&size=3", nil)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Data  []store.BalanceSnapshot `json:"data"`
		Total int                     `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Total != 5 || len(resp.Data) != 3 {
		t.Fatalf("total=%d len=%d", resp.Total, len(resp.Data))
	}
}

func TestRefreshBalance_Unsupported(t *testing.T) {
	a, d := setupAPI(t)
	for _, u := range []store.Upstream{
		{Name: "oai", BaseURL: "https://api.openai.com/v1", APIKey: "k", Format: "openai"},
		// Step Plan 订阅通道同样不支持：月池 Credit 无 API 可查
		{Name: "plan", BaseURL: "https://api.stepfun.com/step_plan", APIKey: "k", Format: "anthropic"},
	} {
		uid, err := store.CreateUpstream(d, &u)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest("POST", "/api/admin/upstreams/"+strconv.FormatInt(uid, 10)+"/balances/refresh", nil)
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, req)
		if w.Code != 400 {
			t.Fatalf("%s: status=%d body=%s", u.Name, w.Code, w.Body.String())
		}
		var resp map[string]any
		json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["error"] == nil {
			t.Fatalf("%s: resp=%+v", u.Name, resp)
		}
	}
}

func TestRefreshBalance_NotFound(t *testing.T) {
	a, _ := setupAPI(t)
	req := httptest.NewRequest("POST", "/api/admin/upstreams/999/balances/refresh", nil)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestRefreshAllBalances_UnsupportedOnly(t *testing.T) {
	a, d := setupAPI(t)
	store.CreateUpstream(d, &store.Upstream{Name: "oai", BaseURL: "https://api.openai.com/v1", APIKey: "k", Format: "openai"})

	req := httptest.NewRequest("POST", "/api/admin/balances", nil)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Data []store.BalanceSnapshot `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 0 {
		t.Fatalf("expected no snapshots for unsupported upstreams, got %+v", resp.Data)
	}
}
