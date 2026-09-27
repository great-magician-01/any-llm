package store

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/great-magician-01/any-llm/internal/db"
)

// 本文件在真实 PostgreSQL 上跑跨分片的读取 SQL（未设 DB_TEST_PG_DSN 时 skip，
// 与 internal/db/pg_e2e_test.go 同一门控）。已有 convPageWindows 的纯函数测试
// 覆盖了窗口数学，这里覆盖**真实 SQL**：分块拼接、块内排序、跨表详情、共享 id。
//
// 业务规则：
//   - 列表按「月分表新→旧，历史分表 conversation_records 永远最后」分块拼接，
//     块内 created_at DESC、同秒用 id DESC 决胜（翻页稳定）；
//   - 详情按 id 逐分表 UNION ALL 查（id 由共享序列分配，全局唯一）；
//   - 写入按 created_at 月份路由，缺表自动建并注册进读路径。

// pgcTestDB 在独立 schema 里建好 PG 库并重载分表注册缓存。
func pgcTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("DB_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set DB_TEST_PG_DSN to run postgres e2e tests")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	schema := fmt.Sprintf("any_llm_store_test_%d", time.Now().UnixNano())
	// search_path 走连接参数：pgx 池里 SET 只影响单条连接。
	cfg.RuntimeParams["search_path"] = schema
	d := stdlib.OpenDB(*cfg)
	if err := d.Ping(); err != nil {
		d.Close()
		t.Fatalf("ping: %v", err)
	}
	if _, err := d.Exec("CREATE SCHEMA " + schema); err != nil {
		d.Close()
		t.Fatalf("create schema: %v", err)
	}
	if err := db.MigrateForTest(d); err != nil {
		d.Exec("DROP SCHEMA " + schema + " CASCADE")
		d.Close()
		t.Fatalf("migrate: %v", err)
	}
	// 分表注册缓存与配置读缓存都是进程级全局：本用例用自己的 schema，必须重载。
	resetConvShardCache(t)
	ResetConfigCache()
	t.Cleanup(ResetConfigCache)
	if err := LoadConvShards(d); err != nil {
		t.Fatalf("load conversation shards: %v", err)
	}
	t.Cleanup(func() {
		d.Exec("DROP SCHEMA " + schema + " CASCADE")
		d.Close()
	})
	return d
}

// pgcCreateLegacyShard 建存量库遗留的 conversation_records 表。BIGSERIAL 会创建
// conversation_records_id_seq —— 与分表共享的那条序列同名，正是存量库升级时
// CREATE SEQUENCE IF NOT EXISTS 变成 no-op 的原因，id 因此在两张表间继续递增。
func pgcCreateLegacyShard(t *testing.T, d *sql.DB) {
	t.Helper()
	if _, err := d.Exec(`CREATE TABLE conversation_records (
		id BIGSERIAL PRIMARY KEY,
		ext_key_id BIGINT,
		upstream_id BIGINT,
		upstream_name TEXT NOT NULL,
		model TEXT NOT NULL,
		in_format TEXT NOT NULL,
		up_format TEXT NOT NULL,
		harness TEXT NOT NULL,
		user_agent TEXT NOT NULL,
		stream INTEGER NOT NULL DEFAULT 0,
		status TEXT NOT NULL DEFAULT 'ok',
		prompt_tokens INTEGER NOT NULL DEFAULT 0,
		completion_tokens INTEGER NOT NULL DEFAULT 0,
		total_tokens INTEGER NOT NULL DEFAULT 0,
		cache_read_tokens INTEGER NOT NULL DEFAULT 0,
		cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
		reasoning_tokens INTEGER NOT NULL DEFAULT 0,
		request_ir JSONB NOT NULL DEFAULT '{}'::jsonb,
		response_ir JSONB NOT NULL DEFAULT '{}'::jsonb,
		request_raw BYTEA NOT NULL DEFAULT '',
		response_raw BYTEA NOT NULL DEFAULT '',
		created_at TIMESTAMP(0) NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatalf("create legacy shard: %v", err)
	}
	// 历史分表出现后必须重载注册缓存（生产路径在启动时做这件事）
	if err := LoadConvShards(d); err != nil {
		t.Fatalf("reload shards: %v", err)
	}
}

// pgcInsertLegacy 往历史分表插一行并返回 id。
func pgcInsertLegacy(t *testing.T, d *sql.DB, model string, created time.Time) int64 {
	t.Helper()
	var id int64
	err := d.QueryRow(`INSERT INTO conversation_records
		(upstream_name, model, in_format, up_format, harness, user_agent, status, total_tokens,
		 request_ir, response_ir, created_at)
		VALUES ('u', $1, 'openai', 'anthropic', 'claude-code', 'legacy-agent', 'ok', 3,
		 '{"Model":"legacy"}', '{"Text":"legacy"}', $2) RETURNING id`, model, created).Scan(&id)
	if err != nil {
		t.Fatalf("insert legacy row %s: %v", model, err)
	}
	return id
}

// pgcInsert 走生产写入路径（InsertConversation）插一行并读回它落在哪张表、id 是多少。
func pgcInsert(t *testing.T, d *sql.DB, model string, created time.Time) (int64, string) {
	t.Helper()
	rec := &ConversationRecord{
		UpstreamName: "u", Model: model, InFormat: "openai", UpFormat: "anthropic",
		Harness: "claude-code", UserAgent: "test-agent", Status: "ok",
		PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7,
		RequestIR: `{"Model":"` + model + `"}`, ResponseIR: `{"Text":"ok-` + model + `"}`,
		RequestRaw: []byte("req-" + model), ResponseRaw: []byte("resp-" + model),
		CreatedAt: created,
	}
	if err := InsertConversation(d, rec); err != nil {
		t.Fatalf("insert conversation %s: %v", model, err)
	}
	table := ConvShardName(created)
	var id int64
	if err := d.QueryRow(`SELECT id FROM `+table+` WHERE model=$1`, model).Scan(&id); err != nil {
		t.Fatalf("read back id of %s from %s: %v", model, table, err)
	}
	return id, table
}

// pgcFixture 造出「历史分表 + 三张月分表」的完整场景。
type pgcFixture struct {
	legacy []int64
	nov    []int64
	jan    []int64
	feb    []int64
	// wantOrder 期望的全局顺序（分块：feb → jan → nov → legacy，块内新→旧）
	wantOrder []string
	// idByModel 用于详情查询断言
	idByModel map[string]int64
}

func pgcBuildFixture(t *testing.T, d *sql.DB) pgcFixture {
	t.Helper()
	pgcCreateLegacyShard(t, d)

	at := func(y int, m time.Month, day, hour int) time.Time {
		return time.Date(y, m, day, hour, 30, 0, 0, time.Local)
	}

	f := pgcFixture{idByModel: map[string]int64{}}
	// 历史分表：两条旧数据 + 一条 created_at 比所有月分表都新的数据。
	// 后者的存在是为了证明「legacy 最后」是**位置规则**而不是按时间排序的结果。
	f.legacy = append(f.legacy, pgcInsertLegacy(t, d, "legacy-old", at(2025, 6, 15, 10)))
	f.legacy = append(f.legacy, pgcInsertLegacy(t, d, "legacy-new", at(2025, 6, 20, 10)))
	f.legacy = append(f.legacy, pgcInsertLegacy(t, d, "legacy-recent", at(2026, 3, 5, 10)))

	// 2025-11（最旧月份）
	f.nov = append(f.nov, pgcInsertMust(t, d, f.idByModel, "nov-old", at(2025, 11, 2, 10)))
	f.nov = append(f.nov, pgcInsertMust(t, d, f.idByModel, "nov-new", at(2025, 11, 20, 10)))
	// 2026-01（中间月份）：含一对同秒记录，用来验证 id DESC 决胜
	f.jan = append(f.jan, pgcInsertMust(t, d, f.idByModel, "jan-old", at(2026, 1, 10, 10)))
	f.jan = append(f.jan, pgcInsertMust(t, d, f.idByModel, "jan-mid", at(2026, 1, 20, 10)))
	f.jan = append(f.jan, pgcInsertMust(t, d, f.idByModel, "jan-mid-tie", at(2026, 1, 20, 10)))
	// 2026-02（最新月份）
	f.feb = append(f.feb, pgcInsertMust(t, d, f.idByModel, "feb-old", at(2026, 2, 5, 10)))
	f.feb = append(f.feb, pgcInsertMust(t, d, f.idByModel, "feb-new", at(2026, 2, 20, 10)))

	f.wantOrder = []string{
		"feb-new", "feb-old",
		"jan-mid-tie", "jan-mid", "jan-old",
		"nov-new", "nov-old",
		"legacy-recent", "legacy-new", "legacy-old",
	}
	return f
}

// pgcInsertMust 插入并记录 id。
func pgcInsertMust(t *testing.T, d *sql.DB, byModel map[string]int64, model string, created time.Time) int64 {
	t.Helper()
	id, _ := pgcInsert(t, d, model, created)
	byModel[model] = id
	return id
}

// 列表：分块顺序（新月份优先、legacy 最后）、块内 created_at DESC + id DESC 决胜、
// 跨分表翻页拼接、深翻页与总数。
func TestConvShardsPGListOrderAndPagination(t *testing.T) {
	d := pgcTestDB(t)
	f := pgcBuildFixture(t, d)

	// 分表注册缓存必须认得四张表（月分表新→旧 + 历史分表最后）
	shards, err := convShardSnapshot(d)
	if err != nil {
		t.Fatal(err)
	}
	wantShards := []string{
		"conversation_records_2026_02",
		"conversation_records_2026_01",
		"conversation_records_2025_11",
		"conversation_records",
	}
	if strings.Join(shards, ",") != strings.Join(wantShards, ",") {
		t.Fatalf("分表清单=%v，期望 %v（月分表新→旧，历史分表最后）", shards, wantShards)
	}

	// 一次取全：顺序必须是分块顺序
	records, total, err := ConversationRecordsList(d, 1, 200)
	if err != nil {
		t.Fatal(err)
	}
	if total != 10 || len(records) != 10 {
		t.Fatalf("total=%d len=%d，期望 10/10", total, len(records))
	}
	gotOrder := make([]string, len(records))
	for i, r := range records {
		gotOrder[i] = r.Model
	}
	if strings.Join(gotOrder, ",") != strings.Join(f.wantOrder, ",") {
		t.Fatalf("全局顺序=\n %v\n期望=\n %v", gotOrder, f.wantOrder)
	}
	// 列表不带 IR payload（大响应）
	if records[0].RequestIR != "" || records[0].ResponseIR != "" {
		t.Fatalf("列表不应返回 IR: %+v", records[0])
	}
	// 元数据列完整
	if records[0].Stream {
		t.Fatalf("stream 列应为 false: %+v", records[0])
	}
	if records[0].Harness != "claude-code" || records[0].UserAgent != "test-agent" || records[0].Status != "ok" {
		t.Fatalf("元数据列丢失: %+v", records[0])
	}
	if records[0].InFormat != "openai" || records[0].UpFormat != "anthropic" ||
		records[0].PromptTokens != 3 || records[0].CompletionTokens != 4 || records[0].TotalTokens != 7 {
		t.Fatalf("元数据列不匹配: %+v", records[0])
	}

	// 分页扫全表（size=4 必然横跨分表边界）：拼接结果必须与一次取全完全一致，
	// 且每页 total 相同（total 是全局计数，不随页变化）。
	var paged []string
	for page := 1; ; page++ {
		rows, tot, err := ConversationRecordsList(d, page, 4)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if tot != 10 {
			t.Fatalf("page %d total=%d，期望 10", page, tot)
		}
		if len(rows) == 0 {
			if page <= 3 {
				t.Fatalf("page %d 为空，但 10 行按 size=4 应该有 3 页", page)
			}
			break
		}
		for _, r := range rows {
			paged = append(paged, r.Model)
		}
		if page > 10 {
			t.Fatal("翻页没有终止")
		}
	}
	if strings.Join(paged, ",") != strings.Join(f.wantOrder, ",") {
		t.Fatalf("跨分表翻页拼接=\n %v\n期望=\n %v", paged, f.wantOrder)
	}

	// 深翻页超出总量：空结果 + 正确 total（管理端据此显示「没有更多」）
	empty, total, err := ConversationRecordsList(d, 99, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 || total != 10 {
		t.Fatalf("深翻页 len=%d total=%d，期望 0/10", len(empty), total)
	}
}

// 详情：按 id 跨分表查（每张分表一条 WHERE id=? 分支的 UNION ALL），
// 每张表的记录都要能查到自己的行与 IR。
func TestConvShardsPGDetailLookupByIDAcrossShards(t *testing.T) {
	d := pgcTestDB(t)
	f := pgcBuildFixture(t, d)

	for _, tc := range []struct {
		name     string
		id       int64
		model    string
		wantReq  string
		wantResp string
	}{
		{"月分表 2026_02", f.idByModel["feb-new"], "feb-new", "feb-new", "ok-feb-new"},
		{"月分表 2026_01（同秒记录）", f.idByModel["jan-mid-tie"], "jan-mid-tie", "jan-mid-tie", "ok-jan-mid-tie"},
		{"月分表 2025_11", f.idByModel["nov-old"], "nov-old", "nov-old", "ok-nov-old"},
		// 历史分表的原始值是 {"Model":"legacy"} / {"Text":"legacy"}
		{"历史分表", f.legacy[1], "legacy-new", "legacy", "legacy"},
		{"历史分表（created_at 最新）", f.legacy[2], "legacy-recent", "legacy", "legacy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec, err := GetConversation(d, tc.id)
			if err != nil {
				t.Fatalf("get id=%d: %v", tc.id, err)
			}
			if rec.ID != tc.id || rec.Model != tc.model {
				t.Fatalf("id=%d 查到 model=%q id=%d，期望 %q（UNION ALL 分支串到别的分表了）",
					tc.id, rec.Model, rec.ID, tc.model)
			}
			// IR 两列随详情返回（列表不返回）
			if !strings.Contains(rec.RequestIR, tc.wantReq) {
				t.Fatalf("request_ir=%q 未包含 %q", rec.RequestIR, tc.wantReq)
			}
			if !strings.Contains(rec.ResponseIR, tc.wantResp) {
				t.Fatalf("response_ir=%q 未包含 %q", rec.ResponseIR, tc.wantResp)
			}
			// raw 字节列不出查询（JSON 为 null），避免 base64 大响应
			if rec.RequestRaw != nil || rec.ResponseRaw != nil {
				t.Fatalf("raw 列不应被查询: %d/%d bytes", len(rec.RequestRaw), len(rec.ResponseRaw))
			}
		})
	}

	// 共享 id 源：历史分表（BIGSERIAL 建的同名序列）与月分表（nextval）必须继续
	// 同一条序列，id 全局唯一且严格递增。
	seen := map[int64]string{}
	all := []int64{}
	all = append(all, f.legacy...)
	all = append(all, f.nov...)
	all = append(all, f.jan...)
	all = append(all, f.feb...)
	for _, id := range all {
		if prev, dup := seen[id]; dup {
			t.Fatalf("id %d 在分表间重复（%s）", id, prev)
		}
		seen[id] = "x"
	}
	for i := 1; i < len(all); i++ {
		if all[i] <= all[i-1] {
			t.Fatalf("id 未按写入顺序递增: %v（分表必须共享一个 id 源）", all)
		}
	}
	// 不存在的 id：sql.ErrNoRows（调用方回 404）
	if _, err := GetConversation(d, 999999999); err == nil {
		t.Fatal("不存在的 id 必须报错")
	} else if !strings.Contains(err.Error(), "no rows") {
		t.Fatalf("不存在的 id 错误=%v，期望 sql.ErrNoRows", err)
	}
}

// 写入自动建表：往一个还没有分表的月份插记录时，InsertConversation 建表并注册，
// 读路径立刻能看到它，且它按「最新月份」排在最前（legacy 仍然最后）。
func TestConvShardsPGInsertAutoCreatesMonthShard(t *testing.T) {
	d := pgcTestDB(t)
	pgcBuildFixture(t, d)

	// 2026-03 尚无分表
	if _, ok := convShardForMonth("2026-03"); ok {
		t.Fatal("2026-03 分表不该预先存在")
	}
	newID, table := pgcInsert(t, d, "mar-new", time.Date(2026, 3, 3, 9, 0, 0, 0, time.Local))
	if table != "conversation_records_2026_03" {
		t.Fatalf("写入路由到 %s，期望 conversation_records_2026_03", table)
	}
	if _, ok := convShardForMonth("2026-03"); !ok {
		t.Fatal("写入后 2026-03 分表未注册进读路径缓存")
	}

	records, total, err := ConversationRecordsList(d, 1, 200)
	if err != nil {
		t.Fatal(err)
	}
	if total != 11 {
		t.Fatalf("total=%d，期望 11", total)
	}
	if records[0].Model != "mar-new" {
		t.Fatalf("首行=%q，期望最新月份的新记录 mar-new", records[0].Model)
	}
	if records[len(records)-1].Model != "legacy-old" {
		t.Fatalf("末行=%q，期望历史分表的最后一行", records[len(records)-1].Model)
	}
	// 新分表的记录按 id 也能查到自己
	rec, err := GetConversation(d, newID)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Model != "mar-new" || rec.ID != newID {
		t.Fatalf("详情=%+v", rec)
	}
}
