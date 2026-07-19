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

func TestMaskKey(t *testing.T) {
	key := "all-sk-abcdefghijklmnopqrstuvwxyz123456"
	masked := MaskKey(key)
	if !strings.HasPrefix(masked, "all-sk-abcde") {
		t.Fatalf("prefix wrong: %q", masked)
	}
	if !strings.HasSuffix(masked, "3456") {
		t.Fatalf("suffix wrong: %q", masked)
	}
	if !strings.Contains(masked, "****") {
		t.Fatalf("no stars: %q", masked)
	}
}

func TestListExtKeysMasked(t *testing.T) {
	d := testDB(t)
	k, _ := CreateExtKey(d, "l", 0, 0)
	list, err := ListExtKeys(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("len=%d", len(list))
	}
	if list[0].Key == k.Key {
		t.Fatal("list returned unmasked key")
	}
	if !strings.HasPrefix(list[0].Key, "all-sk-") {
		t.Fatalf("masked key lost prefix: %q", list[0].Key)
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
