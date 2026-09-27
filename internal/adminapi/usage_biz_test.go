package adminapi

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/any-llm/internal/store"
)

// ---------------------------------------------------------------------------
// usage.go 的三个 handler（usageDaily / usageKeyTotals / usageUpstreamTotals）
// 之前 0% 覆盖。这里走 HTTP 层钉住响应结构与聚合口径。helper 前缀 biz。
// ---------------------------------------------------------------------------

func bizInt64Ptr(v int64) *int64 { return &v }

func bizItoa(id int64) string { return strconv.FormatInt(id, 10) }

func bizUsageDay(ts time.Time) string { return ts.Format("2006-01-02") }

// 业务规则：GET /api/admin/usage/daily 返回「整日桶」序列——从今天回溯 days 天、
// 无数据的日子补零桶（前端热力图/折线依赖固定长度的连续日期），桶内按
// request_count/total_tokens 聚合，status=ok/error 分别计数，跨日不串桶。
func TestUsageBiz_DailyHTTP(t *testing.T) {
	a, d := setupAPI(t)
	uid, _ := store.CreateUpstream(d, &store.Upstream{Name: "u", BaseURL: "b", APIKey: "k", Format: "openai"})

	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	yesterday := today.AddDate(0, 0, -1)

	insert := func(created time.Time, total int, status string) {
		t.Helper()
		if err := store.InsertUsage(d, &store.UsageRecord{
			UpstreamID: &uid, UpstreamName: "u", Model: "m",
			InFormat: "openai", UpFormat: "openai",
			PromptTokens: total - 1, CompletionTokens: 1, TotalTokens: total,
			Status: status, CreatedAt: created,
		}); err != nil {
			t.Fatal(err)
		}
	}
	insert(today.Add(2*time.Hour), 10, "ok")
	insert(today.Add(3*time.Hour), 20, "error")
	insert(yesterday.Add(2*time.Hour), 7, "ok")

	w := doAliasReq(t, a, "GET", "/api/admin/usage/daily?days=3", nil)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Data []struct {
			Day          string `json:"day"`
			RequestCount int    `json:"request_count"`
			TotalTokens  int    `json:"total_tokens"`
			OkCount      int    `json:"ok_count"`
			ErrorCount   int    `json:"error_count"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 3 {
		t.Fatalf("buckets=%d want 3 (days=3, zero-filled)\n%s", len(resp.Data), w.Body.String())
	}
	// 序列必须是从最早到今天、三天连续
	wantDays := []string{
		bizUsageDay(today.AddDate(0, 0, -2)),
		bizUsageDay(yesterday),
		bizUsageDay(today),
	}
	for i, want := range wantDays {
		day, err := time.Parse(time.RFC3339, resp.Data[i].Day)
		if err != nil {
			t.Fatalf("bucket %d day=%q not RFC3339: %v", i, resp.Data[i].Day, err)
		}
		if got := bizUsageDay(day); got != want {
			t.Fatalf("bucket %d day=%s want %s", i, got, want)
		}
	}
	if b := resp.Data[0]; b.RequestCount != 0 || b.TotalTokens != 0 {
		t.Fatalf("day-before-yesterday should be a zero bucket: %+v", b)
	}
	if b := resp.Data[1]; b.RequestCount != 1 || b.TotalTokens != 7 || b.OkCount != 1 || b.ErrorCount != 0 {
		t.Fatalf("yesterday bucket=%+v want 1 req / 7 tokens / 1 ok", b)
	}
	if b := resp.Data[2]; b.RequestCount != 2 || b.TotalTokens != 30 || b.OkCount != 1 || b.ErrorCount != 1 {
		t.Fatalf("today bucket=%+v want 2 req / 30 tokens / ok=1 error=1", b)
	}
}

// 业务规则：GET /api/admin/usage/key/{id} 与 /upstream/{id} 分别按外部 key、上游
// 聚合「今日 / 本月」token 总量——日窗口只含今天，月窗口只含当前自然月（上个月的
// 记录两个窗口都必须排除），且各维度只统计自己的 id（别的 key/上游的记录不串进来）。
func TestUsageBiz_KeyAndUpstreamTotals(t *testing.T) {
	a, d := setupAPI(t)

	now := time.Now()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	prevMonth := monthStart.Add(-time.Hour) // 上个月最后一天 23:00

	k1, err := store.CreateExtKey(d, "biz-k1", "", 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := store.CreateExtKey(d, "biz-k2", "", 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := store.CreateUpstream(d, &store.Upstream{Name: "u", BaseURL: "b", APIKey: "k", Format: "openai"})
	uid2, _ := store.CreateUpstream(d, &store.Upstream{Name: "u2", BaseURL: "b", APIKey: "k", Format: "openai"})

	// --- key 维度 ---
	if err := store.InsertUsage(d, &store.UsageRecord{ExtKeyID: &k1.ID, Model: "m", TotalTokens: 10, Status: "ok", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertUsage(d, &store.UsageRecord{ExtKeyID: &k2.ID, Model: "m", TotalTokens: 100, Status: "ok", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertUsage(d, &store.UsageRecord{ExtKeyID: &k1.ID, Model: "m", TotalTokens: 40, Status: "ok", CreatedAt: prevMonth}); err != nil {
		t.Fatal(err)
	}
	wantKeyMonthly := 10
	// 本月内但不是今天：只有今天不是 1 号时才存在这样的时刻（否则 monthStart 就是今天）
	if now.Day() > 1 {
		if err := store.InsertUsage(d, &store.UsageRecord{ExtKeyID: &k1.ID, Model: "m", TotalTokens: 20, Status: "ok", CreatedAt: monthStart.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		wantKeyMonthly += 20
	}

	w := doAliasReq(t, a, "GET", "/api/admin/usage/key/"+bizItoa(k1.ID), nil)
	if w.Code != 200 {
		t.Fatalf("key totals status=%d body=%s", w.Code, w.Body.String())
	}
	var keyTotals struct {
		DailyTokens   int `json:"daily_tokens"`
		MonthlyTokens int `json:"monthly_tokens"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &keyTotals); err != nil {
		t.Fatal(err)
	}
	if keyTotals.DailyTokens != 10 {
		t.Fatalf("key daily_tokens=%d want 10 (today only; the other key's 100 must not leak in)", keyTotals.DailyTokens)
	}
	if keyTotals.MonthlyTokens != wantKeyMonthly {
		t.Fatalf("key monthly_tokens=%d want %d (previous month's 40 excluded)", keyTotals.MonthlyTokens, wantKeyMonthly)
	}

	// --- upstream 维度 ---
	if err := store.InsertUsage(d, &store.UsageRecord{UpstreamID: &uid, UpstreamName: "u", Model: "m", TotalTokens: 5, Status: "ok", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertUsage(d, &store.UsageRecord{UpstreamID: &uid, UpstreamName: "u", Model: "m", TotalTokens: 7, Status: "ok", CreatedAt: prevMonth}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertUsage(d, &store.UsageRecord{UpstreamID: &uid2, UpstreamName: "u2", Model: "m", TotalTokens: 1000, Status: "ok", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}

	w = doAliasReq(t, a, "GET", "/api/admin/usage/upstream/"+bizItoa(uid), nil)
	if w.Code != 200 {
		t.Fatalf("upstream totals status=%d body=%s", w.Code, w.Body.String())
	}
	var upTotals struct {
		DailyTokens   int `json:"daily_tokens"`
		MonthlyTokens int `json:"monthly_tokens"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &upTotals); err != nil {
		t.Fatal(err)
	}
	if upTotals.DailyTokens != 5 || upTotals.MonthlyTokens != 5 {
		t.Fatalf("upstream totals=%+v want daily=5 monthly=5 (other upstream's 1000 excluded, previous month's 7 excluded)", upTotals)
	}
}

// 业务规则：GET /api/admin/usage/summary?group_by=key 的 group_key 回退链——
// 密钥 label → "#<ext_key_id>"（label 为空或 key 行已不存在）→ "—"（记录连
// ext_key_id 都没有）；同时按 ext_key_id 分组（同名不同 key 不合并），并在 key
// 维度回传 ext_key_id 供客户端区分同名行。其它维度不返回 ext_key_id。
func TestUsageBiz_SummaryKeyGroupFallback(t *testing.T) {
	a, d := setupAPI(t)

	labeled, err := store.CreateExtKey(d, "labeled-key", "", 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := store.CreateExtKey(d, "", "", 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	dup1, err := store.CreateExtKey(d, "dup-label", "", 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteExtKey(d, dup1.ID); err != nil {
		t.Fatal(err)
	}
	dup2, err := store.CreateExtKey(d, "dup-label", "", 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}

	insert := func(extKeyID *int64, total int) {
		t.Helper()
		if err := store.InsertUsage(d, &store.UsageRecord{ExtKeyID: extKeyID, Model: "m", TotalTokens: total, Status: "ok", CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	insert(&labeled.ID, 3)
	insert(&empty.ID, 5)
	insert(bizInt64Ptr(999999), 7) // key 行不存在 → 回退 #id
	insert(nil, 11)                // 连 id 都没有 → —
	insert(&dup1.ID, 13)           // 软删除的 key 行仍在，历史用量照样显示 label
	insert(&dup2.ID, 17)           // 与 dup1 同名但不同 id → 必须是两行

	w := doAliasReq(t, a, "GET", "/api/admin/usage/summary?group_by=key", nil)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Data []struct {
			GroupKey     string `json:"group_key"`
			ExtKeyID     *int64 `json:"ext_key_id"`
			RequestCount int    `json:"request_count"`
			TotalTokens  int    `json:"total_tokens"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	type rowKey struct {
		group string
		id    int64
	}
	got := map[rowKey]int{}
	for _, r := range resp.Data {
		var id int64
		if r.ExtKeyID != nil {
			id = *r.ExtKeyID
		}
		got[rowKey{r.GroupKey, id}] = r.TotalTokens
	}
	want := []struct {
		group string
		id    int64
		total int
	}{
		{"labeled-key", labeled.ID, 3},
		{"#" + bizItoa(empty.ID), empty.ID, 5},
		{"#999999", 999999, 7},
		{"—", 0, 11},
		{"dup-label", dup1.ID, 13},
		{"dup-label", dup2.ID, 17},
	}
	for _, wnt := range want {
		if total, ok := got[rowKey{wnt.group, wnt.id}]; !ok {
			t.Errorf("missing row group_key=%q ext_key_id=%d (rows=%+v)", wnt.group, wnt.id, resp.Data)
		} else if total != wnt.total {
			t.Errorf("row group_key=%q ext_key_id=%d total=%d want %d", wnt.group, wnt.id, total, wnt.total)
		}
	}
	if len(resp.Data) != len(want) {
		t.Fatalf("rows=%d want %d (same-label keys must stay separate rows)\n%+v", len(resp.Data), len(want), resp.Data)
	}
	// 同名不同 id 两行都必须带 ext_key_id，客户端才能区分
	for _, r := range resp.Data {
		if r.GroupKey == "dup-label" && r.ExtKeyID == nil {
			t.Fatalf("key-dimension rows must carry ext_key_id: %+v", r)
		}
	}

	// 其它维度不带 ext_key_id
	w = doAliasReq(t, a, "GET", "/api/admin/usage/summary?group_by=upstream", nil)
	if w.Code != 200 {
		t.Fatalf("upstream summary status=%d", w.Code)
	}
	if body := w.Body.String(); strings.Contains(body, "ext_key_id") {
		t.Fatalf("upstream-dimension rows must not carry ext_key_id: %s", body)
	}
}

// 业务规则：GET /api/admin/usage/records 分页——按 id 倒序、页与页不重叠、
// total 是未分页的全量计数、超范围页返回空数组但 total 仍是全量，
// size 非法（0/负/超 200）回落成默认 50。
func TestUsageBiz_RecordsPagination(t *testing.T) {
	a, d := setupAPI(t)
	for i := 1; i <= 5; i++ {
		if err := store.InsertUsage(d, &store.UsageRecord{Model: "m", TotalTokens: i, Status: "ok"}); err != nil {
			t.Fatal(err)
		}
	}

	fetch := func(query string) ([]int64, int) {
		t.Helper()
		w := doAliasReq(t, a, "GET", "/api/admin/usage/records?"+query, nil)
		if w.Code != 200 {
			t.Fatalf("%s status=%d body=%s", query, w.Code, w.Body.String())
		}
		var resp struct {
			Data []struct {
				ID int64 `json:"id"`
			} `json:"data"`
			Total int `json:"total"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		ids := make([]int64, 0, len(resp.Data))
		for _, r := range resp.Data {
			ids = append(ids, r.ID)
		}
		return ids, resp.Total
	}

	p1, total := fetch("page=1&size=2")
	if total != 5 || len(p1) != 2 {
		t.Fatalf("page1 ids=%v total=%d want 2 ids / total 5", p1, total)
	}
	p2, total2 := fetch("page=2&size=2")
	if total2 != 5 || len(p2) != 2 {
		t.Fatalf("page2 ids=%v total=%d want 2 ids / total 5", p2, total2)
	}
	for _, id := range p1 {
		for _, id2 := range p2 {
			if id == id2 {
				t.Fatalf("pages overlap on id %d (p1=%v p2=%v)", id, p1, p2)
			}
		}
	}
	// 倒序：第一页的每条都必须比第二页的每条新
	if !(p1[1] > p2[0]) {
		t.Fatalf("records not ordered by id DESC: p1=%v p2=%v", p1, p2)
	}
	if _, total := fetch("page=9&size=2"); total != 5 {
		t.Fatalf("out-of-range page must still report the full total, got %d", total)
	}
	if ids, _ := fetch("page=9&size=2"); len(ids) != 0 {
		t.Fatalf("out-of-range page must be empty, got %v", ids)
	}
	// size=0 → 默认 50 → 全部 5 条
	if ids, _ := fetch("page=1&size=0"); len(ids) != 5 {
		t.Fatalf("size=0 must fall back to the default page size, got %d rows", len(ids))
	}
}
