package store

import (
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/great-magician-01/any-llm/internal/db"
)

// 跨月分表的读路径在 PG 与 MySQL 上各跑一遍（conversation_pg_test.go 里的 fixture
// 建在 PG-only 的旧 conversation_records 上，所以那几条只在 PG 跑）。
//
// 这里只用**月分表**构造多分表场景，覆盖两种方言都必须成立的规则：
//   - 分表发现（PG 走 pg_tables，MySQL 走 information_schema）与排序：新月份在前；
//   - 全局分页：跨分表的窗口拼接必须与「一次取全」完全一致，不漏不重；
//   - 分块内排序：created_at DESC，同秒用 id DESC 决胜；
//   - 按 id 跨分表查详情（id 全局唯一：PG 共享序列，MySQL 走 id_sequences 计数器）；
//   - 写入自动建当月分表并注册进读路径。

type cseDialect struct {
	name string
	env  string
}

var cseDialects = []cseDialect{
	{name: "postgres", env: "DB_TEST_PG_DSN"},
	{name: "mysql", env: "DB_TEST_MYSQL_DSN"},
}

// cseTestDB 为指定方言建隔离的 schema/库并跑迁移 + 重载分表注册缓存。
func cseTestDB(t *testing.T, dl cseDialect) *sql.DB {
	t.Helper()
	switch dl.name {
	case "postgres":
		return csePostgresDB(t)
	case "mysql":
		return cseMySQLDB(t)
	}
	t.Fatalf("unknown dialect %q", dl.name)
	return nil
}

func csePostgresDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("DB_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set DB_TEST_PG_DSN to run postgres e2e tests")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	schema := fmt.Sprintf("any_llm_shard_test_%d", time.Now().UnixNano())
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
	if err := LoadConvShards(d); err != nil {
		d.Exec("DROP SCHEMA " + schema + " CASCADE")
		d.Close()
		t.Fatalf("load shards: %v", err)
	}
	t.Cleanup(func() {
		d.Exec("DROP SCHEMA " + schema + " CASCADE")
		d.Close()
	})
	return d
}

func cseMySQLDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("DB_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set DB_TEST_MYSQL_DSN to run mysql e2e tests")
	}
	cfg, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	name := fmt.Sprintf("any_llm_shard_test_%d", time.Now().UnixNano())
	ctrl, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatalf("open control conn: %v", err)
	}
	if err := ctrl.Ping(); err != nil {
		ctrl.Close()
		t.Fatalf("ping control conn: %v", err)
	}
	if _, err := ctrl.Exec(fmt.Sprintf(
		"CREATE DATABASE %s DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_bin", name)); err != nil {
		ctrl.Close()
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() {
		if _, err := ctrl.Exec("DROP DATABASE " + name); err != nil {
			t.Errorf("drop database %s: %v", name, err)
		}
		ctrl.Close()
	})

	cfg.DBName = name
	d, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatalf("open mysql: %v", err)
	}
	if err := d.Ping(); err != nil {
		d.Close()
		t.Fatalf("ping: %v", err)
	}
	if err := db.MigrateForTest(d); err != nil {
		d.Close()
		t.Fatalf("migrate: %v", err)
	}
	if err := LoadConvShards(d); err != nil {
		d.Close()
		t.Fatalf("load shards: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// cseInsert 插入一条归档（按 created_at 路由到对应月分表，缺表时自动建表）。
func cseInsert(t *testing.T, d *sql.DB, model string, created time.Time) int64 {
	t.Helper()
	rec := &ConversationRecord{
		UpstreamName: "u", Model: model, InFormat: "openai", UpFormat: "openai",
		Harness: "claude-code", UserAgent: "cse/1.0", Status: "ok",
		// IR 列是 jsonb/JSON：空串不是合法 JSON，PG 与 MySQL 都会直接拒绝
		RequestIR: `{}`, ResponseIR: `{}`,
		RequestRaw: []byte("{}"), ResponseRaw: []byte("{}"),
		CreatedAt: created,
	}
	if err := InsertConversation(d, rec); err != nil {
		t.Fatalf("insert %s: %v", model, err)
	}
	// InsertConversation 不回传 id：按 model 反查（每个 model 在 fixture 里唯一）。
	var id int64
	if err := d.QueryRow(db.Rebind(d, "SELECT id FROM "+ConvShardName(created)+" WHERE model = ?"), model).Scan(&id); err != nil {
		t.Fatalf("lookup id for %s: %v", model, err)
	}
	return id
}

// TestConvShardsE2E_CrossShardPaging 覆盖两种方言上的跨月分页、分块内排序与
// 跨分表按 id 查详情。
func TestConvShardsE2E_CrossShardPaging(t *testing.T) {
	for _, dl := range cseDialects {
		dl := dl
		t.Run(dl.name, func(t *testing.T) {
			d := cseTestDB(t, dl)

			// 三个不同月份的分表：本月、上月、上上月；本月两条（含同秒，验证 id 决胜）。
			now := time.Now()
			firstOfMonth := time.Date(now.Year(), now.Month(), 1, 12, 0, 0, 0, time.Local)
			prevMonth := firstOfMonth.AddDate(0, -1, 0)
			olderMonth := firstOfMonth.AddDate(0, -2, 0)
			sameSecond := firstOfMonth.Add(30 * time.Second)

			idOldest := cseInsert(t, d, "oldest", olderMonth)
			idPrev := cseInsert(t, d, "prev", prevMonth)
			idNewA := cseInsert(t, d, "new-a", firstOfMonth)
			idNewB := cseInsert(t, d, "new-b", sameSecond) // 与 new-a 同秒，靠 id 决胜

			// 分表发现：三个月的分表都注册进来了，且新→旧。
			shards, err := convShardSnapshot(d)
			if err != nil {
				t.Fatalf("shard snapshot: %v", err)
			}
			for _, want := range []string{ConvShardName(firstOfMonth), ConvShardName(prevMonth), ConvShardName(olderMonth)} {
				found := false
				for _, s := range shards {
					if s == want {
						found = true
					}
				}
				if !found {
					t.Fatalf("shard %s missing from snapshot %v", want, shards)
				}
			}
			if shards[0] != ConvShardName(firstOfMonth) {
				t.Errorf("newest shard must come first: %v", shards)
			}

			// 一次取全：跨三个分表 4 条，顺序 = 新月份优先，块内 created_at DESC、同秒 id DESC。
			all, total, err := ConversationRecordsList(d, 1, 50)
			if err != nil {
				t.Fatalf("list all: %v", err)
			}
			if total != 4 || len(all) != 4 {
				t.Fatalf("total=%d len=%d want 4/4 (shards=%v)", total, len(all), shards)
			}
			wantOrder := []string{"new-b", "new-a", "prev", "oldest"}
			for i, want := range wantOrder {
				if all[i].Model != want {
					t.Fatalf("order[%d]=%q want %q (full=%v)", i, all[i].Model, want, wantOrder)
				}
			}
			// id 全局唯一且单调：MySQL 走 id_sequences 计数器，PG 走共享序列。
			seen := map[int64]bool{}
			for _, r := range all {
				if seen[r.ID] {
					t.Fatalf("duplicate id %d across shards", r.ID)
				}
				seen[r.ID] = true
			}

			// 分页窗口拼接：size=1 逐页取，必须与「一次取全」逐条相同（不漏不重）。
			var paged []string
			for page := 1; page <= 4; page++ {
				recs, pageTotal, err := ConversationRecordsList(d, page, 1)
				if err != nil {
					t.Fatalf("page %d: %v", page, err)
				}
				if pageTotal != 4 {
					t.Fatalf("page %d total=%d want 4", page, pageTotal)
				}
				if len(recs) != 1 {
					t.Fatalf("page %d len=%d want 1", page, len(recs))
				}
				paged = append(paged, recs[0].Model)
			}
			for i, want := range wantOrder {
				if paged[i] != want {
					t.Fatalf("paged[%d]=%q want %q (paged=%v)", i, paged[i], want, paged)
				}
			}
			// 越界页：空数组但 total 不变。
			if recs, pageTotal, err := ConversationRecordsList(d, 99, 1); err != nil || len(recs) != 0 || pageTotal != 4 {
				t.Fatalf("out-of-range page: len=%d total=%d err=%v", len(recs), pageTotal, err)
			}

			// 详情：每个分表的 id 都能跨表查到自己的那一行。
			for _, tc := range []struct {
				id    int64
				model string
			}{
				{idOldest, "oldest"}, {idPrev, "prev"}, {idNewA, "new-a"}, {idNewB, "new-b"},
			} {
				got, err := GetConversation(d, tc.id)
				if err != nil {
					t.Fatalf("get id=%d (%s): %v", tc.id, tc.model, err)
				}
				if got.Model != tc.model || got.ID != tc.id {
					t.Fatalf("get id=%d: model=%q id=%d want %q/%d", tc.id, got.Model, got.ID, tc.model, tc.id)
				}
			}
		})
	}
}
