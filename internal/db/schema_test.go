package db

import (
	"strings"
	"testing"
)

// mustDDL 渲染并断言无错。
func mustDDL(t *testing.T, tbl Table, d Dialect, cfg DDLConfig) []string {
	t.Helper()
	stmts, err := tbl.DDL(d, cfg)
	if err != nil {
		t.Fatalf("%s DDL on %s: %v", tbl.Name, d, err)
	}
	return stmts
}

func joinDDL(stmts []string) string { return strings.Join(stmts, "\n") }

// TestSchemaSQLiteMatchesHistoricalShape 盯住 SQLite 建表形态：它是存量库已经在
// 用的形状，重构不能改。
func TestSchemaSQLiteMatchesHistoricalShape(t *testing.T) {
	tbl, ok := schemaTableByName("ext_keys")
	if !ok {
		t.Fatal("ext_keys missing")
	}
	got := mustDDL(t, tbl, DialectSQLite, DDLConfig{IfNotExists: true})[0]
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS ext_keys (",
		"id INTEGER PRIMARY KEY AUTOINCREMENT",
		"key TEXT NOT NULL",
		"label TEXT NOT NULL DEFAULT ''",
		"created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP",
		"last_used_at DATETIME",
		"is_active INTEGER NOT NULL DEFAULT 1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("SQLite ext_keys DDL missing %q:\n%s", want, got)
		}
	}
	// 部分唯一索引必须是独立语句，且带谓词。
	idx := joinDDL(mustDDL(t, tbl, DialectSQLite, DDLConfig{IfNotExists: true})[1:])
	for _, want := range []string{
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_ext_keys_key ON ext_keys(key) WHERE is_active = 1",
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_ext_keys_label ON ext_keys(label) WHERE is_active = 1 AND label <> ''",
	} {
		if !strings.Contains(idx, want) {
			t.Errorf("SQLite ext_keys indexes missing %q:\n%s", want, idx)
		}
	}
}

func TestSchemaPostgresMatchesHistoricalShape(t *testing.T) {
	tbl, _ := schemaTableByName("upstreams")
	got := mustDDL(t, tbl, DialectPostgres, DDLConfig{IfNotExists: true})[0]
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS upstreams (",
		"id BIGSERIAL PRIMARY KEY",
		"created_at TIMESTAMP(0) NOT NULL DEFAULT CURRENT_TIMESTAMP",
		"expires_at TIMESTAMP(0)",
		// remark 是后加的列（LateAdd）：老库靠 ALTER 补，新库就在这里带默认值。
		"remark TEXT NOT NULL DEFAULT ''",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("PG upstreams DDL missing %q:\n%s", want, got)
		}
	}
	// SQLite 特有写法不能漏到 PG 上。
	for _, bad := range []string{"AUTOINCREMENT", "DATETIME"} {
		if strings.Contains(got, bad) {
			t.Errorf("PG DDL unexpectedly contains %q:\n%s", bad, got)
		}
	}
}

// TestSchemaMySQLRendersInnoDBShape 是 MySQL 渲染的核心断言。
func TestSchemaMySQLRendersInnoDBShape(t *testing.T) {
	tbl, _ := schemaTableByName("ext_keys")
	got := mustDDL(t, tbl, DialectMySQL, DDLConfig{IfNotExists: true})
	if len(got) != 1 {
		t.Fatalf("MySQL DDL should be a single statement (indexes inline), got %d", len(got))
	}
	ddl := got[0]
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS `ext_keys` (",
		"`id` BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY",
		// key/label 要建索引 → VARCHAR；remark/allowed_models 不建索引 → 保持 TEXT。
		// TEXT 不能带裸字面量默认值（错误 1101），必须写成表达式形式（见
		// TestSchemaMySQLExpressionDefaults）。
		"`key` VARCHAR(191) NOT NULL",
		"`label` VARCHAR(191) NOT NULL DEFAULT ''",
		"`remark` TEXT NOT NULL DEFAULT ('')",
		"`allowed_models` TEXT NOT NULL DEFAULT ('')",
		"`created_at` DATETIME(0) NOT NULL DEFAULT CURRENT_TIMESTAMP",
		// 部分唯一索引降级为生成列 + 普通唯一键。
		"`g_idx_ext_keys_key` VARCHAR(191) GENERATED ALWAYS AS (IF(is_active = 1, `key`, NULL)) STORED",
		"UNIQUE KEY `idx_ext_keys_key` (`g_idx_ext_keys_key`)",
		"`g_idx_ext_keys_label` VARCHAR(191) GENERATED ALWAYS AS (IF(is_active = 1 AND label <> '', `label`, NULL)) STORED",
		"UNIQUE KEY `idx_ext_keys_label` (`g_idx_ext_keys_label`)",
		") DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin",
	} {
		if !strings.Contains(ddl, want) {
			t.Errorf("MySQL ext_keys DDL missing %q:\n%s", want, ddl)
		}
	}
	// key 是 MySQL 保留字，必须被反引号包住。
	if strings.Contains(ddl, " key ") {
		t.Errorf("MySQL DDL has unquoted reserved word `key`:\n%s", ddl)
	}
}

// TestSchemaMySQLUpstreamRemarkIsText 盯住 upstreams.remark 在 MySQL 上的形态：
// 不建索引 → 保持 TEXT（不是 VARCHAR，不引入任意长度上限），而 TEXT 不接受裸
// 字面量默认值（错误 1101），必须写成表达式形式。谁给它补上 Len 或去掉括号，
// 这里就会挂。
func TestSchemaMySQLUpstreamRemarkIsText(t *testing.T) {
	tbl, _ := schemaTableByName("upstreams")
	ddl := mustDDL(t, tbl, DialectMySQL, DDLConfig{IfNotExists: true})[0]
	if !strings.Contains(ddl, "`remark` TEXT NOT NULL DEFAULT ('')") {
		t.Errorf("upstreams.remark must be TEXT with a parenthesized default on MySQL:\n%s", ddl)
	}
	if strings.Contains(ddl, "`remark` VARCHAR") {
		t.Errorf("upstreams.remark must not become VARCHAR on MySQL:\n%s", ddl)
	}
}

// TestSchemaMySQLPartialUniqueIsCompositeAware 盯住复合部分唯一索引：只有最后
// 一列换成生成列，前导列保持真实列。少了这一步，(upstream_id, model_name) 的语义
// 就丢了。
func TestSchemaMySQLPartialUniqueIsCompositeAware(t *testing.T) {
	tbl, _ := schemaTableByName("upstream_models")
	ddl := mustDDL(t, tbl, DialectMySQL, DDLConfig{IfNotExists: true})[0]
	for _, want := range []string{
		"`g_idx_upstream_models_uid_name` VARCHAR(255) GENERATED ALWAYS AS (IF(is_active = 1, `model_name`, NULL)) STORED",
		"UNIQUE KEY `idx_upstream_models_uid_name` (`upstream_id`, `g_idx_upstream_models_uid_name`)",
	} {
		if !strings.Contains(ddl, want) {
			t.Errorf("MySQL upstream_models DDL missing %q:\n%s", want, ddl)
		}
	}
}

// TestSchemaMySQLLongTextForSessionMessages 盯住 64 KiB 陷阱：MySQL 的 TEXT 只有
// 64 KiB，而 messages 存的是累积的整段会话历史，长会话会直接写失败。
func TestSchemaMySQLLongTextForSessionMessages(t *testing.T) {
	tbl, _ := schemaTableByName("response_sessions")
	ddl := mustDDL(t, tbl, DialectMySQL, DDLConfig{IfNotExists: true})[0]
	if !strings.Contains(ddl, "`messages` LONGTEXT NOT NULL") {
		t.Errorf("response_sessions.messages must be LONGTEXT on MySQL:\n%s", ddl)
	}
	// id 是主键，TEXT 不能作主键。
	if !strings.Contains(ddl, "`id` VARCHAR(64) PRIMARY KEY") {
		t.Errorf("response_sessions.id must be VARCHAR PK on MySQL:\n%s", ddl)
	}
	// 主键只许声明一次：列内联 + 表级约束同时存在会让 MySQL 报
	// "Multiple primary key defined"。
	if n := strings.Count(ddl, "PRIMARY KEY"); n != 1 {
		t.Errorf("response_sessions declares PRIMARY KEY %d times, want 1:\n%s", n, ddl)
	}
}

// TestSchemaMySQLRejectsIndexedTextWithoutLen 验证渲染器的自校验：不给 Len 的
// TEXT 列一旦要建索引或作主键，直接报错，而不是等到建表时才吐一句没头没尾的语法
// 错误。
func TestSchemaMySQLRejectsIndexedTextWithoutLen(t *testing.T) {
	bad := Table{
		Name: "broken",
		Cols: []Column{
			{Name: "id", Type: TypeBigInt, AutoID: true},
			{Name: "name", Type: TypeText},
		},
		Idx: []Index{
			{Name: "idx_broken_name", Columns: []string{"name"}, Unique: true, Where: "is_active = 1"},
		},
	}
	if _, err := bad.CreateTableDDL(DialectMySQL, DDLConfig{}); err == nil {
		t.Fatal("expected an error for an indexed TEXT column without Len")
	} else if !strings.Contains(err.Error(), "name") || !strings.Contains(err.Error(), "Len") {
		t.Fatalf("error should name the column and the fix, got: %v", err)
	}
}

// TestSchemaMySQLPartialUniqueSurvivesIndexSuffix 盯住一处容易漂移的地方：生成列
// 名由「已套用月份后缀的索引名」派生，而 KEY 引用的也必须是同一个名字。两侧各自
// 取后缀就会生成一份引用了未定义列的 DDL。现有分表没有部分唯一索引，所以这是
// 潜伏问题 —— 用一张合成表把它钉死。
func TestSchemaMySQLPartialUniqueSurvivesIndexSuffix(t *testing.T) {
	tbl := Table{
		Name: "conv_shard",
		Cols: []Column{
			{Name: "id", Type: TypeBigInt, PrimaryKey: true},
			{Name: "name", Type: TypeText, Len: 255},
			{Name: "is_active", Type: TypeInt, Default: "1"},
		},
		Idx: []Index{
			{Name: "idx_conv_{}_name", Columns: []string{"name"}, Unique: true, Where: "is_active = 1"},
		},
	}
	ddl := mustDDL(t, tbl, DialectMySQL, DDLConfig{IfNotExists: true, IndexSuffix: "2026_09"})[0]
	// 生成列与 KEY 必须用同一个名字。
	if !strings.Contains(ddl, "`g_idx_conv_2026_09_name` VARCHAR(255) GENERATED ALWAYS AS") {
		t.Errorf("generated column missing the suffix:\n%s", ddl)
	}
	if !strings.Contains(ddl, "UNIQUE KEY `idx_conv_2026_09_name` (`g_idx_conv_2026_09_name`)") {
		t.Errorf("KEY does not reference the suffixed generated column:\n%s", ddl)
	}
	// 没套后缀的旧名字一个都不许出现。
	for _, bad := range []string{"g_idx_conv{}_name", "`g_idx_conv_name`", "`idx_conv{}_name`"} {
		if strings.Contains(ddl, bad) {
			t.Errorf("DDL still contains un-suffixed %q:\n%s", bad, ddl)
		}
	}
	// SQLite/PG 侧同样要套后缀。
	idx := joinDDL(mustDDL(t, tbl, DialectPostgres, DDLConfig{IfNotExists: true, IndexSuffix: "2026_09"})[1:])
	if !strings.Contains(idx, "CREATE UNIQUE INDEX IF NOT EXISTS idx_conv_2026_09_name") {
		t.Errorf("postgres index name missing the suffix:\n%s", idx)
	}
}

// TestSchemaMySQLExpressionDefaults 盯住 MySQL 错误 1101：BLOB/TEXT/JSON 列拒绝
// 裸字面量默认值，必须写成带括号的表达式形式（8.0.13+）。
// 这里不是「丢掉默认值」——空串/空对象的含义必须三种方言一致，否则同一条 INSERT
// 在 PG/SQLite 静默默认、在 MySQL 报 1364。
func TestSchemaMySQLExpressionDefaults(t *testing.T) {
	cases := []struct {
		col  Column
		want string // MySQL 上的 DEFAULT 片段
	}{
		{Column{Name: "a", Type: TypeText, Default: "''"}, "DEFAULT ('')"},         // TEXT 无 Len
		{Column{Name: "b", Type: TypeText, Len: 255, Default: "''"}, "DEFAULT ''"}, // VARCHAR 不需要括号
		{Column{Name: "c", Type: TypeLongText, Default: "''"}, "DEFAULT ('')"},     // LONGTEXT
		{Column{Name: "d", Type: TypeJSON, Default: "'{}'"}, "DEFAULT ('{}')"},     // JSON
		{Column{Name: "e", Type: TypeInt, Default: "0"}, "DEFAULT 0"},              // 标量不动
		{Column{Name: "f", Type: TypeTime, Default: "CURRENT_TIMESTAMP"}, "DEFAULT CURRENT_TIMESTAMP"},
		{Column{Name: "g", Type: TypeText, Nullable: true}, ""}, // 无默认值：一个 DEFAULT 都不许有
	}
	for _, tc := range cases {
		tbl := Table{Name: "t", Cols: []Column{{Name: "id", Type: TypeBigInt, AutoID: true}, tc.col}}
		def, err := tbl.columnDefMySQL(tc.col, DDLConfig{})
		if err != nil {
			t.Fatalf("%s: %v", tc.col.Name, err)
		}
		if tc.want == "" {
			if strings.Contains(def, "DEFAULT") {
				t.Errorf("%s: column without a default must not render DEFAULT: %q", tc.col.Name, def)
			}
			continue
		}
		if !strings.Contains(def, tc.want) {
			t.Errorf("%s: MySQL column def missing %q: %q", tc.col.Name, tc.want, def)
		}
		// 整表渲染也要能看到（防止只有 columnDef 分支对）。
		if ddl := mustDDL(t, tbl, DialectMySQL, DDLConfig{})[0]; !strings.Contains(ddl, def) {
			t.Errorf("%s: table DDL missing column def %q:\n%s", tc.col.Name, def, ddl)
		}
	}
	// PG/SQLite 保持裸字面量，不能被括号污染。
	for _, d := range []Dialect{DialectSQLite, DialectPostgres} {
		tbl := Table{Name: "t", Cols: []Column{{Name: "id", Type: TypeBigInt, AutoID: true},
			{Name: "a", Type: TypeText, Default: "''"}}}
		ddl := mustDDL(t, tbl, d, DDLConfig{})[0]
		if !strings.Contains(ddl, "a TEXT NOT NULL DEFAULT ''") {
			t.Errorf("%s should keep the literal default:\n%s", d, ddl)
		}
	}
	// cfg.ColumnDefaults 里的表达式（PG 分表的 nextval）原样透传，不再包一层。
	tbl := Table{Name: "t", Cols: []Column{{Name: "id", Type: TypeBigInt, PrimaryKey: true}}}
	ddl := mustDDL(t, tbl, DialectPostgres, DDLConfig{ColumnDefaults: map[string]string{"id": "nextval('seq_x')"}})[0]
	if !strings.Contains(ddl, "DEFAULT nextval('seq_x')") {
		t.Errorf("ColumnDefaults must pass through unwrapped:\n%s", ddl)
	}
}

// TestSchemaIdSequencesIsMySQLOnly 确认不会为了「统一」让 PG/SQLite 也长出一张
// 用不到的表。
func TestSchemaIdSequencesIsMySQLOnly(t *testing.T) {
	tbl, ok := schemaTableByName("id_sequences")
	if !ok {
		t.Fatal("id_sequences missing")
	}
	if _, err := tbl.CreateTableDDL(DialectPostgres, DDLConfig{}); err == nil {
		t.Error("id_sequences must not render on postgres")
	}
	if _, err := tbl.CreateTableDDL(DialectSQLite, DDLConfig{}); err == nil {
		t.Error("id_sequences must not render on sqlite")
	}
	if _, err := tbl.CreateTableDDL(DialectMySQL, DDLConfig{}); err != nil {
		t.Errorf("id_sequences on mysql: %v", err)
	}
	for _, d := range []Dialect{DialectSQLite, DialectPostgres} {
		for _, other := range tablesFor(d) {
			if other.Name == "id_sequences" {
				t.Errorf("%s should not create id_sequences", d)
			}
		}
	}
}

// TestSchemaAllTablesRenderOnEveryDialect 兜底：schema 里每张表在它该出现的方言
// 上都能渲染，且 MySQL 渲染一律带 charset/collation。
func TestSchemaAllTablesRenderOnEveryDialect(t *testing.T) {
	for _, tbl := range schemaTables {
		for _, d := range []Dialect{DialectSQLite, DialectPostgres, DialectMySQL} {
			if tbl.Only != "" && tbl.Only != d {
				continue
			}
			stmts, err := tbl.DDL(d, DDLConfig{IfNotExists: true})
			if err != nil {
				t.Errorf("%s on %s: %v", tbl.Name, d, err)
				continue
			}
			if d == DialectMySQL && !strings.Contains(stmts[0], "utf8mb4_bin") {
				t.Errorf("%s on mysql missing utf8mb4_bin", tbl.Name)
			}
		}
	}
}

// TestConversationShardDDLDialects 覆盖分表 DDL 的三方言渲染：PG 的共享序列
// 默认值、MySQL 的显式 id 与内联索引。
func TestConversationShardDDLDialects(t *testing.T) {
	const seq = "conversation_records_id_seq"
	const name = "conversation_records_2026_09"

	pg, err := ConversationShardDDL(DialectPostgres, name, seq)
	if err != nil {
		t.Fatal(err)
	}
	pgDDL := joinDDL(pg)
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS conversation_records_2026_09 (",
		"id BIGINT NOT NULL DEFAULT nextval('conversation_records_id_seq') PRIMARY KEY",
		"request_ir JSONB NOT NULL DEFAULT '{}'",
		"request_raw BYTEA NOT NULL",
		"created_at TIMESTAMP(0) NOT NULL DEFAULT CURRENT_TIMESTAMP",
	} {
		if !strings.Contains(pgDDL, want) {
			t.Errorf("PG shard DDL missing %q:\n%s", want, pgDDL)
		}
	}
	for _, want := range []string{
		"CREATE INDEX IF NOT EXISTS idx_conv_2026_09_created ON conversation_records_2026_09(created_at)",
		"CREATE INDEX IF NOT EXISTS idx_conv_2026_09_ext_key ON conversation_records_2026_09(ext_key_id)",
		"CREATE INDEX IF NOT EXISTS idx_conv_2026_09_harness ON conversation_records_2026_09(harness)",
	} {
		if !strings.Contains(pgDDL, want) {
			t.Errorf("PG shard indexes missing %q:\n%s", want, pgDDL)
		}
	}

	my, err := ConversationShardDDL(DialectMySQL, name, seq)
	if err != nil {
		t.Fatal(err)
	}
	if len(my) != 1 {
		t.Fatalf("MySQL shard DDL should be one statement (indexes inline), got %d", len(my))
	}
	myDDL := my[0]
	for _, want := range []string{
		"`id` BIGINT NOT NULL PRIMARY KEY",
		"`harness` VARCHAR(64) NOT NULL",
		"`status` VARCHAR(32) NOT NULL DEFAULT 'ok'",
		"`request_ir` JSON NOT NULL",
		"`response_ir` JSON NOT NULL",
		"`request_raw` LONGBLOB NOT NULL",
		"`response_raw` LONGBLOB NOT NULL",
		"KEY `idx_conv_2026_09_created` (`created_at`)",
		"KEY `idx_conv_2026_09_ext_key` (`ext_key_id`)",
		"KEY `idx_conv_2026_09_harness` (`harness`)",
		") DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin",
	} {
		if !strings.Contains(myDDL, want) {
			t.Errorf("MySQL shard DDL missing %q:\n%s", want, myDDL)
		}
	}
	// MySQL 没有序列：id 由计数器表显式分配，不能留 nextval 默认值。
	if strings.Contains(myDDL, "nextval") || strings.Contains(myDDL, "AUTO_INCREMENT") {
		t.Errorf("MySQL shard DDL must not use a sequence:\n%s", myDDL)
	}
	// MySQL 的 JSON 列不能带 DEFAULT。
	if strings.Contains(myDDL, "DEFAULT '{}'") {
		t.Errorf("MySQL shard DDL must not default a JSON column:\n%s", myDDL)
	}

	if got := ShardSequenceStatements(DialectMySQL, seq); got != nil {
		t.Errorf("MySQL needs no sequence statements, got %v", got)
	}
	seqStmts := ShardSequenceStatements(DialectPostgres, seq)
	if len(seqStmts) != 1 || !strings.Contains(seqStmts[0], "CREATE SEQUENCE IF NOT EXISTS conversation_records_id_seq") {
		t.Errorf("PG sequence statements = %v", seqStmts)
	}
}

// TestConversationShardColsMatchSchema 盯住插入列清单与 schema 定义一致：
// store.insertConversationInto 直接用它拼装 INSERT。
func TestConversationShardColsMatchSchema(t *testing.T) {
	cols := ConversationShardCols()
	if len(cols) != len(conversationRecordsCols)-1 {
		t.Fatalf("got %d cols, schema has %d (minus the id column)", len(cols), len(conversationRecordsCols))
	}
	for _, name := range cols {
		if _, ok := ColumnByName(name); !ok {
			t.Errorf("column %q is in the insert list but not in the schema", name)
		}
	}
}
