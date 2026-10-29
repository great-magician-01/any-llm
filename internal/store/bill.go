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
)

// 账单：上游消费的货币流水。两个来源——
//   - kind=auto：厂商余额快照联动。每次新快照与上一条的余额差额 > 0 时自动入账
//     （见 RecordBillFromBalanceDelta）；quota 类快照（kimi 的百分比窗口）没有
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
	// Detail 仅 auto 记账填充：推导依据的 JSON 文本（prev/new 余额、关联快照 id）
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
		if v > math.MaxInt64/10 {
			return 0, fmt.Errorf("amount out of range: %q", s)
		}
		v = v*10 + int64(c-'0')
	}
	if round {
		v++
	}
	if neg {
		v = -v
	}
	return v, nil
}

// InsertBill 写入一条账单，BilledAt/CreatedAt 未设置时按服务端本地 now 补齐
// （项目时间戳约定：Go time.Now()，不用 SQL 默认值）。成功后 b.ID 回填新行 id。
func InsertBill(d *sql.DB, b *Bill) error {
	billed := b.BilledAt
	if billed.IsZero() {
		billed = time.Now()
	}
	created := b.CreatedAt
	if created.IsZero() {
		created = time.Now()
	}
	id, err := db.InsertReturningID(d, `INSERT INTO bills
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

// DeleteBill 删除一条账单。自动记账算错时也走这里人工清掉：余额快照是追加式
// 历史，删账单不会触发重算，下一条快照仍以快照序列为基线。
func DeleteBill(d *sql.DB, id int64) error {
	_, err := d.Exec(db.Rebind(d, `DELETE FROM bills WHERE id = ?`), id)
	if err != nil {
		return fmt.Errorf("delete bill %d: %w", id, err)
	}
	return nil
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

// BillBucketStat 是一个时间桶（日/月/年）的账单聚合。
type BillBucketStat struct {
	// Bucket 归一化桶标识："YYYY-MM-DD" / "YYYY-MM" / "YYYY"（本地时区）
	Bucket       string `json:"bucket"`
	AmountMicros int64  `json:"amount_micros"`
	Count        int    `json:"count"`
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

// BillTrend 按 bucket（day|month|year）聚合账单金额。from/to 是 "YYYY-MM-DD"
// 日期（闭区间语义，服务端换算成排他的下一桶起点）；缺省时 day 回看 30 天、
// month 12 个月、year 5 年。空桶零填充、按时间升序，前端图表直接可用。
// 窗口超上限时从起点截断。
func BillTrend(d *sql.DB, bucket string, upstreamID *int64, from, to string) ([]BillBucketStat, error) {
	step, layout, capBuckets, defaultSpan, err := billBucketSpec(bucket)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	// 缺省窗口：从 now 所在桶回退 defaultSpan 个桶。step 只对桶起点定义（无月末
	// 溢出），回退用反向 AddDate——桶起点上同样安全。终点 = now 所在桶的下一桶
	// （当前桶计入窗口）。
	start := billBucketStart(bucket, now)
	switch bucket {
	case "month":
		start = start.AddDate(0, -defaultSpan, 0)
	case "year":
		start = start.AddDate(-defaultSpan, 0, 0)
	default:
		start = start.AddDate(0, 0, -defaultSpan)
	}
	end := step(billBucketStart(bucket, now))
	if from != "" || to != "" {
		if from != "" {
			ft, err := parseTimeParam(from)
			if err != nil {
				return nil, fmt.Errorf("bill trend: invalid from: %w", err)
			}
			start = billBucketStart(bucket, ft)
		}
		anchor := now
		if to != "" {
			tt, err := parseTimeParam(to)
			if err != nil {
				return nil, fmt.Errorf("bill trend: invalid to: %w", err)
			}
			anchor = tt
		}
		end = step(billBucketStart(bucket, anchor))
	}
	// 桶数截断（含 from/to 都给的宽窗口）。
	n := 0
	for t := start; t.Before(end); t = step(t) {
		n++
		if n > capBuckets {
			break
		}
	}
	if n > capBuckets {
		n = capBuckets
		end = start
		for i := 0; i < capBuckets; i++ {
			end = step(end)
		}
	}
	if n < 1 { // from 晚于 to 等退化输入：至少返回一个桶，不 500
		n = 1
		end = step(start)
	}
	// 零填充所有桶。
	out := make([]BillBucketStat, 0, n)
	byKey := make(map[string]*BillBucketStat, n)
	for t := start; t.Before(end); t = step(t) {
		key := t.Format(layout)
		out = append(out, BillBucketStat{Bucket: key})
		byKey[key] = &out[len(out)-1]
	}
	// DayBucketExpr 恒产 "YYYY-MM-DD" 文本，按桶宽截前缀即得 month/year 桶；
	// substr 三方言都支持（PG/MySQL 均为标准/同义写法）。
	dayExpr := db.DayBucketExpr(d, "billed_at")
	bucketExpr := "substr(" + dayExpr + ", 1, " + strconv.Itoa(len(layout)) + ")"
	where, args := billWhere(upstreamID, start, end)
	q := "SELECT " + bucketExpr + ", COALESCE(SUM(amount_micros), 0), COUNT(*) FROM bills" + where +
		" GROUP BY " + bucketExpr
	rows, err := d.Query(db.Rebind(d, q), args...)
	if err != nil {
		return nil, fmt.Errorf("bill trend: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var micros int64
		var count int
		if err := rows.Scan(&key, &micros, &count); err != nil {
			return nil, fmt.Errorf("bill trend: %w", err)
		}
		if stat, ok := byKey[key]; ok {
			stat.AmountMicros = micros
			stat.Count = count
		}
	}
	return out, rows.Err()
}

// BillUpstreamTotal 是时间窗口内一个上游的账单合计（按上游 + 币种分组：同名
// 上游改名前后是两行历史名称，混币种的假设上游拆开显示，不假装能加总）。
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

// BillKindTotal 是窗口内一个记账来源（auto/manual）的笔数与金额合计，
// 供账单页的统计卡。
type BillKindTotal struct {
	Kind         string `json:"kind"`
	Count        int    `json:"count"`
	AmountMicros int64  `json:"amount_micros"`
}

// BillKindTotals 返回窗口内按 kind 的合计。
func BillKindTotals(d *sql.DB, upstreamID *int64, from, to time.Time) ([]BillKindTotal, error) {
	where, args := billWhere(upstreamID, from, to)
	q := `SELECT kind, COUNT(*), COALESCE(SUM(amount_micros), 0) FROM bills` + where + ` GROUP BY kind`
	rows, err := d.Query(db.Rebind(d, q), args...)
	if err != nil {
		return nil, fmt.Errorf("bill kind totals: %w", err)
	}
	defer rows.Close()
	out := make([]BillKindTotal, 0, 2)
	for rows.Next() {
		var t BillKindTotal
		if err := rows.Scan(&t.Kind, &t.Count, &t.AmountMicros); err != nil {
			return nil, fmt.Errorf("bill kind totals: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// billBalancePayload 是余额类快照 payload 的最小解析结构。生产方是
// upstream.FetchBalance 的归一化（kind=balance|quota）；这里只关心 kind=balance
// 的余额条目，金额保持厂商返回的原串。
type billBalancePayload struct {
	Kind     string `json:"kind"`
	Balances []struct {
		Currency string `json:"currency"`
		Total    string `json:"total"`
	} `json:"balances"`
}

// RecordBillFromBalanceDelta 在新余额快照落库后调用：与该上游上一条快照逐币种
// 对比，余额下降的部分记一条 auto 账单（归属时刻 = 新快照时刻）。返回入账条数。
//
// 规则与边界：
//   - 只认 kind=balance 的 payload；quota 快照（kimi 百分比窗口）没有金额语义，
//     静默跳过；
//   - 上一条快照缺失（首次拉取）或其 payload 不是 balance 类（厂商中途换过
//     查询通道）时跳过——没有同口径基线就无法推导消费；
//   - 余额上升（充值、赠送入账）不记负账单：账单语义是「支出」。厂商赠送额度
//     到期清零会被误记为支出，detail 里留了前后余额与快照 id 供人工核对，
//     算错的行可直接删除；
//   - 必须在写入方串行上下文里调用（db.Writer 的单 goroutine）：「读上一条 →
//     插账单」才不会与另一次快照落库交错重复记账。
func RecordBillFromBalanceDelta(d *sql.DB, snap *BalanceSnapshot) (int, error) {
	if snap.ID == 0 || snap.CreatedAt.IsZero() {
		return 0, nil
	}
	var cur billBalancePayload
	if err := json.Unmarshal(snap.Payload, &cur); err != nil || cur.Kind != "balance" {
		return 0, nil
	}
	var prevPayload string
	var prevID int64
	err := d.QueryRow(db.Rebind(d, `SELECT id, payload FROM balance_snapshots
		WHERE upstream_id = ? AND id < ? ORDER BY id DESC LIMIT 1`), snap.UpstreamID, snap.ID).Scan(&prevID, &prevPayload)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("bill from balance delta: read previous snapshot: %w", err)
	}
	var prev billBalancePayload
	if err := json.Unmarshal([]byte(prevPayload), &prev); err != nil || prev.Kind != "balance" {
		return 0, nil
	}
	prevTotal := make(map[string]string, len(prev.Balances))
	for _, e := range prev.Balances {
		prevTotal[e.Currency] = e.Total
	}
	recorded := 0
	for _, e := range cur.Balances {
		before, ok := prevTotal[e.Currency]
		if !ok {
			continue // 新出现的币种没有基线，跳过
		}
		beforeMicros, err1 := microsFromDecimal(before)
		afterMicros, err2 := microsFromDecimal(e.Total)
		if err1 != nil || err2 != nil {
			continue // 单币种金额解析失败只跳过该币种，不拖垮其他条目
		}
		spend := beforeMicros - afterMicros
		if spend <= 0 {
			continue
		}
		detail, _ := json.Marshal(map[string]any{
			"prev_total": before, "new_total": e.Total,
			"prev_snapshot_id": prevID, "new_snapshot_id": snap.ID,
		})
		b := &Bill{
			UpstreamID:   snap.UpstreamID,
			UpstreamName: snap.UpstreamName,
			Kind:         BillKindAuto,
			AmountMicros: spend,
			Currency:     e.Currency,
			BilledAt:     snap.CreatedAt,
			Detail:       string(detail),
		}
		if err := InsertBill(d, b); err != nil {
			return recorded, err
		}
		recorded++
	}
	return recorded, nil
}
