package logger

import (
	"encoding"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/great-magician-01/logwriter"
)

var fixtureTime = time.Date(2026, 11, 7, 12, 34, 56, int(789*time.Millisecond), time.Local)

func formatEntry(t *testing.T, color bool, e logwriter.Entry) string {
	t.Helper()
	out, err := entryFormatter(color)(e)
	if err != nil {
		t.Fatalf("format: %v", err)
	}
	return string(out)
}

// TestFormatterLayout 锁定整体布局：时间戳 + 级别缩写 + 消息 + k=v 字段。
// 生产日志被 grep/采集依赖这个格式，改了会静默破坏下游。
func TestFormatterLayout(t *testing.T) {
	got := formatEntry(t, false, logwriter.Entry{
		Time: fixtureTime, Level: logwriter.InfoLevel, Message: "request",
		Fields: []logwriter.Field{
			{Key: "method", Value: "POST"},
			{Key: "status", Value: int64(201)},
		},
	})
	want := "2026-11-07 12:34:56.789 INF request method=POST status=201\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestFormatterLevelAbbrevs(t *testing.T) {
	cases := map[logwriter.Level]string{
		logwriter.DebugLevel: "DBG",
		logwriter.InfoLevel:  "INF",
		logwriter.WarnLevel:  "WRN",
		logwriter.ErrorLevel: "ERR",
	}
	for lvl, abbrev := range cases {
		got := formatEntry(t, false, logwriter.Entry{Time: fixtureTime, Level: lvl, Message: "m"})
		if !strings.Contains(got, " "+abbrev+" ") {
			t.Errorf("level %v: got %q want abbrev %q", lvl, got, abbrev)
		}
	}
}

func TestFormatterColors(t *testing.T) {
	got := formatEntry(t, true, logwriter.Entry{
		Time: fixtureTime, Level: logwriter.WarnLevel, Message: "hot",
	})
	want := "\033[90m2026-11-07 12:34:56.789\033[0m \033[33mWRN\033[0m hot\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestFieldValueRendering(t *testing.T) {
	day := time.Date(2026, 11, 7, 8, 9, 10, 0, time.Local)
	textMarshaler := marshalTextValue{"raw-bytes"}
	cases := []struct {
		name string
		f    logwriter.Field
		want string
	}{
		{"string plain", logwriter.Field{Key: "k", Value: "v"}, " k=v"},
		{"string quoted", logwriter.Field{Key: "k", Value: "a b"}, ` k="a b"`},
		{"string unicode", logwriter.Field{Key: "k", Value: "电"}, ` k="电"`},
		{"int64", logwriter.Field{Key: "n", Value: int64(-3)}, " n=-3"},
		{"uint64", logwriter.Field{Key: "n", Value: uint64(7)}, " n=7"},
		{"float64", logwriter.Field{Key: "f", Value: 1.25}, " f=1.25"},
		{"bool", logwriter.Field{Key: "ok", Value: true}, " ok=true"},
		{"time", logwriter.Field{Key: "t", Value: day}, " t=2026-11-07 08:09:10"},
		{"duration", logwriter.Field{Key: "d", Value: 1500 * time.Millisecond}, " d=1.5s"},
		{"dur ms", logwriter.Field{Key: "duration_ms", Value: int64(500)}, " dur=500ms"},
		{"dur seconds", logwriter.Field{Key: "duration_ms", Value: int64(1500)}, " dur=1.500s"},
		{"dur not int", logwriter.Field{Key: "duration_ms", Value: "500"}, " duration_ms=500"},
		{"error", logwriter.Field{Key: "err", Value: errors.New("boom")}, " err=boom"},
		{"nil", logwriter.Field{Key: "err", Value: nil}, " err=<nil>"},
		// KindAny 统一经 fmt.Sprint 语义渲染（与旧 slog handler 一致）：
		// []byte 是数字列表——含换行的字节串不会拆行注入；只实现
		// MarshalText 的 struct 走反射花括号而非 MarshalText 文本；struct
		// 是 %v 空格分隔而非 %+v 字段名。
		{"text marshaler", logwriter.Field{Key: "tm", Value: textMarshaler}, " tm={raw-bytes}"},
		{"bytes", logwriter.Field{Key: "b", Value: []byte("raw")}, " b=[114 97 119]"},
		{"struct", logwriter.Field{Key: "s", Value: struct{ A int }{1}}, " s={1}"},
	}
	for _, c := range cases {
		got := formatEntry(t, false, logwriter.Entry{Time: fixtureTime, Level: logwriter.InfoLevel, Fields: []logwriter.Field{c.f}})
		// 空消息后面保留分隔空格，字段再自带一个前导空格——与旧实现一致。
		want := "2026-11-07 12:34:56.789 INF " + c.want + "\n"
		if got != want {
			t.Errorf("%s: got %q want %q", c.name, got, want)
		}
	}
}

type marshalTextValue struct{ s string }

func (m marshalTextValue) MarshalText() ([]byte, error) { return []byte(m.s), nil }

var _ encoding.TextMarshaler = marshalTextValue{}
