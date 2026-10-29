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

// listBills serves GET /api/admin/bills?upstream_id=&from=&to=&bucket=&page=&size=
// （from/to 为 "YYYY-MM-DD"，按账单归属日期的闭区间过滤）。任一端缺省时与
// summary 一样通过 BillWindow 补全默认窗口；两端都给时保持精确日期且不受桶上限影响。
func (a *API) listBills(w http.ResponseWriter, r *http.Request) {
	upstreamID, err := billUpstreamFilter(r)
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	fromParam, toParam := r.URL.Query().Get("from"), r.URL.Query().Get("to")
	var from, to time.Time
	if fromParam != "" && toParam != "" {
		from, err = billDayParam(fromParam, false)
		if err != nil {
			writeJSON(w, 400, map[string]any{"error": "invalid from: want YYYY-MM-DD"})
			return
		}
		to, err = billDayParam(toParam, true)
		if err != nil {
			writeJSON(w, 400, map[string]any{"error": "invalid to: want YYYY-MM-DD"})
			return
		}
	} else {
		bucket := r.URL.Query().Get("bucket")
		if bucket == "" {
			bucket = "day"
		}
		from, to, err = store.BillWindow(bucket, fromParam, toParam)
		if err != nil {
			writeJSON(w, 400, map[string]any{"error": err.Error()})
			return
		}
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
// &upstream_id=：趋势桶聚合 + 按上游/按来源合计，一次往返喂饱账单页的图表与
// 汇总表。三份聚合共用 store.BillWindow 解析出的同一个窗口——响应里的所有数字
// 统计同一段时间的账，清空日期范围时也是同一缺省回看窗口，不会出现「图表近
// 30 天、统计卡全部历史」的混搭。
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
	// bucket 非法与日期格式错都是调用方输入问题 → 400（BillWindow 统一校验）
	start, end, err := store.BillWindow(bucket, r.URL.Query().Get("from"), r.URL.Query().Get("to"))
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	// 三份聚合不仅要同窗口，还要同数据快照。显式 Serializable + ReadOnly
	// 避免 PG 默认 ReadCommitted 在三条 SELECT 之间看到新提交。
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true})
	if err != nil {
		logger.Error("admin: begin bill summary transaction failed", "err", err)
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	defer tx.Rollback()
	buckets, err := store.BillTrendTx(tx, a.db, bucket, upstreamID, start, end)
	if err != nil {
		logger.Error("admin: bill trend failed", "bucket", bucket, "err", err)
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	totals, err := store.BillUpstreamTotalsTx(tx, a.db, upstreamID, start, end)
	if err != nil {
		logger.Error("admin: bill upstream totals failed", "err", err)
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	kinds, err := store.BillKindTotalsTx(tx, a.db, upstreamID, start, end)
	if err != nil {
		logger.Error("admin: bill kind totals failed", "err", err)
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	if err := tx.Commit(); err != nil {
		logger.Error("admin: commit bill summary transaction failed", "err", err)
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

// validate 校验输入，返回解析好的金额（micros）与账单归属时刻（所选日期的
// 本地零点）。错误文案直接面向管理端用户；日期只在这里解析一次，调用方直接
// 用返回值——重复解析+丢错误会让非法日期静默变成零值（InsertBill 回退为 now，
// 记错账）。
func (in *billInput) validate() (amount int64, billed time.Time, err error) {
	if in.UpstreamID <= 0 {
		return 0, time.Time{}, errors.New("upstream_id 必填")
	}
	billed, err = time.ParseInLocation("2006-01-02", in.Date, time.Local)
	if err != nil {
		return 0, time.Time{}, errors.New("date 格式应为 YYYY-MM-DD")
	}
	amount, err = store.ParseAmountMicros(in.Amount)
	if err != nil {
		return 0, time.Time{}, errors.New("金额格式不正确")
	}
	if amount <= 0 {
		return 0, time.Time{}, errors.New("金额必须大于 0")
	}
	return amount, billed, nil
}

// createBill serves POST /api/admin/bills：记一笔手动账单。
func (a *API) createBill(w http.ResponseWriter, r *http.Request) {
	var in billInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, 400, map[string]any{"error": "invalid JSON body"})
		return
	}
	amount, billed, err := in.validate()
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

var (
	errBillNotFound         = errors.New("bill not found")
	errBillNotManual        = errors.New("自动记账不可编辑，如需修正请删除后手动补记")
	errBillUpstreamNotFound = errors.New("上游不存在")
)

// updateBill serves PUT /api/admin/bills/{id}：编辑手动记账。自动记账是余额
// 推导的客观记录，不允许改（改了就和快照对不上）；要修正请删除后手动补记。
func (a *API) updateBill(w http.ResponseWriter, r *http.Request, id int64) {
	var in billInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, 400, map[string]any{"error": "invalid JSON body"})
		return
	}
	amount, billed, err := in.validate()
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	var b *store.Bill
	internalErr := false
	// GetBill、kind 校验与 UPDATE 必须和 DELETE 共用同一 writer 串行区：
	// 否则 DELETE 可插在预查询和更新之间，0 行更新仍被误报为 200。
	err = a.writeSync(func(d *sql.DB) error {
		var err error
		b, err = store.GetBill(d, id)
		if errors.Is(err, sql.ErrNoRows) {
			return errBillNotFound
		}
		if err != nil {
			internalErr = true
			return err
		}
		if b.Kind != store.BillKindManual {
			return errBillNotManual
		}
		u, err := store.GetUpstreamByID(d, in.UpstreamID)
		if errors.Is(err, sql.ErrNoRows) {
			return errBillUpstreamNotFound
		}
		if err != nil {
			internalErr = true
			return err
		}
		b.UpstreamID = u.ID
		b.UpstreamName = u.Name
		b.AmountMicros = amount
		// 币种只在显式给出时才改：编辑表单不发 currency，缺省覆盖会把非 CNY 账单
		// 静默改币（¥/$ 错位、币种合计全乱）
		if in.Currency != "" {
			b.Currency = billCurrencyOrDefault(in.Currency)
		}
		b.BilledAt = billed
		b.Note = strings.TrimSpace(in.Note)
		return store.UpdateBill(d, b)
	})
	if err != nil {
		switch {
		case errors.Is(err, errBillNotFound):
			writeJSON(w, 404, map[string]any{"error": "not found"})
		case errors.Is(err, errBillNotManual), errors.Is(err, errBillUpstreamNotFound):
			writeJSON(w, 400, map[string]any{"error": err.Error()})
		default:
			logger.Error("admin: update bill failed", "id", id, "err", err)
			if internalErr {
				writeJSON(w, 500, map[string]any{"error": err.Error()})
			} else {
				writeSyncErr(w, 400, err)
			}
		}
		return
	}
	writeJSON(w, 200, map[string]any{"data": b})
}

// deleteBill serves DELETE /api/admin/bills/{id}：删除任意来源的账单（自动记账
// 算错时的人工出口）。存在性由 DELETE 的受影响行数判定，省一次预查询。
func (a *API) deleteBill(w http.ResponseWriter, r *http.Request, id int64) {
	var deleted int64
	if err := a.writeSync(func(d *sql.DB) (err error) {
		deleted, err = store.DeleteBill(d, id)
		return err
	}); err != nil {
		logger.Error("admin: delete bill failed", "id", id, "err", err)
		writeSyncErr(w, 500, err)
		return
	}
	if deleted == 0 {
		writeJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
