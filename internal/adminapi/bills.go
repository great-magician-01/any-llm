package adminapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/great-magician-01/any-llm/internal/logger"
	"github.com/great-magician-01/any-llm/internal/store"
)

// 账单：手动记账（选上游、日期、金额）与自动记账（余额快照联动，由
// balances.go 的快照落库路径写入）的查询/维护入口。自动账单不可编辑，
// 但允许删除（余额推导出错时人工清理，见 store.DeleteBill）。

// billDayParam 把 "YYYY-MM-DD" 查询参数解析成本地当日零点；exclusive 时回
// 次日零点（与 store 侧 [from, to) 半开窗口配套的日期闭区间语义）。空串回零值
// = 不限。
func billDayParam(s string, exclusive bool) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.ParseInLocation("2006-01-02", s, time.Local)
	if err != nil {
		return time.Time{}, err
	}
	if exclusive {
		t = t.AddDate(0, 0, 1)
	}
	return t, nil
}

// billUpstreamFilter 解析可选的 upstream_id 查询参数；缺省/空回 nil（不过滤）。
func billUpstreamFilter(r *http.Request) (*int64, error) {
	s := r.URL.Query().Get("upstream_id")
	if s == "" {
		return nil, nil
	}
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		return nil, errors.New("invalid upstream_id")
	}
	return &id, nil
}

// listBills serves GET /api/admin/bills?upstream_id=&from=&to=&page=&size=
// （from/to 为 "YYYY-MM-DD"，按账单归属日期的闭区间过滤）。
func (a *API) listBills(w http.ResponseWriter, r *http.Request) {
	upstreamID, err := billUpstreamFilter(r)
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	from, err := billDayParam(r.URL.Query().Get("from"), false)
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": "invalid from: want YYYY-MM-DD"})
		return
	}
	to, err := billDayParam(r.URL.Query().Get("to"), true)
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": "invalid to: want YYYY-MM-DD"})
		return
	}
	page, size := pageParams(r)
	bills, total, err := store.BillsList(a.db, upstreamID, from, to, page, size)
	if err != nil {
		logger.Error("admin: list bills failed", "err", err)
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"data": bills, "total": total})
}

// billSummary serves GET /api/admin/bills/summary?bucket=day|month|year&from=&to=
// &upstream_id=：趋势桶聚合 + 按上游合计，一次往返喂饱账单页的图表与汇总表。
func (a *API) billSummary(w http.ResponseWriter, r *http.Request) {
	bucket := r.URL.Query().Get("bucket")
	if bucket == "" {
		bucket = "day"
	}
	upstreamID, err := billUpstreamFilter(r)
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	// 日期参数先在本层校验出 400（store.BillTrend 也解析一遍，双保险只走展示）
	for _, pair := range [][2]string{{"from", r.URL.Query().Get("from")}, {"to", r.URL.Query().Get("to")}} {
		if pair[1] == "" {
			continue
		}
		if _, err := time.ParseInLocation("2006-01-02", pair[1], time.Local); err != nil {
			writeJSON(w, 400, map[string]any{"error": "invalid " + pair[0] + ": want YYYY-MM-DD"})
			return
		}
	}
	buckets, err := store.BillTrend(a.db, bucket, upstreamID, r.URL.Query().Get("from"), r.URL.Query().Get("to"))
	if err != nil {
		if errors.Is(err, store.ErrInvalidBucket) {
			writeJSON(w, 400, map[string]any{"error": err.Error()})
			return
		}
		logger.Error("admin: bill trend failed", "bucket", bucket, "err", err)
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	// 按上游合计不受桶数截断影响：直接用日期换算的半开窗口查全量。
	trendFrom, _ := billDayParam(r.URL.Query().Get("from"), false)
	trendTo, _ := billDayParam(r.URL.Query().Get("to"), true)
	totals, err := store.BillUpstreamTotals(a.db, upstreamID, trendFrom, trendTo)
	if err != nil {
		logger.Error("admin: bill upstream totals failed", "err", err)
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	kinds, err := store.BillKindTotals(a.db, upstreamID, trendFrom, trendTo)
	if err != nil {
		logger.Error("admin: bill kind totals failed", "err", err)
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"buckets": buckets, "upstreams": totals, "kinds": kinds})
}

// billInput 是手动记账的请求体。金额用十进制字符串传递（避免 JS number 的
// 浮点误差直接进账本），服务端解析成 micros。
type billInput struct {
	UpstreamID int64  `json:"upstream_id"`
	Date       string `json:"date"` // YYYY-MM-DD
	Amount     string `json:"amount"`
	Currency   string `json:"currency"`
	Note       string `json:"note"`
}

// validate 校验输入并把金额解析成 micros。错误文案直接面向管理端用户。
func (in *billInput) validate() (int64, error) {
	if in.UpstreamID <= 0 {
		return 0, errors.New("upstream_id 必填")
	}
	if _, err := time.ParseInLocation("2006-01-02", in.Date, time.Local); err != nil {
		return 0, errors.New("date 格式应为 YYYY-MM-DD")
	}
	amount, err := store.ParseAmountMicros(in.Amount)
	if err != nil {
		return 0, errors.New("金额格式不正确")
	}
	if amount <= 0 {
		return 0, errors.New("金额必须大于 0")
	}
	return amount, nil
}

// createBill serves POST /api/admin/bills：记一笔手动账单。
func (a *API) createBill(w http.ResponseWriter, r *http.Request) {
	var in billInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, 400, map[string]any{"error": "invalid JSON body"})
		return
	}
	amount, err := in.validate()
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	u, err := store.GetUpstreamByID(a.db, in.UpstreamID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, 400, map[string]any{"error": "上游不存在"})
			return
		}
		logger.Error("admin: create bill get upstream failed", "err", err)
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	billed, _ := time.ParseInLocation("2006-01-02", in.Date, time.Local)
	b := &store.Bill{
		UpstreamID:   u.ID,
		UpstreamName: u.Name,
		Kind:         store.BillKindManual,
		AmountMicros: amount,
		Currency:     billCurrencyOrDefault(in.Currency),
		BilledAt:     billed,
		Note:         strings.TrimSpace(in.Note),
	}
	if err := a.writeSync(func(d *sql.DB) error { return store.InsertBill(d, b) }); err != nil {
		logger.Error("admin: create bill failed", "err", err)
		writeSyncErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"data": b})
}

// billCurrencyOrDefault 归一化币种：缺省 CNY（全部受支持的余额厂商都是人民币）。
func billCurrencyOrDefault(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	if s == "" {
		return "CNY"
	}
	return s
}

// updateBill serves PUT /api/admin/bills/{id}：编辑手动记账。自动记账是余额
// 推导的客观记录，不允许改（改了就和快照对不上）；要修正请删除后手动补记。
func (a *API) updateBill(w http.ResponseWriter, r *http.Request, id int64) {
	var in billInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, 400, map[string]any{"error": "invalid JSON body"})
		return
	}
	amount, err := in.validate()
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	b, err := store.GetBill(a.db, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, 404, map[string]any{"error": "not found"})
			return
		}
		logger.Error("admin: update bill get failed", "id", id, "err", err)
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	if b.Kind != store.BillKindManual {
		writeJSON(w, 400, map[string]any{"error": "自动记账不可编辑，如需修正请删除后手动补记"})
		return
	}
	u, err := store.GetUpstreamByID(a.db, in.UpstreamID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, 400, map[string]any{"error": "上游不存在"})
			return
		}
		logger.Error("admin: update bill get upstream failed", "err", err)
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	billed, _ := time.ParseInLocation("2006-01-02", in.Date, time.Local)
	b.UpstreamID = u.ID
	b.UpstreamName = u.Name
	b.AmountMicros = amount
	b.Currency = billCurrencyOrDefault(in.Currency)
	b.BilledAt = billed
	b.Note = strings.TrimSpace(in.Note)
	if err := a.writeSync(func(d *sql.DB) error { return store.UpdateBill(d, b) }); err != nil {
		logger.Error("admin: update bill failed", "id", id, "err", err)
		writeSyncErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"data": b})
}

// deleteBill serves DELETE /api/admin/bills/{id}：删除任意来源的账单（自动记账
// 算错时的人工出口）。
func (a *API) deleteBill(w http.ResponseWriter, r *http.Request, id int64) {
	if _, err := store.GetBill(a.db, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, 404, map[string]any{"error": "not found"})
			return
		}
		logger.Error("admin: delete bill get failed", "id", id, "err", err)
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	if err := a.writeSync(func(d *sql.DB) error { return store.DeleteBill(d, id) }); err != nil {
		logger.Error("admin: delete bill failed", "id", id, "err", err)
		writeSyncErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
