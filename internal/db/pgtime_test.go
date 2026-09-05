package db

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// newLocalTimestampMap 模拟 registerLocalTimestamp 在连接上的注册结果。
func newLocalTimestampMap() *pgtype.Map {
	m := pgtype.NewMap()
	m.RegisterType(&pgtype.Type{
		Name:  "timestamp",
		OID:   pgtype.TimestampOID,
		Codec: timestampLocalCodec{},
	})
	return m
}

func encodeTimestamp(t *testing.T, m *pgtype.Map, format int16, ts pgtype.Timestamp) []byte {
	t.Helper()
	plan := m.PlanEncode(pgtype.TimestampOID, format, ts)
	if plan == nil {
		t.Fatalf("no encode plan for format %d", format)
	}
	buf, err := plan.Encode(ts, nil)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf
}

func scanTimestamp(t *testing.T, m *pgtype.Map, format int16, buf []byte) pgtype.Timestamp {
	t.Helper()
	var ts pgtype.Timestamp
	plan := m.PlanScan(pgtype.TimestampOID, format, &ts)
	if plan == nil {
		t.Fatalf("no scan plan for format %d", format)
	}
	if err := plan.Scan(buf, &ts); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return ts
}

// PG 往返：本地时间写入 timestamp 列（存墙钟、丢弃时区），读回必须标为
// 本地时区且墙钟不变 —— 这样 JSON 输出带本地偏移，前端时区转换才正确。
func TestTimestampLocalCodecRoundTrip(t *testing.T) {
	m := newLocalTimestampMap()
	written := time.Date(2026, 9, 4, 22, 11, 12, 123456000, time.Local)

	for _, format := range []int16{pgtype.BinaryFormatCode, pgtype.TextFormatCode} {
		buf := encodeTimestamp(t, m, format, pgtype.Timestamp{Time: written, Valid: true})
		got := scanTimestamp(t, m, format, buf)
		if !got.Valid {
			t.Fatalf("format %d: invalid after round trip", format)
		}
		if got.Time.Location() != time.Local {
			t.Fatalf("format %d: location = %v, want Local", format, got.Time.Location())
		}
		gc, wc := got.Time, written
		if gc.Year() != wc.Year() || gc.Month() != wc.Month() || gc.Day() != wc.Day() ||
			gc.Hour() != wc.Hour() || gc.Minute() != wc.Minute() || gc.Second() != wc.Second() {
			t.Fatalf("format %d: wall clock %v, want %v", format, gc, wc)
		}
		// 同一墙钟 + 同一时区标签 => 与写入值是同一绝对时刻。
		if !got.Time.Equal(written) {
			t.Fatalf("format %d: instant %v != written %v", format, got.Time, written)
		}
	}
}

// 编码路径保持内建行为：本地时间存为墙钟（等价于重标为 UTC 后再编码）。
func TestTimestampLocalCodecEncodeUnchanged(t *testing.T) {
	m := newLocalTimestampMap()
	local := time.Date(2026, 9, 4, 22, 11, 12, 123456000, time.Local)
	asUTCWallClock := time.Date(2026, 9, 4, 22, 11, 12, 123456000, time.UTC)

	for _, format := range []int16{pgtype.BinaryFormatCode, pgtype.TextFormatCode} {
		gotBuf := encodeTimestamp(t, m, format, pgtype.Timestamp{Time: local, Valid: true})
		wantBuf := encodeTimestamp(t, m, format, pgtype.Timestamp{Time: asUTCWallClock, Valid: true})
		if string(gotBuf) != string(wantBuf) {
			t.Fatalf("format %d: encode %q != wall-clock encode %q", format, gotBuf, wantBuf)
		}
	}
}

// NULL 与 infinity 原样透传，不做时区重标定。
func TestTimestampLocalCodecSpecialValues(t *testing.T) {
	m := newLocalTimestampMap()

	for _, format := range []int16{pgtype.BinaryFormatCode, pgtype.TextFormatCode} {
		// NULL（database/sql 不会对 nil 列调 valueFunc，但 plan 本身要容忍）
		got := scanTimestamp(t, m, format, nil)
		if got.Valid {
			t.Fatalf("format %d: null src should scan invalid", format)
		}

		buf := encodeTimestamp(t, m, format, pgtype.Timestamp{Valid: true, InfinityModifier: pgtype.Infinity})
		got = scanTimestamp(t, m, format, buf)
		if !got.Valid || got.InfinityModifier != pgtype.Infinity {
			t.Fatalf("format %d: infinity round trip got %+v", format, got)
		}
	}
}

func TestRelabelLocal(t *testing.T) {
	utc := time.Date(2026, 9, 4, 22, 11, 12, 789000000, time.UTC)
	got := relabelLocal(utc)
	if got.Location() != time.Local {
		t.Fatalf("location = %v, want Local", got.Location())
	}
	if got.Hour() != 22 || got.Minute() != 11 || got.Second() != 12 || got.Nanosecond() != 789000000 {
		t.Fatalf("wall clock changed: %v", got)
	}
}
