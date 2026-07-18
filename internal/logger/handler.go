package logger

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
)

func levelAbbrev(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "ERR"
	case l >= slog.LevelWarn:
		return "WRN"
	case l >= slog.LevelInfo:
		return "INF"
	default:
		return "DBG"
	}
}

func levelColor(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "\033[31m"
	case l >= slog.LevelWarn:
		return "\033[33m"
	case l >= slog.LevelInfo:
		return "\033[32m"
	default:
		return "\033[90m"
	}
}

const colorReset = "\033[0m"
const colorGray = "\033[90m"

type output struct {
	w     io.Writer
	color bool
}

type handler struct {
	level      slog.Level
	mu         sync.Mutex
	outputs    []output
	preAttrs   []slog.Attr
	group      string
}

func newHandler(level slog.Level, outputs []output, preAttrs []slog.Attr, group string) *handler {
	return &handler{
		level:    level,
		outputs:  outputs,
		preAttrs: preAttrs,
		group:    group,
	}
}

func (h *handler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *handler) Handle(_ context.Context, r slog.Record) error {
	var msgBuf bytes.Buffer

	timestr := r.Time.Format("2006-01-02 15:04:05.000")

	msgBuf.WriteString(r.Message)

	var attrBuf bytes.Buffer
	for _, a := range h.preAttrs {
		appendAttr(&attrBuf, a)
	}
	r.Attrs(func(a slog.Attr) bool {
		appendAttr(&attrBuf, a)
		return true
	})
	attrBytes := attrBuf.Bytes()

	for _, out := range h.outputs {
		var buf bytes.Buffer

		if out.color {
			buf.WriteString(colorGray)
			buf.WriteString(timestr)
			buf.WriteString(colorReset)
		} else {
			buf.WriteString(timestr)
		}
		buf.WriteByte(' ')

		if out.color {
			buf.WriteString(levelColor(r.Level))
			buf.WriteString(levelAbbrev(r.Level))
			buf.WriteString(colorReset)
		} else {
			buf.WriteString(levelAbbrev(r.Level))
		}

		buf.WriteByte(' ')
		buf.Write(msgBuf.Bytes())
		if len(attrBytes) > 0 {
			buf.WriteByte(' ')
			buf.Write(attrBytes[1:])
		}
		buf.WriteByte('\n')

		h.mu.Lock()
		out.w.Write(buf.Bytes())
		h.mu.Unlock()
	}
	return nil
}

func appendAttr(buf *bytes.Buffer, a slog.Attr) {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return
	}
	key := a.Key

	if key == "duration_ms" && a.Value.Kind() == slog.KindInt64 {
		ms := a.Value.Int64()
		if ms >= 1000 {
			fmt.Fprintf(buf, " dur=%.3fs", float64(ms)/1000.0)
		} else {
			fmt.Fprintf(buf, " dur=%dms", ms)
		}
		return
	}

	buf.WriteByte(' ')
	buf.WriteString(key)
	buf.WriteByte('=')

	switch a.Value.Kind() {
	case slog.KindString:
		s := a.Value.String()
		if needQuote(s) {
			fmt.Fprintf(buf, "%q", s)
		} else {
			buf.WriteString(s)
		}
	case slog.KindInt64:
		fmt.Fprintf(buf, "%d", a.Value.Int64())
	case slog.KindUint64:
		fmt.Fprintf(buf, "%d", a.Value.Uint64())
	case slog.KindFloat64:
		fmt.Fprintf(buf, "%g", a.Value.Float64())
	case slog.KindBool:
		fmt.Fprintf(buf, "%v", a.Value.Bool())
	case slog.KindTime:
		buf.WriteString(a.Value.Time().Format("2006-01-02 15:04:05"))
	case slog.KindDuration:
		buf.WriteString(a.Value.Duration().String())
	case slog.KindGroup:
		for _, ga := range a.Value.Group() {
			sub := slog.Attr{Key: key + "." + ga.Key, Value: ga.Value}
			appendAttr(buf, sub)
		}
	default:
		buf.WriteString(a.Value.String())
	}
}

func needQuote(s string) bool {
	for _, c := range s {
		if c <= ' ' || c == '=' || c == '"' || c == '\\' || c >= 127 {
			return true
		}
	}
	return false
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newAttrs := make([]slog.Attr, 0, len(h.preAttrs)+len(attrs))
	newAttrs = append(newAttrs, h.preAttrs...)
	for _, a := range attrs {
		if h.group != "" {
			a.Key = h.group + "." + a.Key
		}
		newAttrs = append(newAttrs, a)
	}
	return &handler{
		level:    h.level,
		outputs:  h.outputs,
		preAttrs: newAttrs,
	}
}

func (h *handler) WithGroup(name string) slog.Handler {
	return &handler{
		level:    h.level,
		outputs:  h.outputs,
		preAttrs: h.preAttrs,
		group:    name,
	}
}
