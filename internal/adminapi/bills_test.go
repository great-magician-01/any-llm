package adminapi

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/any-llm/internal/store"
)

func billBucketSum(buckets []store.BillBucketStat) int64 {
	var sum int64
	for _, bucket := range buckets {
		for _, amount := range bucket.Amounts {
			sum += amount.AmountMicros
		}
	}
	return sum
}

func TestBillsCRUD(t *testing.T) {
	a, d := setupAPI(t)
	uid, err := store.CreateUpstream(d, &store.Upstream{Name: "ds", BaseURL: "https://api.deepseek.com", APIKey: "k", Format: "openai"})
	if err != nil {
		t.Fatal(err)
	}

	// 记一笔
	req := httptest.NewRequest("POST", "/api/admin/bills", strings.NewReader(
		`{"upstream_id":`+bizItoa(uid)+`,"date":"2026-10-15","amount":"12.50","note":"订阅费"}`))
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
	req = httptest.NewRequest("PUT", "/api/admin/bills/"+bizItoa(b.ID), strings.NewReader(
		`{"upstream_id":`+bizItoa(uid)+`,"date":"2026-10-16","amount":"20.00","note":"改"}`))
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
		if sum := billBucketSum(sumResp.Buckets); sum != 20_000_000 {
			t.Fatalf("summary %s: buckets sum=%d", q, sum)
		}
	}

	// 删除
	req = httptest.NewRequest("DELETE", "/api/admin/bills/"+bizItoa(b.ID), nil)
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

	req := httptest.NewRequest("PUT", "/api/admin/bills/"+bizItoa(auto.ID), strings.NewReader(
		`{"upstream_id":`+bizItoa(uid)+`,"date":"2026-10-15","amount":"99"}`))
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("update auto: status=%d body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest("DELETE", "/api/admin/bills/"+bizItoa(auto.ID), nil)
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

// 记账联动（经 store.RecordBalanceSnapshot 冒烟：快照+账单同一事务；真厂商
// fetch 与 handler 串接由 internal/upstream 的 poller/契约测试覆盖）。
func TestRecordBalanceSnapshotFlow(t *testing.T) {
	_, d := setupAPI(t)
	uid, _ := store.CreateUpstream(d, &store.Upstream{Name: "ds", BaseURL: "https://api.deepseek.com", APIKey: "k", Format: "openai"})
	snapFor := func(total string, at time.Time) *store.BalanceSnapshot {
		t.Helper()
		payload := `{"kind":"balance","is_available":true,"balances":[{"currency":"CNY","total":"` + total + `","granted":"0","topped_up":"` + total + `"}]}`
		return &store.BalanceSnapshot{UpstreamID: uid, UpstreamName: "ds", Vendor: "deepseek", Payload: json.RawMessage(payload), CreatedAt: at}
	}
	if _, err := store.RecordBalanceSnapshot(d, snapFor("110.00", time.Now().Add(-time.Hour))); err != nil {
		t.Fatal(err)
	}
	// 第二次：余额下降 → 自动落一笔账单
	if n, err := store.RecordBalanceSnapshot(d, snapFor("108.35", time.Now())); err != nil || n != 1 {
		t.Fatalf("record: n=%d err=%v", n, err)
	}
	list, total, _ := store.BillsList(d, &uid, time.Time{}, time.Time{}, 1, 10)
	if total != 1 || list[0].AmountMicros != 1_650_000 || list[0].Kind != "auto" {
		t.Fatalf("bills=%+v", list)
	}
}

// 编辑请求体缺省（表单不发 currency）时不得覆盖原币种：否则非 CNY 手动账单
// 一被编辑就被静默改成 CNY。
func TestBillsUpdateKeepsCurrency(t *testing.T) {
	a, d := setupAPI(t)
	uid, _ := store.CreateUpstream(d, &store.Upstream{Name: "oai", BaseURL: "https://api.openai.com/v1", APIKey: "k", Format: "openai"})

	req := httptest.NewRequest("POST", "/api/admin/bills", strings.NewReader(
		`{"upstream_id":`+bizItoa(uid)+`,"date":"2026-10-15","amount":"10.00","currency":"USD"}`))
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("create: status=%d body=%s", w.Code, w.Body.String())
	}
	var created struct {
		Data store.Bill
	}
	json.Unmarshal(w.Body.Bytes(), &created)

	// 编辑不带 currency 字段（与前端 submitForm 的 body 一致）
	req = httptest.NewRequest("PUT", "/api/admin/bills/"+bizItoa(created.Data.ID), strings.NewReader(
		`{"upstream_id":`+bizItoa(uid)+`,"date":"2026-10-16","amount":"12.00","note":"x"}`))
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("update: status=%d body=%s", w.Code, w.Body.String())
	}
	var updated struct {
		Data store.Bill
	}
	json.Unmarshal(w.Body.Bytes(), &updated)
	if updated.Data.Currency != "USD" {
		t.Fatalf("currency=%q, want USD kept", updated.Data.Currency)
	}
	// 显式给出 currency 时才覆盖
	req = httptest.NewRequest("PUT", "/api/admin/bills/"+bizItoa(created.Data.ID), strings.NewReader(
		`{"upstream_id":`+bizItoa(uid)+`,"date":"2026-10-16","amount":"12.00","currency":"EUR"}`))
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	json.Unmarshal(w.Body.Bytes(), &updated)
	if updated.Data.Currency != "EUR" {
		t.Fatalf("currency=%q, want EUR", updated.Data.Currency)
	}
}

// 汇总的图表与统计卡必须统计同一个窗口：清空日期范围（不传 from/to）时两者
// 都是缺省回看窗口，账单（落在 now 附近）在两侧都能看到且数字一致。
func TestBillsSummaryCoherentWindow(t *testing.T) {
	a, d := setupAPI(t)
	uid, _ := store.CreateUpstream(d, &store.Upstream{Name: "ds", BaseURL: "https://api.deepseek.com", APIKey: "k", Format: "openai"})
	b := &store.Bill{UpstreamID: uid, UpstreamName: "ds", Kind: store.BillKindAuto, AmountMicros: 1_650_000, Currency: "CNY", BilledAt: time.Now()}
	if err := store.InsertBill(d, b); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/api/admin/bills/summary?bucket=day", nil)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("summary: status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Buckets   []store.BillBucketStat    `json:"buckets"`
		Upstreams []store.BillUpstreamTotal `json:"upstreams"`
		Kinds     []store.BillKindTotal     `json:"kinds"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	bucketSum := billBucketSum(resp.Buckets)
	var upstreamSum int64
	for _, u := range resp.Upstreams {
		upstreamSum += u.AmountMicros
	}
	var kindSum int64
	for _, k := range resp.Kinds {
		kindSum += k.AmountMicros
	}
	if bucketSum != 1_650_000 || upstreamSum != 1_650_000 || kindSum != 1_650_000 {
		t.Fatalf("incoherent window: buckets=%d upstreams=%d kinds=%d", bucketSum, upstreamSum, kindSum)
	}
}
