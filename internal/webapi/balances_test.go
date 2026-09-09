package webapi

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/great-magician-01/any-llm/internal/model"
)

func TestListLatestBalances(t *testing.T) {
	a, d := setupAPI(t)
	model.InsertBalanceSnapshot(d, &model.BalanceSnapshot{UpstreamID: 1, UpstreamName: "ds", Vendor: "deepseek", Payload: json.RawMessage(`{"kind":"balance","is_available":true,"balances":[{"currency":"CNY","total":"110.00","granted":"10.00","topped_up":"100.00"}]}`)})
	model.InsertBalanceSnapshot(d, &model.BalanceSnapshot{UpstreamID: 1, UpstreamName: "ds", Vendor: "deepseek", Payload: json.RawMessage(`{"kind":"balance","is_available":false,"balances":[]}`)})
	model.InsertBalanceSnapshot(d, &model.BalanceSnapshot{UpstreamID: 2, UpstreamName: "kimi", Vendor: "kimi-coding", Payload: json.RawMessage(`{"kind":"quota","windows":[{"id":"weekly","used_percent":"26.0"}]}`)})

	req := httptest.NewRequest("GET", "/api/admin/balances", nil)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Data []model.BalanceSnapshot `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("len=%d", len(resp.Data))
	}
	// Latest per upstream: upstream 1's newest snapshot (is_available=false).
	for _, s := range resp.Data {
		if s.UpstreamID == 1 {
			var p struct {
				IsAvailable bool `json:"is_available"`
			}
			if err := json.Unmarshal(s.Payload, &p); err != nil {
				t.Fatal(err)
			}
			if p.IsAvailable {
				t.Fatalf("upstream 1 latest payload=%s", s.Payload)
			}
		}
	}
}

func TestBalanceHistory(t *testing.T) {
	a, d := setupAPI(t)
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "ds", BaseURL: "https://api.deepseek.com", APIKey: "k", Format: "openai"})
	for i := 0; i < 5; i++ {
		model.InsertBalanceSnapshot(d, &model.BalanceSnapshot{UpstreamID: uid, UpstreamName: "ds", Vendor: "deepseek", Payload: json.RawMessage(`{"kind":"balance","is_available":true,"balances":[]}`)})
	}

	req := httptest.NewRequest("GET", "/api/admin/upstreams/"+strconv.FormatInt(uid, 10)+"/balances?page=1&size=3", nil)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Data  []model.BalanceSnapshot `json:"data"`
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
	uid, _ := model.CreateUpstream(d, &model.Upstream{Name: "oai", BaseURL: "https://api.openai.com/v1", APIKey: "k", Format: "openai"})

	req := httptest.NewRequest("POST", "/api/admin/upstreams/"+strconv.FormatInt(uid, 10)+"/balances/refresh", nil)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] == nil {
		t.Fatalf("resp=%+v", resp)
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
	model.CreateUpstream(d, &model.Upstream{Name: "oai", BaseURL: "https://api.openai.com/v1", APIKey: "k", Format: "openai"})

	req := httptest.NewRequest("POST", "/api/admin/balances", nil)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Data []model.BalanceSnapshot `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 0 {
		t.Fatalf("expected no snapshots for unsupported upstreams, got %+v", resp.Data)
	}
}
