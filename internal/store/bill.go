package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/great-magician-01/any-llm/internal/db"
	"github.com/great-magician-01/any-llm/internal/logger"
)

// 账单：上游消费的货币流水。两个来源——
//   - kind=auto：厂商余额快照联动。每笔新快照经 RecordBalanceSnapshot 与上一条
//     对比，余额差额 > 0 的币种自动入账；quota 类快照（kimi 的百分比窗口）没有
//     金额语义，不产生账单。
//   - kind=manual：管理端手动记一笔（选上游、日期、金额）。
//
// 金额一律存 amount_micros（1e-6 币种单位的整数），SQL SUM 是精确整数运算，
// 展示层再除回来——动机与 balance payload 保持金额原串一样：不让浮点进账本。

// Bill 是一条账单记录。
type Bill struct {
	ID           int64     `json:"id"`
	UpstreamID   int64     `json:"upstream_id"`
	UpstreamName string    `json:"upstream_name"`
	Kind         string    `json:"kind"` // auto | manual
	AmountMicros int64     `json:"amount_micros"`
	Currency     string    `json:"currency"`
	BilledAt     time.Time `json:"billed_at"`
	Note         string    `json:"note"`
	// Detail 仅 auto 记账填充：推导依据的 JSON 文本（prev/new 余额、vendor、
	// 关联快照 id）
	Detail    string    `json:"detail,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

const (
	BillKindAuto   = "auto"
	BillKindManual = "manual"
)

// microsScale 是金额的最小小数位数（1e-6 币种单位）。
const microsScale = 6

// ParseAmountMicros 把十进制金额串转成 1e-6 单位的整数（自动记账的余额差额与
// 手动记账的用户输入共用这一个入口，口径只有一份）。
func ParseAmountMicros(s string) (int64, error) { return microsFromDecimal(s) }

// microsFromDecimal 把厂商/用户给的十进制金额串转成 1e-6 单位的整数。
// 逐字符解析而非 ParseFloat：金额串要保持精度（"12.34" 不允许变 12.339999…）；
// 超过 6 位的小数按第 7 位四舍五入到 micros。
func microsFromDecimal(s string) (int64, error) {
	s = strings.TrimSpace(s)
	neg := false
	switch {
	case strings.HasPrefix(s, "-"):
		neg, s = true, s[1:]
	case strings.HasPrefix(s, "+"):
		s = s[1:]
	}
	intPart, fracPart, hasDot := strings.Cut(s, ".")
	if intPart == "" && (!hasDot || fracPart == "") {
		return 0, fmt.Errorf("not a number: %q", s)
	}
	for i := 0; i < len(fracPart); i++ {
		if fracPart[i] < '0' || fracPart[i] > '9' {
			return 0, fmt.Errorf("not a number: %q", s)
		}
	}
	round := false
	if len(fracPart) > microsScale {
		round = fracPart[microsScale] >= '5'
		fracPart = fracPart[:microsScale]
	}
	for len(fracPart) < microsScale {
		fracPart += "0"
	}
	digits := intPart + fracPart
	var v int64
	for i := 0; i < len(digits); i++ {
		c := digits[i]
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a number: %q", s)
		}
		// 逐位防溢出：v*10+d 必须 ≤ MaxInt64。写成 (Max-d)/10 而不是
		// v > Max/10——后者在 v == Max/10 且 d ≥ 8 时放行，乘出来就回绕成负数了。
		if v > (math.MaxInt64-int64(c-'0'))/10 {
			return 0, fmt.Errorf("amount out of range: %q", s)
		}
		v = v*10 + int64(c-'0')
	}
	if round {
		if v == math.MaxInt64 {
			return 0, fmt.Errorf("amount out of range: %q", s)
		}
		v++
	}
	if neg {
		v = -v
	}
	return v, nil
}

// insertBillOn 把一条账单写入 e（*sql.DB 或事务），dialect 只用于方言判定与
// 占位符转写。公共入口 InsertBill 走 d == e 的直连路径。
func insertBillOn(e billExec, dialect *sql.DB, b *Bill) error {
	billed := b.BilledAt
	if billed.IsZero() {
		billed = time.Now()
	}
	created := b.CreatedAt
	if created.IsZero() {
		created = time.Now()
	}
	id, err := db.InsertReturningIDOn(e, dialect, `INSERT INTO bills
		(upstream_id, upstream_name, kind, amount_micros, currency, billed_at, note, detail, created_at)
		VALUES (?,?,?,?,?,?,?,?,?) RETURNING id`,
		b.UpstreamID, b.UpstreamName, b.Kind, b.AmountMicros, b.Currency, billed, b.Note, b.Detail, created)
	if err != nil {
		return fmt.Errorf("insert bill: %w", err)
	}
	b.ID = id
	b.BilledAt = billed
	b.CreatedAt = created
	return nil
}

// InsertBill 写入一条账单，BilledAt/CreatedAt 未设置时按服务端本地 now 补齐
// （项目时间戳约定：Go time.Now()，不用 SQL 默认值）。成功后 b.ID 回填新行 id。
func InsertBill(d *sql.DB, b *Bill) error {
	if err := insertBillOn(d, d, b); err != nil {
		return err
	}
	return nil
}

// GetBill 按 id 取一条账单；未找到回 sql.ErrNoRows。
func GetBill(d *sql.DB, id int64) (*Bill, error) {
	b := &Bill{}
	err := d.QueryRow(db.Rebind(d, `SELECT id, upstream_id, upstream_name, kind, amount_micros, currency,
		billed_at, note, detail, created_at FROM bills WHERE id = ?`), id).
		Scan(&b.ID, &b.UpstreamID, &b.UpstreamName, &b.Kind, &b.AmountMicros, &b.Currency,
			&b.BilledAt, &b.Note, &b.Detail, &b.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("get bill %d: %w", id, err)
	}
	return b, nil
}

// UpdateBill 手动记账的编辑入口（handler 先校验 kind=manual 才允许走到这里）。
// 覆盖可编辑字段；kind/detail/created_at 不在可编辑范围。
func UpdateBill(d *sql.DB, b *Bill) error {
	_, err := d.Exec(db.Rebind(d, `UPDATE bills SET upstream_id = ?, upstream_name = ?, amount_micros = ?,
		currency = ?, billed_at = ?, note = ? WHERE id = ?`),
		b.UpstreamID, b.UpstreamName, b.AmountMicros, b.Currency, b.BilledAt, b.Note, b.ID)
	if err != nil {
		return fmt.Errorf("update bill %d: %w", b.ID, err)
	}
	return nil
}

// DeleteBill 删除一条账单，返回删除的行数（0 = 不存在，调用方据此回 404）。
// 自动记账算错时也走这里人工清掉：余额快照是追加式历史，删账单不会触发重算，
// 下一条快照仍以快照序列为基线。
func DeleteBill(d *sql.DB, id int64) (int64, error) {
	res, err := d.Exec(db.Rebind(d, `DELETE FROM bills WHERE id = ?`), id)
	if err != nil {
		return 0, fmt.Errorf("delete bill %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("delete bill %d: rows affected: %w", id, err)
	}
	return n, nil
}

// BillsList 分页列出账单（billed_at 倒序），支持可选的 upstream 与时间窗口过滤，
// 沿用 UsageRecordsList 的 clamp/COUNT/LIMIT-OFFSET 口径。from/to 零值表示不限。
func BillsList(d *sql.DB, upstreamID *int64, from, to time.Time, page, size int) ([]Bill, int, error) {
	page, size = normalizePage(page, size)
	where, args := billWhere(upstreamID, from, to)
	var total int
	if err := d.QueryRow(db.Rebind(d, "SELECT COUNT(*) FROM bills"+where), args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count bills: %w", err)
	}
	offset := (page - 1) * size
	q := `SELECT id, upstream_id, upstream_name, kind, amount_micros, currency, billed_at, note, detail, created_at
		FROM bills` + where + ` ORDER BY billed_at DESC, id DESC LIMIT ? OFFSET ?`
	listArgs := append(append([]any{}, args...), size, offset)
	rows, err := d.Query(db.Rebind(d, q), listArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("list bills: %w", err)
	}
	defer rows.Close()
	out := make([]Bill, 0)
	for rows.Next() {
		var b Bill
		if err := rows.Scan(&b.ID, &b.UpstreamID, &b.UpstreamName, &b.Kind, &b.AmountMicros, &b.Currency,
			&b.BilledAt, &b.Note, &b.Detail, &b.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, b)
	}
	return out, total, rows.Err()
}

// billWhere 拼出账单查询共用的 WHERE 子句（" WHERE" 开头或空串），args 与条件
// 一一对应。to 是排他上界（< end）。
func billWhere(upstreamID *int64, from, to time.Time) (string, []any) {
	var conds []string
	var args []any
	if upstreamID != nil {
		conds = append(conds, "upstream_id = ?")
		args = append(args, *upstreamID)
	}
	if !from.IsZero() {
		conds = append(conds, "billed_at >= ?")
		args = append(args, from)
	}
	if !to.IsZero() {
		conds = append(conds, "billed_at < ?")
		args = append(args, to)
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// ErrInvalidBucket 报告 bucket 参数不在 day/month/year 白名单内；handler 据此
// 回 400 而不是 500（调用方的问题，不是服务端故障）。
var ErrInvalidBucket = errors.New("invalid bucket")

// ErrBillWindowTooWide 拒绝会产生过多零填充桶的窗口。静默截断会让汇总
// 与明细口径不同，因此超宽输入必须显式报错。
var ErrBillWindowTooWide = errors.New("bill window too wide")

// BillCurrencyAmount 是一个时间桶内单一币种的金额。趋势不能把不同币种
// 加成一个数字，调用方按币种选择或分系列展示。
type BillCurrencyAmount struct {
	Currency     string `json:"currency"`
	AmountMicros int64  `json:"amount_micros"`
}

// BillBucketStat 是一个时间桶（日/月/年）的账单聚合。
type BillBucketStat struct {
	// Bucket 归一化桶标识："YYYY-MM-DD" / "YYYY-MM" / "YYYY"（本地时区）
	Bucket  string               `json:"bucket"`
	Amounts []BillCurrencyAmount `json:"amounts"`
	Count   int                  `json:"count"`
}

// 桶数上限：day 与用量页 90 天窗口同量级再放宽一档（预留全年趋势图），month/year
// 取个人网关合理的回看跨度。超宽窗口截断而不是把前端拖死。
const (
	maxBillDayBuckets   = 370
	maxBillMonthBuckets = 60
	maxBillYearBuckets  = 20
)

// billBucketSpec 返回桶的时间推进步长（只在桶起点上调用，AddDate 的月末溢出
// 不会发生）、桶 key 的 Go 布局（长度即 DayBucketExpr 产物的截取宽度）、桶数
// 上限与缺省回看桶数。
func billBucketSpec(bucket string) (step func(time.Time) time.Time, layout string, capBuckets, defaultSpan int, err error) {
	switch bucket {
	case "day":
		return func(t time.Time) time.Time { return t.AddDate(0, 0, 1) }, "2006-01-02", maxBillDayBuckets, 29, nil
	case "month":
		return func(t time.Time) time.Time { return t.AddDate(0, 1, 0) }, "2006-01", maxBillMonthBuckets, 11, nil
	case "year":
		return func(t time.Time) time.Time { return t.AddDate(1, 0, 0) }, "2006", maxBillYearBuckets, 4, nil
	}
	return nil, "", 0, 0, fmt.Errorf("%w: %q (want day, month or year)", ErrInvalidBucket, bucket)
}

// billBucketStart 把时刻截到所在桶的起点（本地时区）。
func billBucketStart(bucket string, t time.Time) time.Time {
	switch bucket {
	case "month":
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
	case "year":
		return time.Date(t.Year(), 1, 1, 0, 0, 0, 0, t.Location())
	default: // day
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	}
}

// BillWindow 返回账单窗口 [start, end)（本地时区）。from/to 是
// "YYYY-MM-DD"（可空；日期闭区间语义，内部换算成排他的次日零点）：
//   - 显式 from/to 始终按精确日期过滤，不因 month/year 图表档位扩展到整桶；
//   - 只给 from：终点 = 明天零点（「从 from 到今天」）；
//   - 只给 to：起点 = to 所在桶回退 defaultSpan 个桶（「截止 to 的近 N 桶」）；
//   - 都不给：now 所在桶回退 defaultSpan 到 now 所在桶（day 近 30 天 / month
//     近 12 个月 / year 近 5 年）。
//
// 桶数超上限时返回 ErrBillWindowTooWide，不静默截断；from 晚于 to 返回空窗口。
// 账单 summary 的图表与两类合计共用这一个窗口，和明细的显式日期范围一致。
func BillWindow(bucket, from, to string) (start, end time.Time, err error) {
	step, _, capBuckets, defaultSpan, err := billBucketSpec(bucket)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	// 锚点：to 给了锚在 to 的桶起点，否则锚在 now 的桶起点。回退跨度与终点都
	// 相对锚点，四种 from/to 组合下 start ≤ end 恒成立（显式 from 晚于 to 除外）。
	now := time.Now()
	anchor := billBucketStart(bucket, now)
	if to != "" {
		tt, err := parseTimeParam(to)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("bill window: invalid to: %w", err)
		}
		anchor = billBucketStart(bucket, tt)
	}
	start = anchor
	switch bucket {
	case "month":
		start = start.AddDate(0, -defaultSpan, 0)
	case "year":
		start = start.AddDate(-defaultSpan, 0, 0)
	default:
		start = start.AddDate(0, 0, -defaultSpan)
	}
	// 显式日期是精确闭区间，不能随月/年桶扩成整月/整年，否则汇总与明细
	// 在切换图表档位时统计不同窗口。没有显式端点时仍按桶提供缺省回看窗口。
	if from != "" {
		ft, err := parseTimeParam(from)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("bill window: invalid from: %w", err)
		}
		start = ft
	}
	end = step(anchor)
	if to != "" {
		tt, _ := parseTimeParam(to) // 上面已解析并校验
		end = tt.AddDate(0, 0, 1)
	} else if from != "" {
		end = billBucketStart("day", now).AddDate(0, 0, 1)
	}
	// 只给 to 时起点是 to 所在桶回退 defaultSpan，保持既有缺省跨度语义。
	// 超宽窗口显式失败，不再从末端静默截断：调用方可以看到错误并缩小范围，
	// 不会把被截断的汇总误当成整个筛选区间。
	n := 0
	for t := billBucketStart(bucket, start); t.Before(end); t = step(t) {
		n++
		if n > capBuckets {
			return time.Time{}, time.Time{}, fmt.Errorf("%w: %s allows at most %d buckets", ErrBillWindowTooWide, bucket, capBuckets)
		}
	}
	if !end.After(start) { // from 晚于 to 等退化输入：空窗口，调用方得空结果
		return start, start, nil
	}
	return start, end, nil
}

// BillTrend 聚合窗口 [start, end)（来自 BillWindow）内的账单金额，空桶零填充、
// 按时间升序，前端图表直接可用；空窗口返回空切片。
func BillTrend(d *sql.DB, bucket string, upstreamID *int64, start, end time.Time) ([]BillBucketStat, error) {
	step, layout, _, _, err := billBucketSpec(bucket)
	if err != nil {
		return nil, err
	}
	out := make([]BillBucketStat, 0, 32)
	idx := make(map[string]int, 32)
	// 起止时刻是精确查询窗口，但空桶要覆盖与窗口相交的完整桶序列。
	// 例如 month + 1/15~1/20 只显示一个 2026-01 桶，桶内只统计这六天。
	for t := billBucketStart(bucket, start); t.Before(end); t = step(t) {
		key := t.Format(layout)
		idx[key] = len(out)
		out = append(out, BillBucketStat{Bucket: key, Amounts: make([]BillCurrencyAmount, 0, 1)})
	}
	if len(out) == 0 {
		return out, nil
	}
	// DayBucketExpr 恒产 "YYYY-MM-DD" 文本，按桶宽截前缀即得 month/year 桶；
	// substr 三方言都支持（PG/MySQL 均为标准/同义写法）。
	dayExpr := db.DayBucketExpr(d, "billed_at")
	bucketExpr := "substr(" + dayExpr + ", 1, " + strconv.Itoa(len(layout)) + ")"
	where, args := billWhere(upstreamID, start, end)
	q := "SELECT " + bucketExpr + ", currency, COALESCE(SUM(amount_micros), 0), COUNT(*) FROM bills" + where +
		" GROUP BY " + bucketExpr + ", currency ORDER BY " + bucketExpr + ", currency"
	rows, err := d.Query(db.Rebind(d, q), args...)
	if err != nil {
		return nil, fmt.Errorf("bill trend: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key, currency string
		var micros int64
		var count int
		if err := rows.Scan(&key, &currency, &micros, &count); err != nil {
			return nil, fmt.Errorf("bill trend: %w", err)
		}
		if i, ok := idx[key]; ok {
			out[i].Amounts = append(out[i].Amounts, BillCurrencyAmount{Currency: currency, AmountMicros: micros})
			out[i].Count += count
		}
	}
	return out, rows.Err()
}

// BillUpstreamTotal 是时间窗口内一个上游的账单合计（按上游 + 币种分组：同名
// 上游改名前后是两行历史名称；混币种拆行显示，不假装能加总——前端按币种各自
// 出合计）。
type BillUpstreamTotal struct {
	UpstreamID   int64  `json:"upstream_id"`
	UpstreamName string `json:"upstream_name"`
	Currency     string `json:"currency"`
	AmountMicros int64  `json:"amount_micros"`
	Count        int    `json:"count"`
}

// BillUpstreamTotals 返回窗口内按上游汇总的账单合计，金额降序。
func BillUpstreamTotals(d *sql.DB, upstreamID *int64, from, to time.Time) ([]BillUpstreamTotal, error) {
	where, args := billWhere(upstreamID, from, to)
	q := `SELECT upstream_id, upstream_name, currency, COALESCE(SUM(amount_micros), 0), COUNT(*)
		FROM bills` + where + ` GROUP BY upstream_id, upstream_name, currency
		ORDER BY SUM(amount_micros) DESC, upstream_name`
	rows, err := d.Query(db.Rebind(d, q), args...)
	if err != nil {
		return nil, fmt.Errorf("bill upstream totals: %w", err)
	}
	defer rows.Close()
	out := make([]BillUpstreamTotal, 0)
	for rows.Next() {
		var t BillUpstreamTotal
		if err := rows.Scan(&t.UpstreamID, &t.UpstreamName, &t.Currency, &t.AmountMicros, &t.Count); err != nil {
			return nil, fmt.Errorf("bill upstream totals: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// BillKindTotal 是窗口内一个记账来源（auto/manual）×币种的笔数与金额合计，
// 供账单页的统计卡。按币种拆分：跨币种加总没有意义（CNY+USD ≠ 任何真实金额）。
type BillKindTotal struct {
	Kind         string `json:"kind"`
	Currency     string `json:"currency"`
	Count        int    `json:"count"`
	AmountMicros int64  `json:"amount_micros"`
}

// BillKindTotals 返回窗口内按 kind × 币种的合计。
func BillKindTotals(d *sql.DB, upstreamID *int64, from, to time.Time) ([]BillKindTotal, error) {
	where, args := billWhere(upstreamID, from, to)
	q := `SELECT kind, currency, COUNT(*), COALESCE(SUM(amount_micros), 0) FROM bills` + where +
		` GROUP BY kind, currency`
	rows, err := d.Query(db.Rebind(d, q), args...)
	if err != nil {
		return nil, fmt.Errorf("bill kind totals: %w", err)
	}
	defer rows.Close()
	out := make([]BillKindTotal, 0, 2)
	for rows.Next() {
		var t BillKindTotal
		if err := rows.Scan(&t.Kind, &t.Currency, &t.Count, &t.AmountMicros); err != nil {
			return nil, fmt.Errorf("bill kind totals: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// billExec 抽象 *sql.DB 与 *sql.Tx 共有的执行面：快照归档与记账推导在事务内外
// 共用同一套代码。
type billExec interface {
	QueryRow(query string, args ...any) *sql.Row
	Exec(query string, args ...any) (sql.Result, error)
}

// insertBalanceSnapshotOn 是 InsertBalanceSnapshot 的执行面抽象版（事务内复用）。
func insertBalanceSnapshotOn(e billExec, dialect *sql.DB, s *BalanceSnapshot) error {
	ts := s.CreatedAt
	if ts.IsZero() {
		ts = time.Now()
	}
	id, err := db.InsertReturningIDOn(e, dialect, `INSERT INTO balance_snapshots
		(upstream_id, upstream_name, vendor, payload, created_at)
		VALUES (?,?,?,?,?) RETURNING id`,
		s.UpstreamID, s.UpstreamName, s.Vendor, string(s.Payload), ts)
	if err != nil {
		return fmt.Errorf("insert balance snapshot: %w", err)
	}
	s.ID = id
	s.CreatedAt = ts
	return nil
}

// billBalancePayload 是余额类快照 payload 的最小解析结构。生产方是
// upstream.FetchBalance 的归一化（kind=balance|quota）；这里只关心 kind=balance
// 的余额条目，金额保持厂商返回的原串。两边字段名/kind 字面量的契约由
// internal/upstream 的契约测试钉住（TestBalancePayloadContractWithStoreBilling）。
type billBalancePayload struct {
	Kind     string `json:"kind"`
	Balances []struct {
		Currency string `json:"currency"`
		Total    string `json:"total"`
	} `json:"balances"`
}

// RecordBalanceSnapshot 归档一条余额快照并在**同一事务**内推导自动账单：要么
// 快照与账单一起落库，要么都不落。若只提交快照，新快照会成为下一条的对比
// 基线，这两次快照之间的消费就永久丢了——所以记账失败时连快照一起回滚，下次
// 抓取自动重试（对比基线仍是旧快照）。返回入账条数。
//
// 必须经由写入方串行上下文调用（db.Writer 的单 goroutine）：「读上一条快照 →
// 插账单」与并发的另一次快照落库交错才不会重复记账。该保证只在单进程内成立——
// 多实例共享一个库、或管理端刷新与 poller 并发抓取同一上游时，插入顺序可能与
// 抓取顺序不一致，极端情况下会按错误基线记账；detail JSON 里留了前后余额与
// 快照 id，发现错账直接删行。
func RecordBalanceSnapshot(d *sql.DB, snap *BalanceSnapshot) (int, error) {
	tx, err := d.Begin()
	if err != nil {
		return 0, fmt.Errorf("record balance snapshot: begin: %w", err)
	}
	defer tx.Rollback()
	if err := insertBalanceSnapshotOn(tx, d, snap); err != nil {
		return 0, err
	}
	n, err := recordBillFromBalanceDelta(tx, d, snap)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("record balance snapshot: commit: %w", err)
	}
	return n, nil
}

// recordBillFromBalanceDelta 在新余额快照落库后（同一事务/同一串行上下文）调用：
// 与该上游上一条快照逐币种对比，余额下降的部分记一条 auto 账单（归属时刻 =
// 新快照时刻）。返回入账条数。
//
// 规则与边界：
//   - 只认 kind=balance 的 payload；quota 快照（kimi 百分比窗口）没有金额语义，
//     静默跳过；
//   - 上一条快照缺失（首次拉取）时跳过——没有基线就无法推导消费；
//   - 上一条快照的 vendor 与本次不同时跳过：换厂商=换计费账户/口径（如
//     deepseek 换指向 stepfun，两边都是 balance 类但根本不是一个钱包），差额
//     是幻觉。同厂商换 API key（换账户）vendor 挡不住，detail 留痕人工核对；
//   - 余额上升（充值、赠送入账）不记负账单：账单语义是「支出」。厂商赠送额度
//     到期清零会被误记为支出，detail 里留了前后余额与快照 id 供人工核对，
//     算错的行可直接删除；
//   - 单币种金额串解析失败只跳过该币种并 Warn（厂商数字格式变更时账本会无声
//     停摆，必须有信号），不拖垮其他条目。
func recordBillFromBalanceDelta(e billExec, dialect *sql.DB, snap *BalanceSnapshot) (int, error) {
	if snap.ID == 0 || snap.CreatedAt.IsZero() {
		return 0, nil
	}
	var cur billBalancePayload
	if err := json.Unmarshal(snap.Payload, &cur); err != nil || cur.Kind != "balance" {
		return 0, nil
	}
	var prevPayload, prevVendor string
	var prevID int64
	err := e.QueryRow(db.Rebind(dialect, `SELECT id, vendor, payload FROM balance_snapshots
		WHERE upstream_id = ? AND id < ? ORDER BY id DESC LIMIT 1`), snap.UpstreamID, snap.ID).
		Scan(&prevID, &prevVendor, &prevPayload)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("bill from balance delta: read previous snapshot: %w", err)
	}
	var prev billBalancePayload
	if err := json.Unmarshal([]byte(prevPayload), &prev); err != nil || prev.Kind != "balance" {
		// 上一条不是 balance 类（quota 通道或 payload 损坏）：没有同口径基线
		logger.Debug("bill from balance delta: previous snapshot has no balance payload, skip",
			"upstream_id", snap.UpstreamID, "prev_snapshot_id", prevID)
		return 0, nil
	}
	if prevVendor != snap.Vendor {
		// 厂商变了：换指向后的第一次抓取不与旧厂商钱包对比
		logger.Info("bill from balance delta: vendor changed between snapshots, skip",
			"upstream_id", snap.UpstreamID, "prev_vendor", prevVendor, "vendor", snap.Vendor)
		return 0, nil
	}
	prevTotal := make(map[string]string, len(prev.Balances))
	for _, entry := range prev.Balances {
		prevTotal[entry.Currency] = entry.Total
	}
	recorded := 0
	for _, entry := range cur.Balances {
		before, ok := prevTotal[entry.Currency]
		if !ok {
			continue // 新出现的币种没有基线，跳过
		}
		beforeMicros, err1 := microsFromDecimal(before)
		afterMicros, err2 := microsFromDecimal(entry.Total)
		if err1 != nil || err2 != nil {
			// 厂商数字格式变了（如千分位/科学计数法）：跳过并留下信号，
			// 否则自动记账会无声停摆
			logger.Warn("bill from balance delta: unparsable balance amount, skip currency",
				"upstream_id", snap.UpstreamID, "currency", entry.Currency,
				"prev_total", before, "new_total", entry.Total, "err", errors.Join(err1, err2))
			continue
		}
		spend := beforeMicros - afterMicros
		if spend <= 0 {
			continue
		}
		detail, _ := json.Marshal(map[string]any{
			"prev_total": before, "new_total": entry.Total, "vendor": snap.Vendor,
			"prev_snapshot_id": prevID, "new_snapshot_id": snap.ID,
		})
		b := &Bill{
			UpstreamID:   snap.UpstreamID,
			UpstreamName: snap.UpstreamName,
			Kind:         BillKindAuto,
			AmountMicros: spend,
			Currency:     entry.Currency,
			BilledAt:     snap.CreatedAt,
			Detail:       string(detail),
		}
		if err := insertBillOn(e, dialect, b); err != nil {
			return recorded, err
		}
		recorded++
	}
	return recorded, nil
}
