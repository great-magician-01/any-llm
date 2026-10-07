package adminapi

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/great-magician-01/any-llm/internal/db"
	"github.com/great-magician-01/any-llm/internal/store"
)

// adminPGDB 连接 DB_TEST_PG_DSN，在独立 schema 里跑迁移 + 加载分表注册缓存，
// 用例结束 drop schema。未配置 DSN 时跳过（CI 有 postgres:17 service）。
//
// 为什么必须走真库：SQLite 上归档整体关闭，所以 listConversations /
// getConversation 的**真实**分支（跨月分表翻页、只查元数据列、raw 字节不外泄）
// 在 SQLite 用例里一条都没执行到。
func adminPGDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("DB_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set DB_TEST_PG_DSN to run postgres e2e tests")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	schema := fmt.Sprintf("any_llm_admin_test_%d", time.Now().UnixNano())
	cfg.RuntimeParams["search_path"] = schema
	d := stdlib.OpenDB(*cfg)
	if err := d.Ping(); err != nil {
		d.Close()
		t.Fatalf("ping: %v", err)
	}
	if _, err := d.Exec(fmt.Sprintf("CREATE SCHEMA %s", schema)); err != nil {
		d.Close()
		t.Fatalf("create schema: %v", err)
	}
	if err := db.MigrateForTest(d); err != nil {
		d.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
		d.Close()
		t.Fatalf("migrate: %v", err)
	}
	// 分表注册缓存是进程级全局的：换 schema 必须重载，否则会串到别的用例。
	if err := store.LoadConvShards(d); err != nil {
		d.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
		d.Close()
		t.Fatalf("load conversation shards: %v", err)
	}
	t.Cleanup(func() {
		d.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema))
		d.Close()
	})
	return d
}

type pgConvListResp struct {
	Data []struct {
		ID           int64           `json:"id"`
		Model        string          `json:"model"`
		Status       string          `json:"status"`
		Stream       bool            `json:"stream"`
		PromptTokens int             `json:"prompt_tokens"`
		RequestIR    string          `json:"request_ir"`
		ResponseIR   string          `json:"response_ir"`
		RequestRaw   json.RawMessage `json:"request_raw"`
		ResponseRaw  json.RawMessage `json:"response_raw"`
	} `json:"data"`
	Total int `json:"total"`
}

// seedPGConversations 写入三条归档：两条在本月、一条在两个月前（即另一个分表）。
// raw 字节列带可识别的哨兵值，用来验证它们不会经 HTTP 泄漏出去。
func seedPGConversations(t *testing.T, d *sql.DB) {
	t.Helper()
	now := time.Now()
	seed := []struct {
		when  time.Time
		model string
		ir    string
	}{
		{now, "gpt-4o", `{"content":[{"type":"text","text":"newest"}]}`},
		{now.Add(-time.Minute), "gpt-4o-mini", `{"content":[{"type":"text","text":"middle"}]}`},
		{now.AddDate(0, -2, 0), "claude-3-5", `{"content":[{"type":"text","text":"oldest"}]}`},
	}
	for _, s := range seed {
		if err := store.InsertConversation(d, &store.ConversationRecord{
			Model: s.model, InFormat: "openai", UpFormat: "openai",
			Status: "ok", Stream: true,
			PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15,
			RequestIR:  `{"model":"` + s.model + `"}`,
			ResponseIR: s.ir,
			RequestRaw: []byte("RAW-REQUEST-SENTINEL"), ResponseRaw: []byte("RAW-RESPONSE-SENTINEL"),
			CreatedAt: s.when,
		}); err != nil {
			t.Fatalf("insert %s: %v", s.model, err)
		}
	}
}

// TestConversationsOnPostgres_CrossShardPagingAndRawColumns 覆盖 PG 上真实的
// 归档读路径：
//  1. 列表按 created_at 新→旧跨月分表翻页（第 2 页必须落到更老的那个分表）；
//  2. total 是全部分表的合计，不是单表行数；
//  3. 列表不带 IR（只查元数据列）；
//  4. raw 请求/响应字节列**任何响应都不出现**（详情也不查它们）。
func TestConversationsOnPostgres_CrossShardPagingAndRawColumns(t *testing.T) {
	d := adminPGDB(t)
	a := NewAPI(d, nil, nil)
	seedPGConversations(t, d)

	// 第 1 页：size=2，取到本月的两条，新的在前。
	req := httptest.NewRequest("GET", "/api/admin/conversations?page=1&size=2", nil)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "RAW-") {
		t.Fatalf("raw byte columns leaked through the list endpoint: %s", w.Body.String())
	}
	var page1 pgConvListResp
	if err := json.Unmarshal(w.Body.Bytes(), &page1); err != nil {
		t.Fatal(err)
	}
	if page1.Total != 3 {
		t.Errorf("total=%d want 3 (合计必须跨所有分表)", page1.Total)
	}
	if len(page1.Data) != 2 {
		t.Fatalf("page1 len=%d want 2", len(page1.Data))
	}
	if page1.Data[0].Model != "gpt-4o" || page1.Data[1].Model != "gpt-4o-mini" {
		t.Errorf("page1 order=%q,%q want newest first", page1.Data[0].Model, page1.Data[1].Model)
	}
	for i, row := range page1.Data {
		if row.RequestIR != "" || row.ResponseIR != "" {
			t.Errorf("page1[%d] must not ship IR columns (list only selects metadata): ir=%q/%q",
				i, row.RequestIR, row.ResponseIR)
		}
		if string(row.RequestRaw) != "null" || string(row.ResponseRaw) != "null" {
			t.Errorf("page1[%d] raw columns=%s/%s want null", i, row.RequestRaw, row.ResponseRaw)
		}
	}

	// 第 2 页：跨越到两个月前的那个分表。
	req = httptest.NewRequest("GET", "/api/admin/conversations?page=2&size=2", nil)
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var page2 pgConvListResp
	if err := json.Unmarshal(w.Body.Bytes(), &page2); err != nil {
		t.Fatal(err)
	}
	if page2.Total != 3 {
		t.Errorf("page2 total=%d want 3", page2.Total)
	}
	if len(page2.Data) != 1 {
		t.Fatalf("page2 len=%d want 1 (跨分表的第二页)", len(page2.Data))
	}
	if page2.Data[0].Model != "claude-3-5" {
		t.Errorf("page2[0]=%q want claude-3-5 (更老的分表)", page2.Data[0].Model)
	}

	// 详情：跨分表按 id 查（这个 id 只在那个更老的分表里）。
	oldID := page2.Data[0].ID
	req = httptest.NewRequest("GET", fmt.Sprintf("/api/admin/conversations/%d", oldID), nil)
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("detail status=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "RAW-") {
		t.Fatalf("raw byte columns leaked through the detail endpoint: %s", w.Body.String())
	}
	var detail struct {
		Data struct {
			ID         int64           `json:"id"`
			Model      string          `json:"model"`
			RequestIR  string          `json:"request_ir"`
			ResponseIR string          `json:"response_ir"`
			RequestRaw json.RawMessage `json:"request_raw"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Data.ID != oldID || detail.Data.Model != "claude-3-5" {
		t.Errorf("detail=%+v want id=%d model=claude-3-5", detail.Data, oldID)
	}
	if !strings.Contains(detail.Data.ResponseIR, "oldest") {
		t.Errorf("detail must include response_ir, got %q", detail.Data.ResponseIR)
	}
	if detail.Data.RequestIR == "" {
		t.Error("detail must include request_ir")
	}
	if string(detail.Data.RequestRaw) != "null" {
		t.Errorf("detail raw=%s want null (raw bytes are never queried)", detail.Data.RequestRaw)
	}
}

// TestConversationsOnPostgres_NotFoundAndPagingDefaults 覆盖详情 404 与
// page/size 规范化：size 越界（>200 或 <1）回落到 50，page<1 当作 1。
func TestConversationsOnPostgres_NotFoundAndPagingDefaults(t *testing.T) {
	d := adminPGDB(t)
	a := NewAPI(d, nil, nil)
	seedPGConversations(t, d)

	req := httptest.NewRequest("GET", "/api/admin/conversations/999999", nil)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatalf("unknown id status=%d want 404, body=%s", w.Code, w.Body.String())
	}

	// size=0 → 50；page=0 → 1：三条记录全都返回。
	req = httptest.NewRequest("GET", "/api/admin/conversations?page=0&size=0", nil)
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp pgConvListResp
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 3 || resp.Total != 3 {
		t.Fatalf("len=%d total=%d want 3/3 with default paging", len(resp.Data), resp.Total)
	}
}
