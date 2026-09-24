package store

import (
	"errors"
	"strings"
	"testing"
)

func TestCreateExtKeyFormat(t *testing.T) {
	d := testDB(t)
	k, err := CreateExtKey(d, "test-label", "test-remark", 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(k.Key, "all-sk-") {
		t.Fatalf("missing prefix: %q", k.Key)
	}
	if len(k.Key) < 39 {
		t.Fatalf("key too short: %q (len %d)", k.Key, len(k.Key))
	}
	if k.Label != "test-label" {
		t.Fatalf("label=%q", k.Label)
	}
	if k.Remark != "test-remark" {
		t.Fatalf("remark=%q", k.Remark)
	}
	if !k.Enabled {
		t.Fatal("should be enabled")
	}
}

func TestCreateExtKeysUnique(t *testing.T) {
	d := testDB(t)
	k1, _ := CreateExtKey(d, "a", "", 0, 0, nil)
	k2, _ := CreateExtKey(d, "b", "", 0, 0, nil)
	if k1.Key == k2.Key {
		t.Fatal("duplicate keys generated")
	}
}

// TestExtKeyNameUnique 名称唯一性：同名活跃密钥被拒且不落库；更新时排除自己；
// 软删除的行不占名称名额；空名不参与（接口直建/历史密钥可能没有名称）。
// 应用层判定之外还有部分唯一索引 idx_ext_keys_label 在 DB 层兜底（见 db 包）。
func TestExtKeyNameUnique(t *testing.T) {
	d := testDB(t)
	prod, err := CreateExtKey(d, "prod", "", 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateExtKey(d, "prod", "", 0, 0, nil); !errors.Is(err, ErrExtKeyLabelTaken) {
		t.Fatalf("duplicate create err=%v want ErrExtKeyLabelTaken", err)
	}
	list, _ := ListExtKeys(d)
	if len(list) != 1 {
		t.Fatalf("rejected duplicate must not insert: len=%d", len(list))
	}

	// 空名可以有多个
	if _, err := CreateExtKey(d, "", "", 0, 0, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateExtKey(d, "", "", 0, 0, nil); err != nil {
		t.Fatalf("empty names should not collide: %v", err)
	}

	other, _ := CreateExtKey(d, "other", "", 0, 0, nil)
	if err := UpdateExtKey(d, other.ID, "prod", "", true, 0, 0, nil); !errors.Is(err, ErrExtKeyLabelTaken) {
		t.Fatalf("rename onto taken label err=%v want ErrExtKeyLabelTaken", err)
	}
	if got, _ := GetExtKeyByID(d, other.ID); got.Label != "other" {
		t.Fatalf("rejected rename must not persist: label=%q", got.Label)
	}
	// 改成空闲名、保留自己的名字、改成空名都放行
	if err := UpdateExtKey(d, other.ID, "other2", "", true, 0, 0, nil); err != nil {
		t.Fatalf("rename to free label: %v", err)
	}
	if err := UpdateExtKey(d, other.ID, "other2", "note", true, 0, 0, nil); err != nil {
		t.Fatalf("update keeping own label: %v", err)
	}
	if err := UpdateExtKey(d, other.ID, "", "", true, 0, 0, nil); err != nil {
		t.Fatalf("update to empty label: %v", err)
	}

	// 软删除后名称释放
	if err := DeleteExtKey(d, prod.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateExtKey(d, "prod", "", 0, 0, nil); err != nil {
		t.Fatalf("label should be free after soft delete: %v", err)
	}
}

// TestExtKeyLabelTrimmed 名称按 TrimSpace 归一存储：仅靠前后空白区分的名字视为
// 同名（UI 侧也 trim，两端口径一致）。
func TestExtKeyLabelTrimmed(t *testing.T) {
	d := testDB(t)
	k, err := CreateExtKey(d, " prod ", "", 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if k.Label != "prod" {
		t.Fatalf("label=%q want trimmed prod", k.Label)
	}
	if got, _ := GetExtKeyByID(d, k.ID); got.Label != "prod" {
		t.Fatalf("persisted label=%q want prod", got.Label)
	}
	if _, err := CreateExtKey(d, "prod", "", 0, 0, nil); !errors.Is(err, ErrExtKeyLabelTaken) {
		t.Fatalf("duplicate after trim err=%v want ErrExtKeyLabelTaken", err)
	}
	if err := UpdateExtKey(d, k.ID, " prod ", "r", true, 0, 0, nil); err != nil {
		t.Fatalf("re-save trimmed-to-same label: %v", err)
	}
}

// TestExtKeyUpdateLegacyDuplicateLabel 老库里本就有重名活跃 key（唯一性约束加入前
// 的数据）时，不改名的更新不能被别人的重名锁死；只有改到占用中的名字才被拒。
// 重名状态靠先删掉兜底索引再裸 UPDATE 模拟（有索引时造不出来——这正是索引的意义）。
func TestExtKeyUpdateLegacyDuplicateLabel(t *testing.T) {
	d := testDB(t)
	if _, err := CreateExtKey(d, "dup", "", 0, 0, nil); err != nil {
		t.Fatal(err)
	}
	b, _ := CreateExtKey(d, "other", "", 0, 0, nil)
	if _, err := d.Exec(`DROP INDEX idx_ext_keys_label`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE ext_keys SET label='dup' WHERE id=?`, b.ID); err != nil {
		t.Fatal(err)
	}

	// 不改名的部分更新（限额/备注/启停）放行
	if err := UpdateExtKey(d, b.ID, "dup", "only-remark", true, 100, 0, nil); err != nil {
		t.Fatalf("non-rename update on legacy duplicate: %v", err)
	}
	got, _ := GetExtKeyByID(d, b.ID)
	if got.Label != "dup" || got.Remark != "only-remark" || got.DailyTokenLimit != 100 {
		t.Fatalf("update not persisted: %+v", got)
	}
	// 改到另一个占用中的名字仍然被拒
	if _, err := CreateExtKey(d, "taken", "", 0, 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := UpdateExtKey(d, b.ID, "taken", "", true, 0, 0, nil); !errors.Is(err, ErrExtKeyLabelTaken) {
		t.Fatalf("rename onto taken label err=%v want ErrExtKeyLabelTaken", err)
	}
}

// TestExtKeyLabelUniqueIndex 重名活跃 key 连裸 SQL 都插不进（DB 层兜底生效）。
func TestExtKeyLabelUniqueIndex(t *testing.T) {
	d := testDB(t)
	k, _ := CreateExtKey(d, "idx-prod", "", 0, 0, nil)
	if _, err := d.Exec(`INSERT INTO ext_keys (key, label) VALUES ('all-sk-rawdup', 'idx-prod')`); err == nil {
		t.Fatal("raw duplicate label insert should be rejected by idx_ext_keys_label")
	}
	// 软删后不占名额：裸插同名也放行
	if err := DeleteExtKey(d, k.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO ext_keys (key, label) VALUES ('all-sk-rawdup', 'idx-prod')`); err != nil {
		t.Fatalf("label should be reusable after soft delete: %v", err)
	}
}

func TestGetExtKey(t *testing.T) {
	d := testDB(t)
	k, _ := CreateExtKey(d, "n", "r", 0, 0, nil)
	got, err := GetExtKey(d, k.Key)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != k.ID {
		t.Fatalf("id=%d want %d", got.ID, k.ID)
	}
	if got.Label != "n" || got.Remark != "r" {
		t.Fatalf("label=%q remark=%q", got.Label, got.Remark)
	}
	_, err = GetExtKey(d, "all-sk-nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent key")
	}
}

func TestListExtKeysFullKey(t *testing.T) {
	d := testDB(t)
	k, _ := CreateExtKey(d, "l", "", 0, 0, nil)
	list, err := ListExtKeys(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("len=%d", len(list))
	}
	if list[0].Key != k.Key {
		t.Fatalf("list key=%q want full key %q", list[0].Key, k.Key)
	}
}

func TestDeleteExtKey(t *testing.T) {
	d := testDB(t)
	k, _ := CreateExtKey(d, "l", "", 0, 0, nil)
	if err := DeleteExtKey(d, k.ID); err != nil {
		t.Fatal(err)
	}
	list, _ := ListExtKeys(d)
	if len(list) != 0 {
		t.Fatalf("after delete len=%d", len(list))
	}
}

func TestTouchExtKey(t *testing.T) {
	d := testDB(t)
	k, _ := CreateExtKey(d, "l", "", 0, 0, nil)
	if err := TouchExtKey(d, k.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := GetExtKey(d, k.Key)
	if got.LastUsedAt == nil {
		t.Fatal("last_used_at not set")
	}
}

// TestSoftDeleteExtKey verifies a deleted key is kept in the table but
// immediately fails auth lookups and disappears from lists.
func TestSoftDeleteExtKey(t *testing.T) {
	d := testDB(t)
	k, _ := CreateExtKey(d, "l", "", 0, 0, nil)

	if err := DeleteExtKey(d, k.ID); err != nil {
		t.Fatal(err)
	}
	// 认证查询（按 key 值）立即失效
	if _, err := GetExtKey(d, k.Key); err == nil {
		t.Fatal("deleted key still authenticates")
	}
	if _, err := GetExtKeyByID(d, k.ID); err == nil {
		t.Fatal("deleted key still resolvable by id")
	}
	// 列表不再出现，但行保留
	list, _ := ListExtKeys(d)
	if len(list) != 0 {
		t.Fatalf("list after delete len=%d", len(list))
	}
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM ext_keys WHERE key=?`, k.Key).Scan(&n); err != nil || n != 1 {
		t.Fatalf("row should be kept: n=%d err=%v", n, err)
	}
	// 幂等：重复删除不报错
	if err := DeleteExtKey(d, k.ID); err != nil {
		t.Fatalf("second delete should be a no-op: %v", err)
	}
}

// TestExtKeyAllowedModels 覆盖白名单读路径（raw SQL 写入，写路径随管理端
// CRUD 接入）：空串/空数组 = 不限；JSON 数组解析；AllowsModel 精确匹配；
// 非法 JSON 响亮失败而非静默放行。
func TestExtKeyAllowedModels(t *testing.T) {
	d := testDB(t)
	k, _ := CreateExtKey(d, "l", "", 0, 0, nil)

	// 默认：不限制
	got, err := GetExtKey(d, k.Key)
	if err != nil {
		t.Fatal(err)
	}
	if got.AllowedModels != nil {
		t.Fatalf("default allowed_models=%v want nil", got.AllowedModels)
	}
	if !got.AllowsModel("any/model") {
		t.Fatal("empty allowlist should allow everything")
	}

	// 写入白名单后读取 + 精确匹配
	if _, err := d.Exec(`UPDATE ext_keys SET allowed_models=? WHERE id=?`, `["deepseek/deepseek-chat","gpt"]`, k.ID); err != nil {
		t.Fatal(err)
	}
	got, err = GetExtKey(d, k.Key)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.AllowedModels) != 2 || got.AllowedModels[0] != "deepseek/deepseek-chat" || got.AllowedModels[1] != "gpt" {
		t.Fatalf("allowed_models=%v", got.AllowedModels)
	}
	if !got.AllowsModel("gpt") || !got.AllowsModel("deepseek/deepseek-chat") {
		t.Fatalf("listed entries should be allowed: %v", got.AllowedModels)
	}
	if got.AllowsModel("deepseek/other") || got.AllowsModel("gp") {
		t.Fatalf("unlisted entries should be denied: %v", got.AllowedModels)
	}

	// GetExtKeyByID / ListExtKeys 同样带出
	byID, err := GetExtKeyByID(d, k.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(byID.AllowedModels) != 2 {
		t.Fatalf("by id allowed_models=%v", byID.AllowedModels)
	}
	list, err := ListExtKeys(d)
	if err != nil || len(list) != 1 {
		t.Fatalf("list err=%v len=%d", err, len(list))
	}
	if len(list[0].AllowedModels) != 2 {
		t.Fatalf("list allowed_models=%v", list[0].AllowedModels)
	}

	// 空数组 = 不限
	if _, err := d.Exec(`UPDATE ext_keys SET allowed_models='[]' WHERE id=?`, k.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = GetExtKey(d, k.Key)
	if got.AllowedModels != nil || !got.AllowsModel("x") {
		t.Fatalf("empty array should mean unrestricted: %v", got.AllowedModels)
	}

	// 非法 JSON：响亮失败
	if _, err := d.Exec(`UPDATE ext_keys SET allowed_models='{"a":1}' WHERE id=?`, k.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := GetExtKey(d, k.Key); err == nil {
		t.Fatal("corrupt allowed_models should error, not silently allow")
	}
}
