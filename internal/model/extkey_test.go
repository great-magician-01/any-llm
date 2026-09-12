package model

import (
	"errors"
	"strings"
	"testing"
)

func TestCreateExtKeyFormat(t *testing.T) {
	d := testDB(t)
	k, err := CreateExtKey(d, "test-name", "test-remark", 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(k.Key, "all-sk-") {
		t.Fatalf("missing prefix: %q", k.Key)
	}
	if len(k.Key) < 39 {
		t.Fatalf("key too short: %q (len %d)", k.Key, len(k.Key))
	}
	if k.Name != "test-name" {
		t.Fatalf("name=%q", k.Name)
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

// TestExtKeyNameUnique 名称唯一性（应用层，无 DB 约束）：同名活跃密钥被拒且不落库；
// 更新时排除自己；软删除的行不占名称名额；空名不参与（接口直建/历史密钥可能没有名称）。
func TestExtKeyNameUnique(t *testing.T) {
	d := testDB(t)
	prod, err := CreateExtKey(d, "prod", "", 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateExtKey(d, "prod", "", 0, 0, nil); !errors.Is(err, ErrExtKeyNameTaken) {
		t.Fatalf("duplicate create err=%v want ErrExtKeyNameTaken", err)
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
	if err := UpdateExtKey(d, other.ID, "prod", "", true, 0, 0, nil); !errors.Is(err, ErrExtKeyNameTaken) {
		t.Fatalf("rename onto taken name err=%v want ErrExtKeyNameTaken", err)
	}
	if got, _ := GetExtKeyByID(d, other.ID); got.Name != "other" {
		t.Fatalf("rejected rename must not persist: name=%q", got.Name)
	}
	// 改成空闲名、保留自己的名字、改成空名都放行
	if err := UpdateExtKey(d, other.ID, "other2", "", true, 0, 0, nil); err != nil {
		t.Fatalf("rename to free name: %v", err)
	}
	if err := UpdateExtKey(d, other.ID, "other2", "note", true, 0, 0, nil); err != nil {
		t.Fatalf("update keeping own name: %v", err)
	}
	if err := UpdateExtKey(d, other.ID, "", "", true, 0, 0, nil); err != nil {
		t.Fatalf("update to empty name: %v", err)
	}

	// 软删除后名称释放
	if err := DeleteExtKey(d, prod.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateExtKey(d, "prod", "", 0, 0, nil); err != nil {
		t.Fatalf("name should be free after soft delete: %v", err)
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
	if got.Name != "n" || got.Remark != "r" {
		t.Fatalf("name=%q remark=%q", got.Name, got.Remark)
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
