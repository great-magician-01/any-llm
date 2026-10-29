package adminapi

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/any-llm/internal/store"
)

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

func TestBillsCRUD(t *testing.T) {
	a, d := setupAPI(t)
	uid, err := store.CreateUpstream(d, &store.Upstream{Name: "ds", BaseURL: "https://api.deepseek.com", APIKey: "k", Format: "openai"})
	if err != nil {
		t.Fatal(err)
	}

	// 记一笔
	req := httptest.NewRequest("POST", "/api/admin/bills", strings.NewReader(
		`{"upstream_id":`+itoa(uid)+`,"date":"2026-10-15","amount":"12.50","note":"订阅费"}`))
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("create: status=%d body=%s", w.Code, w.Body.String())
	}
	var created struct {
		Data store.Bill
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	b := created.Data
	if b.ID == 0 || b.Kind != "manual" || b.AmountMicros != 12_500_000 || b.Currency != "CNY" {
		t.Fatalf("created=%+v", b)
	}
	if !b.BilledAt.Equal(time.Date(2026, 10, 15, 0, 0, 0, 0, time.Local)) {
		t.Fatalf("billed_at=%v", b.BilledAt)
	}

	// 编辑
	req = httptest.NewRequest("PUT", "/api/admin/bills/"+itoa(b.ID), strings.NewReader(
		`{"upstream_id":`+itoa(uid)+`,"date":"2026-10-16","amount":"20.00","note":"改"}`))
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("update: status=%d body=%s", w.Code, w.Body.String())
	}

	// 列表带过滤
	req = httptest.NewRequest("GET", "/api/admin/bills?from=2026-10-16&to=2026-10-16", nil)
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("list: status=%d body=%s", w.Code, w.Body.String())
	}
	var listResp struct {
		Data  []store.Bill `json:"data"`
		Total int          `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatal(err)
	}
	if listResp.Total != 1 || listResp.Data[0].AmountMicros != 20_000_000 || listResp.Data[0].Note != "改" {
		t.Fatalf("list=%+v", listResp)
	}

	// 汇总：日/月/年桶 + 按上游
	for _, q := range []string{"bucket=day&from=2026-10-01&to=2026-10-31", "bucket=month&from=2026-10-01&to=2026-10-31", "bucket=year"} {
		req = httptest.NewRequest("GET", "/api/admin/bills/summary?"+q, nil)
		w = httptest.NewRecorder()
		a.Handler().ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("summary %s: status=%d body=%s", q, w.Code, w.Body.String())
		}
		var sumResp struct {
			Buckets   []store.BillBucketStat    `json:"buckets"`
			Upstreams []store.BillUpstreamTotal `json:"upstreams"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &sumResp); err != nil {
			t.Fatal(err)
		}
		if len(sumResp.Upstreams) != 1 || sumResp.Upstreams[0].AmountMicros != 20_000_000 {
			t.Fatalf("summary %s: upstreams=%+v", q, sumResp.Upstreams)
		}
		var sum int64
		for _, bk := range sumResp.Buckets {
			sum += bk.AmountMicros
		}
		if sum != 20_000_000 {
			t.Fatalf("summary %s: buckets sum=%d", q, sum)
		}
	}

	// 删除
	req = httptest.NewRequest("DELETE", "/api/admin/bills/"+itoa(b.ID), nil)
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("delete: status=%d body=%s", w.Code, w.Body.String())
	}
	req = httptest.NewRequest("GET", "/api/admin/bills", nil)
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	var after struct {
		Total int `json:"total"`
	}
	json.Unmarshal(w.Body.Bytes(), &after)
	if after.Total != 0 {
		t.Fatalf("after delete total=%d", after.Total)
	}
}

func TestBillsValidation(t *testing.T) {
	a, d := setupAPI(t)
	uid, _ := store.CreateUpstream(d, &store.Upstream{Name: "ds", BaseURL: "https://api.deepseek.com", APIKey: "k", Format: "openai"})
	if uid != 1 {
		t.Fatalf("fresh db: first upstream id=%d, cases assume 1", uid)
	}

	cases := []struct {
		name string
		body string
	}{
		{"金额非数字", `{"upstream_id":1,"date":"2026-10-15","amount":"abc"}`},
		{"金额为零", `{"upstream_id":1,"date":"2026-10-15","amount":"0"}`},
		{"金额为负", `{"upstream_id":1,"date":"2026-10-15","amount":"-5"}`},
		{"金额缺失", `{"upstream_id":1,"date":"2026-10-15"}`},
		{"日期格式错", `{"upstream_id":1,"date":"2026/10/15","amount":"1"}`},
		{"上游缺失", `{"date":"2026-10-15","amount":"1"}`},
		{"上游不存在", `{"upstream_id":9999,"date":"2026-10-15","amount":"1"}`},
	}
	for _, c := range cases {
		req := httptest.NewRequest("POST", "/api/admin/bills", strings.NewReader(c.body))
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, req)
		if w.Code != 400 {
			t.Errorf("%s: status=%d body=%s", c.name, w.Code, w.Body.String())
		}
	}

	// 查询参数校验
	for _, q := range []string{"?upstream_id=abc", "?from=2026/10/01", "?to=not-a-date"} {
		req := httptest.NewRequest("GET", "/api/admin/bills"+q, nil)
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, req)
		if w.Code != 400 {
			t.Errorf("list %s: status=%d", q, w.Code)
		}
	}
	req := httptest.NewRequest("GET", "/api/admin/bills/summary?bucket=week", nil)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("summary bad bucket: status=%d", w.Code)
	}
}

// 自动记账是余额推导的客观记录：不可编辑（要修正就删掉后手动补记），但可删除。
func TestBillsAutoKindImmutable(t *testing.T) {
	a, d := setupAPI(t)
	uid, _ := store.CreateUpstream(d, &store.Upstream{Name: "ds", BaseURL: "https://api.deepseek.com", APIKey: "k", Format: "openai"})
	auto := &store.Bill{UpstreamID: uid, UpstreamName: "ds", Kind: store.BillKindAuto, AmountMicros: 1_000_000, Currency: "CNY", BilledAt: time.Now()}
	if err := store.InsertBill(d, auto); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("PUT", "/api/admin/bills/"+itoa(auto.ID), strings.NewReader(
		`{"upstream_id":`+itoa(uid)+`,"date":"2026-10-15","amount":"99"}`))
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("update auto: status=%d body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest("DELETE", "/api/admin/bills/"+itoa(auto.ID), nil)
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("delete auto: status=%d body=%s", w.Code, w.Body.String())
	}

	// 不存在的账单：更新与删除都 404
	for _, m := range []string{"PUT", "DELETE"} {
		var body *strings.Reader
		if m == "PUT" {
			body = strings.NewReader(`{"upstream_id":1,"date":"2026-10-15","amount":"1"}`)
		} else {
			body = strings.NewReader("")
		}
		req = httptest.NewRequest(m, "/api/admin/bills/99999", body)
		w = httptest.NewRecorder()
		a.Handler().ServeHTTP(w, req)
		if w.Code != 404 {
			t.Fatalf("%s missing: status=%d", m, w.Code)
		}
	}
}

// 余额快照落库后的记账联动（经 store 层冒烟；真厂商 fetch 不在测试范围）。
func TestRefreshBalanceRecordsBill(t *testing.T) {
	_, d := setupAPI(t)
	uid, _ := store.CreateUpstream(d, &store.Upstream{Name: "ds", BaseURL: "https://api.deepseek.com", APIKey: "k", Format: "openai"})
	insertSnapFor := func(total string) {
		t.Helper()
		payload := `{"kind":"balance","is_available":true,"balances":[{"currency":"CNY","total":"` + total + `","granted":"0","topped_up":"` + total + `"}]}`
		if err := store.InsertBalanceSnapshot(d, &store.BalanceSnapshot{UpstreamID: uid, UpstreamName: "ds", Vendor: "deepseek", Payload: json.RawMessage(payload)}); err != nil {
			t.Fatal(err)
		}
	}
	insertSnapFor("110.00")
	// 手工模拟「新快照落库后的记账联动」（fetch 真厂商不在测试范围内）
	insertSnapFor("108.35")
	snaps, _, err := store.BalanceSnapshotsList(d, uid, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := store.RecordBillFromBalanceDelta(d, &snaps[0]); err != nil || n != 1 {
		t.Fatalf("record: n=%d err=%v", n, err)
	}
	list, total, _ := store.BillsList(d, &uid, time.Time{}, time.Time{}, 1, 10)
	if total != 1 || list[0].AmountMicros != 1_650_000 || list[0].Kind != "auto" {
		t.Fatalf("bills=%+v", list)
	}
}
