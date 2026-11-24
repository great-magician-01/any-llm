package store

import (
	"database/sql"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/great-magician-01/any-llm/internal/db"
)

// UsageOverviewTotals 是概览统计卡使用的一组计数。三个时间窗口共用同一形状。
type UsageOverviewTotals struct {
	RequestCount int `json:"request_count"`
	TotalTokens  int `json:"total_tokens"`
	OkCount      int `json:"ok_count"`
	ErrorCount   int `json:"error_count"`
}

// UsageOverviewModel 是本月单个模型的用量；概览只展示这些字段，不再为首页
// 计算用量统计页才需要的输入/输出 token 拆分和平均速度。
type UsageOverviewModel struct {
	GroupKey     string `json:"group_key"`
	RequestCount int    `json:"request_count"`
	TotalTokens  int    `json:"total_tokens"`
	OkCount      int    `json:"ok_count"`
	ErrorCount   int    `json:"error_count"`
}

// UsageUpstreamTotal 是一个上游的今日/本月 token 用量。
type UsageUpstreamTotal struct {
	UpstreamID    int64 `json:"upstream_id"`
	DailyTokens   int   `json:"daily_tokens"`
	MonthlyTokens int   `json:"monthly_tokens"`
}

// UsageOverview 一次提供首页所需的全部用量数据。Upstreams/keys/balances 是
// 配置或快照数据，仍由各自接口提供；这里只合并 usage_records 上的聚合。
type UsageOverview struct {
	Today          UsageOverviewTotals  `json:"today"`
	Month          UsageOverviewTotals  `json:"month"`
	AllTime        UsageOverviewTotals  `json:"all_time"`
	Models         []UsageOverviewModel `json:"models"`
	Daily          []UsageDayStat       `json:"daily"`
	UpstreamTotals []UsageUpstreamTotal `json:"upstream_totals"`
}

const usageOverviewTopModels = 8

// UsageOverviewStats 用三条相互独立的查询替代首页原先的三份模型汇总、一份
// 每日汇总以及每个上游两条 SumTokens。三条查询并行，保留原先浏览器并发请求
// 的延迟特征，同时把数据库扫描次数和 HTTP 扇出降下来。
func UsageOverviewStats(d *sql.DB) (*UsageOverview, error) {
	now := time.Now()
	dayStart, dayEnd, monthStart, monthEnd := TokenWindows(now)
	out := &UsageOverview{
		Models:         make([]UsageOverviewModel, 0, usageOverviewTopModels),
		UpstreamTotals: make([]UsageUpstreamTotal, 0),
	}

	var summaryErr, dailyErr, upstreamErr error
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		summaryErr = fillUsageOverviewSummary(d, out, dayStart, monthStart)
	}()
	go func() {
		defer wg.Done()
		out.Daily, dailyErr = usageDailyStatsAt(d, maxUsageDailyDays, "", "", now)
	}()
	go func() {
		defer wg.Done()
		out.UpstreamTotals, upstreamErr = usageOverviewUpstreamTotals(d, dayStart, dayEnd, monthStart, monthEnd)
	}()
	wg.Wait()

	// 固定检查顺序，多个查询同时失败时错误仍然可复现。
	if summaryErr != nil {
		return nil, summaryErr
	}
	if dailyErr != nil {
		return nil, dailyErr
	}
	if upstreamErr != nil {
		return nil, upstreamErr
	}
	return out, nil
}

// fillUsageOverviewSummary 一次全表分组扫描同时得到累计、本月、今日汇总和
// 本月模型 Top。累计卡片需要所有模型，因此不能在 SQL 里只 LIMIT Top N；先在
// Go 中累加全部行，再裁出展示用的前 8 个模型。
func fillUsageOverviewSummary(d *sql.DB, out *UsageOverview, dayStart, monthStart time.Time) error {
	// 首页旧接口的今日/本月查询只有 from、没有 to；这里保留相同口径。
	// 每个窗口重复四次参数，分别计算请求、token、成功、失败。
	q := `SELECT model,
		COUNT(*), COALESCE(SUM(total_tokens), 0),
		COALESCE(SUM(CASE WHEN status='ok' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status='error' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN created_at >= ? THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN created_at >= ? THEN total_tokens ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN created_at >= ? AND status='ok' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN created_at >= ? AND status='error' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN created_at >= ? THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN created_at >= ? THEN total_tokens ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN created_at >= ? AND status='ok' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN created_at >= ? AND status='error' THEN 1 ELSE 0 END), 0)
		FROM usage_records GROUP BY model`
	args := []any{
		monthStart, monthStart, monthStart, monthStart,
		dayStart, dayStart, dayStart, dayStart,
	}
	rows, err := d.Query(db.Rebind(d, q), args...)
	if err != nil {
		return fmt.Errorf("usage overview summary: %w", err)
	}
	defer rows.Close()

	models := make([]UsageOverviewModel, 0)
	for rows.Next() {
		var modelName string
		var all, month, today UsageOverviewTotals
		if err := rows.Scan(
			&modelName,
			&all.RequestCount, &all.TotalTokens, &all.OkCount, &all.ErrorCount,
			&month.RequestCount, &month.TotalTokens, &month.OkCount, &month.ErrorCount,
			&today.RequestCount, &today.TotalTokens, &today.OkCount, &today.ErrorCount,
		); err != nil {
			return fmt.Errorf("usage overview summary: %w", err)
		}
		addUsageOverviewTotals(&out.AllTime, all)
		addUsageOverviewTotals(&out.Month, month)
		addUsageOverviewTotals(&out.Today, today)
		if month.RequestCount > 0 {
			models = append(models, UsageOverviewModel{
				GroupKey:     modelName,
				RequestCount: month.RequestCount,
				TotalTokens:  month.TotalTokens,
				OkCount:      month.OkCount,
				ErrorCount:   month.ErrorCount,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("usage overview summary: %w", err)
	}

	// 与原前端一致：token 降序；并列时沿用原 SQL 的模型名升序。
	sort.Slice(models, func(i, j int) bool {
		if models[i].TotalTokens != models[j].TotalTokens {
			return models[i].TotalTokens > models[j].TotalTokens
		}
		return models[i].GroupKey < models[j].GroupKey
	})
	if len(models) > usageOverviewTopModels {
		models = models[:usageOverviewTopModels]
	}
	out.Models = models
	return nil
}

func addUsageOverviewTotals(dst *UsageOverviewTotals, src UsageOverviewTotals) {
	dst.RequestCount += src.RequestCount
	dst.TotalTokens += src.TotalTokens
	dst.OkCount += src.OkCount
	dst.ErrorCount += src.ErrorCount
}

// usageOverviewUpstreamTotals 一次 GROUP BY 取代每个上游的今日/本月两次查询。
// WHERE 把扫描限制在本月，月总量直接 SUM，日总量用条件聚合取出。
func usageOverviewUpstreamTotals(d *sql.DB, dayStart, dayEnd, monthStart, monthEnd time.Time) ([]UsageUpstreamTotal, error) {
	rows, err := d.Query(db.Rebind(d, `SELECT upstream_id,
		COALESCE(SUM(CASE WHEN created_at >= ? AND created_at < ? THEN total_tokens ELSE 0 END), 0),
		COALESCE(SUM(total_tokens), 0)
		FROM usage_records
		WHERE upstream_id IS NOT NULL AND created_at >= ? AND created_at < ?
		GROUP BY upstream_id ORDER BY upstream_id`), dayStart, dayEnd, monthStart, monthEnd)
	if err != nil {
		return nil, fmt.Errorf("usage overview upstream totals: %w", err)
	}
	defer rows.Close()

	out := make([]UsageUpstreamTotal, 0)
	for rows.Next() {
		var total UsageUpstreamTotal
		if err := rows.Scan(&total.UpstreamID, &total.DailyTokens, &total.MonthlyTokens); err != nil {
			return nil, fmt.Errorf("usage overview upstream totals: %w", err)
		}
		out = append(out, total)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("usage overview upstream totals: %w", err)
	}
	return out, nil
}
