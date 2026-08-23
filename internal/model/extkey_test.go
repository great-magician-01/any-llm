package model

import (
	"strings"
	"testing"
)

func TestCreateExtKeyFormat(t *testing.T) {
	d := testDB(t)
	k, err := CreateExtKey(d, "test-label", 0, 0)
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
	if !k.Enabled {
		t.Fatal("should be enabled")
	}
}

func TestCreateExtKeysUnique(t *testing.T) {
	d := testDB(t)
	k1, _ := CreateExtKey(d, "a", 0, 0)
	k2, _ := CreateExtKey(d, "b", 0, 0)
	if k1.Key == k2.Key {
		t.Fatal("duplicate keys generated")
	}
}

func TestGetExtKey(t *testing.T) {
	d := testDB(t)
	k, _ := CreateExtKey(d, "l", 0, 0)
	got, err := GetExtKey(d, k.Key)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != k.ID {
		t.Fatalf("id=%d want %d", got.ID, k.ID)
	}
	_, err = GetExtKey(d, "all-sk-nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent key")
	}
}

func TestListExtKeysFullKey(t *testing.T) {
	d := testDB(t)
	k, _ := CreateExtKey(d, "l", 0, 0)
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
	k, _ := CreateExtKey(d, "l", 0, 0)
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
	k, _ := CreateExtKey(d, "l", 0, 0)
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
	k, _ := CreateExtKey(d, "l", 0, 0)

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
