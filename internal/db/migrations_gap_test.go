package db

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// 本文件钉住「规范列缺失」的 fail-loud 语义（schema.go 的 Column.LateAdd 与
// migrations.go 的 migrateSoftDeleteSQLite 共同承担）：
//
//   - 补列只登记 LateAdd 列（初始 schema 之后加的列）。同名表缺**初始**列，
//     说明这个库不是本项目建的：不能静默补一个语义不明的空壳列，也不能在
//     重建老表时按列名搬运而把缺的列悄悄丢掉。
//   - SQLite 的老表重建（migrateSoftDeleteSQLite）在动手前核对规范列清单，
//     缺列时响亮失败并**点名缺失列**，数据与表结构原样保留。
//   - 不触发重建的老库：迁移可以成功，但缺的初始列仍然缺席，第一次查询
//     就报错（查询层 fail loud），而不是返回零值。

// migGapLegacyMissingCol 描述一个「老表缺初始列」的构造：表的 DDL（必须带上
// 该表的重建触发条件）与缺失的初始列名。
type migGapLegacyMissingCol struct {
	name       string
	ddl        string
	missingCol string
	// insert 造一行数据，用来验证失败时数据没有被搬丢。
	insert string
	// probeCol 用来在失败后读出那行数据（必须是存在的列）。
	probeCol  string
	probeWant string
}

func TestMigrateSoftDeleteSQLiteRefusesLegacyTableMissingCanonicalColumn(t *testing.T) {
	cases := []migGapLegacyMissingCol{
		{
			name: "ext_keys 缺 last_used_at",
			ddl: `CREATE TABLE ext_keys (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				key TEXT NOT NULL UNIQUE,
				label TEXT NOT NULL DEFAULT '',
				created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
			)`,
			missingCol: "last_used_at",
			insert:     `INSERT INTO ext_keys (key, label) VALUES ('all-sk-legacy','legacy')`,
			probeCol:   "label",
			probeWant:  "legacy",
		},
		{
			name: "upstreams 缺 base_url",
			ddl: `CREATE TABLE upstreams (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				name TEXT NOT NULL,
				api_key TEXT NOT NULL,
				format TEXT NOT NULL CHECK(format IN ('openai','anthropic')),
				created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
				updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
			)`,
			missingCol: "base_url",
			insert:     `INSERT INTO upstreams (name, api_key, format) VALUES ('u1','k1','openai')`,
			probeCol:   "api_key",
			probeWant:  "k1",
		},
		{
			name: "upstream_models 缺 manual",
			ddl: `CREATE TABLE upstream_models (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				upstream_id INTEGER NOT NULL,
				model_name TEXT NOT NULL,
				UNIQUE(upstream_id, model_name)
			)`,
			missingCol: "manual",
			insert:     `INSERT INTO upstream_models (upstream_id, model_name) VALUES (1,'m1')`,
			probeCol:   "model_name",
			probeWant:  "m1",
		},
		{
			name: "usage_records 缺 model",
			ddl: `CREATE TABLE usage_records (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				ext_key_id INTEGER REFERENCES ext_keys(id),
				upstream_id INTEGER,
				upstream_name TEXT NOT NULL,
				in_format TEXT NOT NULL,
				up_format TEXT NOT NULL,
				created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
			)`,
			missingCol: "model",
			insert:     `INSERT INTO usage_records (ext_key_id, upstream_name, in_format, up_format) VALUES (1,'u1','anthropic','openai')`,
			probeCol:   "upstream_name",
			probeWant:  "u1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "legacy.db")
			raw, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := raw.Exec(tc.ddl); err != nil {
				t.Fatalf("建老表: %v", err)
			}
			if _, err := raw.Exec(tc.insert); err != nil {
				t.Fatalf("插入老数据: %v", err)
			}
			if err := raw.Close(); err != nil {
				t.Fatal(err)
			}

			// 迁移必须失败：老表缺初始列，重建会按列名搬运，静默接受就等于丢列。
			got, err := OpenSQLite(path)
			if err == nil {
				got.Close()
				t.Fatal("缺初始列的老表必须让迁移响亮失败，而不是静默重建/丢列")
			}
			msg := err.Error()
			if !strings.Contains(msg, tc.missingCol) {
				t.Fatalf("错误信息必须点名缺失列 %q，实际=%v", tc.missingCol, err)
			}
			table := strings.Fields(tc.name)[0]
			if !strings.Contains(msg, table) {
				t.Fatalf("错误信息必须点名表 %q，实际=%v", table, err)
			}

			// 失败发生在重建之前：老表与数据原样保留（不半迁移、不丢行）。
			raw2, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer raw2.Close()
			var probe string
			if err := raw2.QueryRow(`SELECT ` + tc.probeCol + ` FROM ` + table).Scan(&probe); err != nil {
				t.Fatalf("失败后老数据不可读（被搬丢了？）: %v", err)
			}
			if probe != tc.probeWant {
				t.Fatalf("失败后老数据=%q，期望 %q", probe, tc.probeWant)
			}
			// 缺失的初始列没有被悄悄补成空壳
			var n int
			if err := raw2.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, table, tc.missingCol).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Fatalf("初始列 %s.%s 被自动补上了；它没有 LateAdd 标记，不该被补", table, tc.missingCol)
			}
		})
	}
}

// 不触发重建的老库（表里没有 UNIQUE/REFERENCES/CHECK）：迁移成功，但只补
// LateAdd 列；缺的初始列仍缺席，第一次查询点名报错——这是刻意的 fail-loud，
// 而不是返回零值让人以为数据是空的。
func TestMigrateExtraColsBackfillsOnlyLateAddColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-no-rebuild.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// 无 UNIQUE → 不触发重建；缺初始列 last_used_at。
	if _, err := raw.Exec(`CREATE TABLE ext_keys (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		key TEXT NOT NULL,
		label TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO ext_keys (key, label) VALUES ('all-sk-old','legacy')`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	d, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("无重建触发条件的老库应当迁移成功: %v", err)
	}
	defer d.Close()

	// LateAdd 列被补齐（这些列缺失是老库升级的正常路径）
	for _, col := range []string{"remark", "enabled", "daily_token_limit", "monthly_token_limit", "allowed_models", "is_active"} {
		ok, err := columnExists(d, DialectSQLite, "ext_keys", col)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Errorf("LateAdd 列 %s 未被补齐", col)
		}
	}
	// 初始列 last_used_at 不在补列范围内
	ok, err := columnExists(d, DialectSQLite, "ext_keys", "last_used_at")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("初始列 last_used_at 不该被自动补上")
	}
	// 查询层 fail loud：点名缺列，而不是静默返回零值
	var lastUsed sql.NullTime
	err = d.QueryRow(`SELECT last_used_at FROM ext_keys WHERE key='all-sk-old'`).Scan(&lastUsed)
	if err == nil {
		t.Fatal("查询缺失的初始列必须报错（fail loud），而不是返回零值")
	}
	if !strings.Contains(err.Error(), "last_used_at") {
		t.Fatalf("查询错误必须点名缺失列，实际=%v", err)
	}
}
