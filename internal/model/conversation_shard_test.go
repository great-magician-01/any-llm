package model

import (
	"strings"
	"testing"
	"time"
)

func TestConvShardName(t *testing.T) {
	ts := time.Date(2026, 9, 12, 15, 30, 0, 0, time.Local)
	if got := ConvShardName(ts); got != "conversation_records_2026_09" {
		t.Fatalf("ConvShardName=%q", got)
	}
	if got := convMonthKey(ts); got != "2026-09" {
		t.Fatalf("convMonthKey=%q", got)
	}
	if got := convShardNameToKey("conversation_records_2026_09"); got != "2026-09" {
		t.Fatalf("convShardNameToKey=%q", got)
	}
	// 一月/十二月边界（零填充保证字典序=时间序）
	if got := ConvShardName(time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)); got != "conversation_records_2026_01" {
		t.Fatalf("ConvShardName jan=%q", got)
	}
	a := ConvShardName(time.Date(2026, 12, 1, 0, 0, 0, 0, time.Local))
	b := ConvShardName(time.Date(2027, 1, 1, 0, 0, 0, 0, time.Local))
	if !(a < b) {
		t.Fatalf("names not chronologically ordered: %q vs %q", a, b)
	}
}

func TestConvShardNameRe(t *testing.T) {
	valid := []string{"conversation_records_2026_09", "conversation_records_1999_01"}
	for _, s := range valid {
		if !convShardNameRe.MatchString(s) {
			t.Errorf("%q should match", s)
		}
	}
	invalid := []string{
		"conversation_records",             // 历史分表不是月分表
		"conversation_records_2026_9",      // 月份未零填充
		"conversation_records_2026_091",    // 超长
		"conversation_records_2026-09",     // 错误分隔符
		"conversation_records_2026_09_bak", // 后缀
		"xconversation_records_2026_09",    // 前缀
		`conversation_records_2026_09"; DROP TABLE ext_keys; --`, // 注入
		"", // 空
	}
	for _, s := range invalid {
		if convShardNameRe.MatchString(s) {
			t.Errorf("%q should not match", s)
		}
	}
}

func TestConvShardDDL(t *testing.T) {
	stmts := convShardDDL("conversation_records_2026_09")
	if len(stmts) != 5 {
		t.Fatalf("want 5 stmts, got %d", len(stmts))
	}
	if !strings.Contains(stmts[0], "CREATE SEQUENCE IF NOT EXISTS conversation_records_id_seq") {
		t.Errorf("seq stmt: %q", stmts[0])
	}
	// 表 DDL：共享序列默认值 + 主键
	if !strings.Contains(stmts[1], "CREATE TABLE IF NOT EXISTS conversation_records_2026_09") ||
		!strings.Contains(stmts[1], "DEFAULT nextval('conversation_records_id_seq') PRIMARY KEY") {
		t.Errorf("table stmt: %q", stmts[1])
	}
	// 索引带月份后缀且幂等
	for i, want := range []string{"idx_conv_2026_09_created", "idx_conv_2026_09_ext_key", "idx_conv_2026_09_harness"} {
		if !strings.Contains(stmts[2+i], "CREATE INDEX IF NOT EXISTS "+want+" ON conversation_records_2026_09") {
			t.Errorf("index stmt %d: %q", i, stmts[2+i])
		}
	}
	// 非法表名不生成 DDL
	if convShardDDL("conversation_records_2026_9; DROP TABLE ext_keys") != nil {
		t.Error("bad name should yield nil")
	}
}

func TestRegisterConvShard(t *testing.T) {
	resetConvShardCache(t)
	// 乱序注册，months 应保持新→旧
	registerConvShard("2026-09", "conversation_records_2026_09")
	registerConvShard("2026-11", "conversation_records_2026_11")
	registerConvShard("2026-10", "conversation_records_2026_10")
	convShardCache.RLock()
	got := append([]string(nil), convShardCache.months...)
	convShardCache.RUnlock()
	want := []string{"conversation_records_2026_11", "conversation_records_2026_10", "conversation_records_2026_09"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("months=%v, want %v", got, want)
	}
	// 幂等：重复注册不产生重复项
	registerConvShard("2026-10", "conversation_records_2026_10")
	convShardCache.RLock()
	n := len(convShardCache.months)
	convShardCache.RUnlock()
	if n != 3 {
		t.Fatalf("after re-register months len=%d, want 3", n)
	}
	// 月份键查询
	if name, ok := convShardForMonth("2026-11"); !ok || name != "conversation_records_2026_11" {
		t.Fatalf("convShardForMonth=%q,%v", name, ok)
	}
	if _, ok := convShardForMonth("2026-12"); ok {
		t.Fatal("unexpected hit for 2026-12")
	}
}

// resetConvShardCache 清空包级注册缓存并注册 t.Cleanup 还原，保证测试间隔离。
func resetConvShardCache(t *testing.T) {
	t.Helper()
	convShardCache.Lock()
	saved := struct {
		loaded  bool
		months  []string
		byMonth map[string]string
		hasBase bool
	}{convShardCache.loaded, convShardCache.months, convShardCache.byMonth, convShardCache.hasBase}
	convShardCache.loaded = false
	convShardCache.months = nil
	convShardCache.byMonth = nil
	convShardCache.hasBase = false
	convShardCache.Unlock()
	t.Cleanup(func() {
		convShardCache.Lock()
		convShardCache.loaded = saved.loaded
		convShardCache.months = saved.months
		convShardCache.byMonth = saved.byMonth
		convShardCache.hasBase = saved.hasBase
		convShardCache.Unlock()
	})
}

func TestConvPageWindows(t *testing.T) {
	cases := []struct {
		name          string
		counts        []int
		offset, size  int
		want          []convWindow
	}{
		{"空分表", nil, 0, 10, nil},
		{"单表首页", []int{100}, 0, 20, []convWindow{{0, 0, 20}}},
		{"单表深页", []int{100}, 60, 20, []convWindow{{0, 60, 20}}},
		{"offset跳过整表", []int{10, 20, 30}, 10, 5, []convWindow{{1, 0, 5}}},
		{"跨两张表", []int{10, 20}, 5, 10, []convWindow{{0, 5, 5}, {1, 0, 5}}},
		{"跨三张表", []int{10, 20, 30}, 5, 50, []convWindow{{0, 5, 5}, {1, 0, 20}, {2, 0, 25}}},
		{"offset超出总量", []int{10, 20}, 30, 10, nil},
		{"末页不足size", []int{10, 20}, 25, 10, []convWindow{{1, 15, 5}}},
		{"中间空表跳过", []int{10, 0, 30}, 8, 5, []convWindow{{0, 8, 2}, {2, 0, 3}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := convPageWindows(c.counts, c.offset, c.size)
			if len(got) != len(c.want) {
				t.Fatalf("windows=%v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("windows[%d]=%v, want %v (all=%v)", i, got[i], c.want[i], got)
				}
			}
		})
	}
}
