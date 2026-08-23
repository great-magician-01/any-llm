package model

import (
	"database/sql"
	"errors"
	"testing"
)

// createConvTable 建 PG conversation_records 的 SQLite 等价表（仅查询用到的列），
// 让列表/详情查询可以单测；真实建表只在 migrationPG（SQLite 不建此表）。
func createConvTable(t *testing.T, d *sql.DB) {
	t.Helper()
	_, err := d.Exec(`CREATE TABLE conversation_records (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		ext_key_id INTEGER,
		upstream_id INTEGER,
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
		request_ir TEXT NOT NULL DEFAULT '{}',
		response_ir TEXT NOT NULL DEFAULT '{}',
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		t.Fatal(err)
	}
}

func insertConv(t *testing.T, d *sql.DB, mdl, status string, stream, total int) int64 {
	t.Helper()
	res, err := d.Exec(`INSERT INTO conversation_records
		(upstream_name, model, in_format, up_format, harness, user_agent, stream, status,
		 total_tokens, request_ir, response_ir)
		VALUES ('u', ?, 'openai', 'anthropic', 'claude-code', 'test-agent', ?, ?, ?,
			'{"Messages":[{"Role":"user","Content":[{"Type":"text","Text":"hi"}]}]}', '{"Content":[{"Type":"text","Text":"hello"}]}')`,
		mdl, stream, status, total)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func TestConversationRecordsList(t *testing.T) {
	d := testDB(t)
	createConvTable(t, d)
	for i := 0; i < 5; i++ {
		insertConv(t, d, "m", "ok", 1, i+1)
	}

	records, total, err := ConversationRecordsList(d, 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 || len(records) != 3 {
		t.Fatalf("total=%d len=%d, want 5/3", total, len(records))
	}
	// ORDER BY id DESC：第一条是最后插入的
	if records[0].TotalTokens != 5 {
		t.Fatalf("first total_tokens=%d, want 5", records[0].TotalTokens)
	}
	if !records[0].Stream || records[0].Harness != "claude-code" || records[0].Status != "ok" {
		t.Fatalf("first=%+v", records[0])
	}
	// 列表不带 IR payload
	if records[0].RequestIR != "" || records[0].ResponseIR != "" {
		t.Fatalf("list should not return IR, got %q", records[0].RequestIR)
	}

	records, total, err = ConversationRecordsList(d, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 || len(records) != 2 {
		t.Fatalf("page2 total=%d len=%d, want 5/2", total, len(records))
	}
}

func TestGetConversation(t *testing.T) {
	d := testDB(t)
	createConvTable(t, d)
	id := insertConv(t, d, "gpt-4o", "error", 0, 42)

	rec, err := GetConversation(d, id)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Model != "gpt-4o" || rec.TotalTokens != 42 || rec.Status != "error" {
		t.Fatalf("rec=%+v", rec)
	}
	wantReq := `{"Messages":[{"Role":"user","Content":[{"Type":"text","Text":"hi"}]}]}`
	if rec.RequestIR != wantReq {
		t.Fatalf("request_ir=%q", rec.RequestIR)
	}
	if rec.ResponseIR != `{"Content":[{"Type":"text","Text":"hello"}]}` {
		t.Fatalf("response_ir=%q", rec.ResponseIR)
	}
	// raw 字节不查询，JSON 输出为 null
	if rec.RequestRaw != nil || rec.ResponseRaw != nil {
		t.Fatalf("raw should be nil, got %d/%d bytes", len(rec.RequestRaw), len(rec.ResponseRaw))
	}

	if _, err := GetConversation(d, 9999); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("err=%v, want sql.ErrNoRows", err)
	}
}
