package upstream

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/great-magician-01/any-llm/internal/store"
)

func TestBalanceVendor(t *testing.T) {
	cases := []struct {
		baseURL string
		want    string
	}{
		{"https://api.deepseek.com", VendorDeepSeek},
		{"https://api.deepseek.com/v1", VendorDeepSeek},
		{"https://api.kimi.com/coding", VendorKimiCoding},
		{"https://API.KIMI.COM", VendorKimiCoding},
		{"https://api.stepfun.com/v1", VendorStepFun},
		{"https://api.stepfun.ai/v1", VendorStepFun},
		{"https://api.openai.com/v1", ""},
		{"not a url", ""},
		{"", ""},
	}
	for _, c := range cases {
		got := BalanceVendor(&store.Upstream{BaseURL: c.baseURL})
		if got != c.want {
			t.Errorf("BalanceVendor(%q)=%q want %q", c.baseURL, got, c.want)
		}
	}
}

// registerTestHost maps an httptest server's host to a vendor so FetchBalance
// can be exercised end-to-end without hitting the real vendor endpoints.
func registerTestHost(t *testing.T, rawURL, vendor string) {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	host := parsed.Hostname()
	balanceVendorHosts[host] = vendor
	t.Cleanup(func() { delete(balanceVendorHosts, host) })
}

func TestFetchBalance_DeepSeek(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/balance" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Fatalf("auth=%s", r.Header.Get("Authorization"))
		}
		w.Write([]byte(`{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"110.00","granted_balance":"10.00","topped_up_balance":"100.00"}]}`))
	}))
	defer srv.Close()
	registerTestHost(t, srv.URL, VendorDeepSeek)

	u := &store.Upstream{Name: "ds", BaseURL: srv.URL, APIKey: "sk-test"}
	vendor, payload, err := FetchBalance(context.Background(), http.DefaultClient, u)
	if err != nil {
		t.Fatal(err)
	}
	if vendor != VendorDeepSeek {
		t.Fatalf("vendor=%q", vendor)
	}
	var p struct {
		Kind        string `json:"kind"`
		IsAvailable bool   `json:"is_available"`
		Balances    []struct {
			Currency string `json:"currency"`
			Total    string `json:"total"`
			Granted  string `json:"granted"`
			ToppedUp string `json:"topped_up"`
		} `json:"balances"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.Kind != "balance" || !p.IsAvailable || len(p.Balances) != 1 {
		t.Fatalf("payload=%s", payload)
	}
	b := p.Balances[0]
	if b.Currency != "CNY" || b.Total != "110.00" || b.Granted != "10.00" || b.ToppedUp != "100.00" {
		t.Fatalf("balance=%+v", b)
	}
}

func TestFetchBalance_KimiCoding(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/coding/v1/usages" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk-kimi-test" {
			t.Fatalf("auth=%s", r.Header.Get("Authorization"))
		}
		w.Write([]byte(`{
			"usage": {"limit": "100", "used": "26", "remaining": "74", "resetTime": "2026-08-11T15:53:05Z"},
			"limits": [{"window": {"duration": 300, "timeUnit": "TIME_UNIT_MINUTE"},
				"detail": {"limit": "100", "used": "27", "remaining": "73", "resetTime": "2026-08-08T14:53:05Z"}}],
			"totalQuota": {"limit": "1000", "used": "500", "remaining": "500"},
			"subType": "TYPE_PURCHASE"
		}`))
	}))
	defer srv.Close()
	registerTestHost(t, srv.URL, VendorKimiCoding)

	u := &store.Upstream{Name: "kimi", BaseURL: srv.URL, APIKey: "sk-kimi-test"}
	vendor, payload, err := FetchBalance(context.Background(), http.DefaultClient, u)
	if err != nil {
		t.Fatal(err)
	}
	if vendor != VendorKimiCoding {
		t.Fatalf("vendor=%q", vendor)
	}
	var p struct {
		Kind    string `json:"kind"`
		Windows []struct {
			ID          string `json:"id"`
			UsedPercent string `json:"used_percent"`
			ResetAt     string `json:"reset_at"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.Kind != "quota" || len(p.Windows) != 3 {
		t.Fatalf("payload=%s", payload)
	}
	w5h := p.Windows[0]
	if w5h.ID != "five_hour" || w5h.UsedPercent != "27.0" || w5h.ResetAt != "2026-08-08T14:53:05Z" {
		t.Fatalf("five_hour=%+v", w5h)
	}
	wk := p.Windows[1]
	if wk.ID != "weekly" || wk.UsedPercent != "26.0" || wk.ResetAt != "2026-08-11T15:53:05Z" {
		t.Fatalf("weekly=%+v", wk)
	}
	mo := p.Windows[2]
	if mo.ID != "monthly" || mo.UsedPercent != "50.0" || mo.ResetAt != "" {
		t.Fatalf("monthly=%+v", mo)
	}
}

// TestNormalizeKimiCodingUsage_FaultTolerance: missing/unparsable windows are
// skipped individually instead of failing the whole snapshot.
func TestNormalizeKimiCodingUsage_FaultTolerance(t *testing.T) {
	payload, err := normalizeKimiCodingUsage([]byte(`{
		"usage": {"limit": "100", "used": "40", "resetTime": "2026-08-11T00:00:00Z"},
		"limits": [{"window": {"duration": 300, "timeUnit": "TIME_UNIT_MINUTE"}, "detail": {"used": "oops", "limit": "100"}}],
		"totalQuota": {}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		Windows []struct {
			ID          string `json:"id"`
			UsedPercent string `json:"used_percent"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Windows) != 1 || p.Windows[0].ID != "weekly" || p.Windows[0].UsedPercent != "40.0" {
		t.Fatalf("payload=%s", payload)
	}
}

func TestFetchBalance_StepFun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/accounts" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk-stepfun-test" {
			t.Fatalf("auth=%s", r.Header.Get("Authorization"))
		}
		// 官方文档示例：金额为 JSON 数字（与 deepseek 的字符串不同）
		w.Write([]byte(`{"object":"account","type":"prepaid","balance":26.00,"total_cash_balance":0.00,"total_voucher_balance":26.00}`))
	}))
	defer srv.Close()
	registerTestHost(t, srv.URL, VendorStepFun)

	u := &store.Upstream{Name: "stepfun", BaseURL: srv.URL, APIKey: "sk-stepfun-test"}
	vendor, payload, err := FetchBalance(context.Background(), http.DefaultClient, u)
	if err != nil {
		t.Fatal(err)
	}
	if vendor != VendorStepFun {
		t.Fatalf("vendor=%q", vendor)
	}
	var p struct {
		Kind        string `json:"kind"`
		IsAvailable bool   `json:"is_available"`
		Balances    []struct {
			Currency string `json:"currency"`
			Total    string `json:"total"`
			Granted  string `json:"granted"`
			ToppedUp string `json:"topped_up"`
		} `json:"balances"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.Kind != "balance" || !p.IsAvailable || len(p.Balances) != 1 {
		t.Fatalf("payload=%s", payload)
	}
	b := p.Balances[0]
	if b.Currency != "CNY" || b.Total != "26.00" || b.Granted != "26.00" || b.ToppedUp != "0.00" {
		t.Fatalf("balance=%+v", b)
	}
}

// TestNormalizeStepFunAccount_Availability: is_available 由账户语义推导——
// postpaid 先用后付恒可用；prepaid 余额大于零才可用；字段缺失按 0 处理。
func TestNormalizeStepFunAccount_Availability(t *testing.T) {
	cases := []struct {
		body string
		want bool
	}{
		{`{"type":"postpaid","balance":0.00,"total_cash_balance":0.00,"total_voucher_balance":0.00}`, true},
		{`{"type":"prepaid","balance":0.00,"total_cash_balance":0.00,"total_voucher_balance":0.00}`, false},
		{`{"type":"prepaid","balance":1.50,"total_cash_balance":1.50,"total_voucher_balance":0.00}`, true},
	}
	for _, c := range cases {
		payload, err := normalizeStepFunAccount([]byte(c.body))
		if err != nil {
			t.Fatal(err)
		}
		var p struct {
			IsAvailable bool `json:"is_available"`
		}
		if err := json.Unmarshal(payload, &p); err != nil {
			t.Fatal(err)
		}
		if p.IsAvailable != c.want {
			t.Errorf("body=%s available=%v want %v", c.body, p.IsAvailable, c.want)
		}
	}
	// 金额字段整体缺失时条目按 "0" 归一化，前端不会出现裸币种符号
	payload, err := normalizeStepFunAccount([]byte(`{"object":"account","type":"prepaid"}`))
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		Balances []struct {
			Total    string `json:"total"`
			Granted  string `json:"granted"`
			ToppedUp string `json:"topped_up"`
		} `json:"balances"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Balances) != 1 || p.Balances[0].Total != "0" || p.Balances[0].Granted != "0" || p.Balances[0].ToppedUp != "0" {
		t.Fatalf("payload=%s", payload)
	}
}

func TestFetchBalance_Unsupported(t *testing.T) {
	u := &store.Upstream{Name: "oai", BaseURL: "https://api.openai.com/v1", APIKey: "k"}
	if _, _, err := FetchBalance(context.Background(), http.DefaultClient, u); err == nil {
		t.Fatal("expected error for unsupported upstream")
	}
}

func TestFetchBalance_UpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":{"message":"Authentication Fails"}}`))
	}))
	defer srv.Close()
	registerTestHost(t, srv.URL, VendorDeepSeek)

	u := &store.Upstream{Name: "ds", BaseURL: srv.URL, APIKey: "bad"}
	if _, _, err := FetchBalance(context.Background(), http.DefaultClient, u); err == nil {
		t.Fatal("expected error for 401")
	}
}

// TestFetchBalance_ErrorBodyTruncated: the returned error carries only a
// short prefix of the vendor error body — admin handlers relay the error into
// JSON responses, so a huge vendor error page must not pass through in full.
func TestFetchBalance_ErrorBodyTruncated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(strings.Repeat("x", 5000)))
	}))
	defer srv.Close()
	registerTestHost(t, srv.URL, VendorDeepSeek)

	u := &store.Upstream{Name: "ds", BaseURL: srv.URL, APIKey: "k"}
	_, _, err := FetchBalance(context.Background(), http.DefaultClient, u)
	if err == nil {
		t.Fatal("expected error for 500")
	}
	// "upstream 500: " + 512 chars + marker ≈ 541; 600 leaves headroom.
	if len(err.Error()) > 600 {
		t.Fatalf("error body not truncated: len=%d", len(err.Error()))
	}
	if !strings.Contains(err.Error(), "...(truncated)") {
		t.Fatalf("error missing truncation marker: %q", err.Error()[:100])
	}
}
