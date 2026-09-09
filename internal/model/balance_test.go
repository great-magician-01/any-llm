package model

import (
	"encoding/json"
	"testing"
	"time"
)

func TestInsertBalanceSnapshot(t *testing.T) {
	d := testDB(t)
	s := &BalanceSnapshot{
		UpstreamID:   1,
		UpstreamName: "ds",
		Vendor:       "deepseek",
		Payload:      json.RawMessage(`{"kind":"balance","is_available":true,"balances":[{"currency":"CNY","total":"110.00","granted":"10.00","topped_up":"100.00"}]}`),
	}
	if err := InsertBalanceSnapshot(d, s); err != nil {
		t.Fatal(err)
	}
	if s.ID == 0 {
		t.Fatal("id not set after insert")
	}
	if s.CreatedAt.IsZero() {
		t.Fatal("created_at not filled")
	}
}

func TestLatestBalanceSnapshots(t *testing.T) {
	d := testDB(t)
	// Two snapshots for upstream 1 (id 2 is newer), one for upstream 2.
	mustInsert := func(s *BalanceSnapshot) {
		t.Helper()
		if err := InsertBalanceSnapshot(d, s); err != nil {
			t.Fatal(err)
		}
	}
	mustInsert(&BalanceSnapshot{UpstreamID: 1, UpstreamName: "ds", Vendor: "deepseek", Payload: json.RawMessage(`{"kind":"balance","is_available":false,"balances":[]}`)})
	time.Sleep(time.Millisecond) // ensure distinct created_at ordering by id anyway
	mustInsert(&BalanceSnapshot{UpstreamID: 1, UpstreamName: "ds", Vendor: "deepseek", Payload: json.RawMessage(`{"kind":"balance","is_available":true,"balances":[]}`)})
	mustInsert(&BalanceSnapshot{UpstreamID: 2, UpstreamName: "kimi", Vendor: "kimi-coding", Payload: json.RawMessage(`{"kind":"quota","windows":[]}`)})

	latest, err := LatestBalanceSnapshots(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(latest) != 2 {
		t.Fatalf("len=%d", len(latest))
	}
	byUpstream := map[int64]BalanceSnapshot{}
	for _, s := range latest {
		byUpstream[s.UpstreamID] = s
	}
	s1, ok := byUpstream[1]
	if !ok || s1.ID != 2 {
		t.Fatalf("upstream 1 latest=%+v ok=%v (want id=2)", s1, ok)
	}
	if s2, ok := byUpstream[2]; !ok || s2.Vendor != "kimi-coding" {
		t.Fatalf("upstream 2 latest=%+v ok=%v", s2, ok)
	}
}

func TestBalanceSnapshotsList(t *testing.T) {
	d := testDB(t)
	for i := 0; i < 5; i++ {
		if err := InsertBalanceSnapshot(d, &BalanceSnapshot{UpstreamID: 7, UpstreamName: "ds", Vendor: "deepseek", Payload: json.RawMessage(`{"kind":"balance"}`)}); err != nil {
			t.Fatal(err)
		}
	}
	// Another upstream's rows must not leak in.
	if err := InsertBalanceSnapshot(d, &BalanceSnapshot{UpstreamID: 8, UpstreamName: "kimi", Vendor: "kimi-coding", Payload: json.RawMessage(`{"kind":"quota"}`)}); err != nil {
		t.Fatal(err)
	}

	rows, total, err := BalanceSnapshotsList(d, 7, 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 || len(rows) != 3 {
		t.Fatalf("total=%d len=%d", total, len(rows))
	}
	// Newest first.
	if rows[0].ID < rows[1].ID {
		t.Fatalf("not ordered by id DESC: %d then %d", rows[0].ID, rows[1].ID)
	}
	// Second page.
	rows2, total2, err := BalanceSnapshotsList(d, 7, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	if total2 != 5 || len(rows2) != 2 {
		t.Fatalf("page2 total=%d len=%d", total2, len(rows2))
	}
	// Clamping: invalid page/size fall back to defaults.
	rows3, _, err := BalanceSnapshotsList(d, 7, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows3) != 5 {
		t.Fatalf("clamped len=%d", len(rows3))
	}
}
