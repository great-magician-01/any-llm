package db

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// 业务规则：对话归档的分表发现走 db.ListTablesLike —— 它必须是「当前库/schema
// 内、表名以 prefix 开头」的粗筛，前缀之后的精确白名单（convShardNameRe）由
// store 侧复校验。两条容易写错的语义：
//   - 前缀必须能捞到不带尾下划线的历史分表（conversation_records 本身），
//     否则存量库的历史归档会从读路径整体消失；
//   - 必须限定在当前 schema/库，同库里其他 schema 的同名前缀表不算数
//     （多实例共用一个 PG database 时，另一实例的表会让本实例读到别人的归档）。

func TestListTablesLikeSQLite(t *testing.T) {
	d, err := OpenSQLite(filepath.Join(t.TempDir(), "lt.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	for _, stmt := range []string{
		`CREATE TABLE conversation_records (id INTEGER PRIMARY KEY AUTOINCREMENT)`,
		`CREATE TABLE conversation_records_2026_01 (id INTEGER PRIMARY KEY AUTOINCREMENT)`,
		`CREATE TABLE conversation_records_2026_02 (id INTEGER PRIMARY KEY AUTOINCREMENT)`,
		// 同前缀但不符合月分表白名单：ListTablesLike 只做前缀粗筛，必须原样返回
		`CREATE TABLE conversation_records_extra (id INTEGER PRIMARY KEY AUTOINCREMENT)`,
		`CREATE TABLE unrelated (id INTEGER PRIMARY KEY AUTOINCREMENT)`,
	} {
		if _, err := d.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}

	got, err := ListTablesLike(d, "conversation_records")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"conversation_records":           true,
		"conversation_records_2026_01":   true,
		"conversation_records_2026_02":   true,
		"conversation_records_extra":     true,
		"unrelated":                      false,
		"conversation_records_2026_0100": false,
	}
	seen := map[string]bool{}
	for _, n := range got {
		seen[n] = true
	}
	for name, expected := range want {
		if seen[name] != expected {
			t.Errorf("表 %q 出现=%v，期望 %v（全部结果=%v）", name, seen[name], expected, got)
		}
	}
}

func TestListTablesLikePostgres(t *testing.T) {
	d := pgTestDB(t)

	// 本 schema 里的分表 + 历史分表 + 同前缀干扰表
	for _, stmt := range []string{
		`CREATE TABLE conversation_records (id BIGINT PRIMARY KEY)`,
		`CREATE TABLE conversation_records_2026_01 (id BIGINT PRIMARY KEY)`,
		`CREATE TABLE conversation_records_2026_02 (id BIGINT PRIMARY KEY)`,
		`CREATE TABLE conversation_records_extra (id BIGINT PRIMARY KEY)`,
		`CREATE TABLE unrelated (id BIGINT PRIMARY KEY)`,
	} {
		if _, err := d.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}

	// 另一个 schema 里的同名表：ListTablesLike 限定 current_schema()，必须看不见。
	// 单开一个 schema 而不是用 public —— CI 的 PG 服务容器上 public 未必可写。
	other := fmt.Sprintf("any_llm_probe_%d", time.Now().UnixNano())
	if _, err := d.Exec("CREATE SCHEMA " + other); err != nil {
		t.Fatalf("create probe schema: %v", err)
	}
	t.Cleanup(func() { d.Exec("DROP SCHEMA " + other + " CASCADE") })
	if _, err := d.Exec(`CREATE TABLE ` + other + `.conversation_records_2026_03 (id BIGINT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}

	got, err := ListTablesLike(d, "conversation_records")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, n := range got {
		seen[n] = true
	}
	for name, expected := range map[string]bool{
		"conversation_records":         true,
		"conversation_records_2026_01": true,
		"conversation_records_2026_02": true,
		"conversation_records_extra":   true, // 前缀粗筛：白名单过滤是 store 的事
		"unrelated":                    false,
		// 别的 schema 里的同名分表绝不能出现（多实例共用 PG 库时会串数据）
		"conversation_records_2026_03": false,
	} {
		if seen[name] != expected {
			t.Errorf("表 %q 出现=%v，期望 %v（全部结果=%v）", name, seen[name], expected, got)
		}
	}

	// 空结果不是错误（没有任何分表的库）
	none, err := ListTablesLike(d, "conv_shard_absent_prefix_")
	if err != nil {
		t.Fatalf("空结果不应报错: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("无匹配时应返回空列表，实际=%v", none)
	}
}

// 构造一个「老库遗留的 conversation_records」并确认 ListTablesLike 的前缀语义
// 与 loadShards 的白名单过滤合起来能把历史分表捞回来——前缀写成
// conversation_records_% 会漏掉它，这里用带下划线的前缀做反证。
func TestListTablesLikePrefixWithoutUnderscoreCatchesBaseTable(t *testing.T) {
	d, err := OpenSQLite(filepath.Join(t.TempDir(), "pfx.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec(`CREATE TABLE conversation_records (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`CREATE TABLE conversation_records_2026_05 (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}

	withUnderscore, err := ListTablesLike(d, "conversation_records_")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range withUnderscore {
		if n == "conversation_records" {
			t.Fatalf("带下划线的前缀不该匹配历史分表: %v", withUnderscore)
		}
	}

	// convBaseTable 形态的前缀必须同时捞到历史分表与月分表
	got, err := ListTablesLike(d, "conversation_records")
	if err != nil {
		t.Fatal(err)
	}
	var hasBase, hasShard bool
	for _, n := range got {
		hasBase = hasBase || n == "conversation_records"
		hasShard = hasShard || n == "conversation_records_2026_05"
	}
	if !hasBase || !hasShard {
		t.Fatalf("前缀粗筛必须同时捞到历史分表与月分表，实际=%v", got)
	}
}
