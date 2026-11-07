package logger

import (
	"context"
	"log/slog"

	"github.com/great-magician-01/logwriter"
)

// handler 把 slog 记录转换成 logwriter 条目并入队。attrs 转换（含分组拍平）
// 在调用方 goroutine 完成，格式化与磁盘写入都由 logwriter 的单个后台消费者
// 串行执行——请求热路径只剩字段拍平和一次 channel 发送。
//
// lw 为 nil 时（Init 之前的初始状态、或 Close 之后）记录被直接丢弃。
type handler struct {
	lw       *logwriter.Logger
	level    slog.Level
	attrs    []logwriter.Field // With(...) 预置的、已按分组前缀拍平的字段
	group    string            // 累积的分组前缀（WithGroup 链接成 "a.b"）
	fileOnly bool              // true 时附加 FileOnly 标记，logwriter 只写文件
}

func newHandler(lw *logwriter.Logger, level slog.Level, attrs []logwriter.Field, group string, fileOnly bool) *handler {
	return &handler{lw: lw, level: level, attrs: attrs, group: group, fileOnly: fileOnly}
}

// withFileOnly 返回同配置、但只写文件的 handler（FileOnly() 的底座）。
func (h *handler) withFileOnly() *handler {
	c := *h
	c.fileOnly = true
	return &c
}

func (h *handler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *handler) Handle(_ context.Context, r slog.Record) error {
	if h.lw == nil {
		return nil
	}
	fields := make([]logwriter.Field, 0, len(h.attrs)+r.NumAttrs()+1)
	fields = append(fields, h.attrs...)
	r.Attrs(func(a slog.Attr) bool {
		fields = flattenAttr(fields, h.group, a)
		return true
	})
	if h.fileOnly {
		fields = append(fields, logwriter.FileOnly())
	}
	return h.lw.Log(toLogwriterLevel(r.Level), r.Message, fields...)
}

// flattenAttr 把一个 slog 属性拍平成 logwriter 字段。prefix 是累积分组前缀；
// slog.KindGroup（logger.With("g", slog.Group(...)) 风格）递归展开成
// "g.k=v"。空属性按 slog 约定跳过。
func flattenAttr(fields []logwriter.Field, prefix string, a slog.Attr) []logwriter.Field {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return fields
	}
	key := a.Key
	if prefix != "" {
		key = prefix + "." + key
	}
	if a.Value.Kind() == slog.KindGroup {
		for _, ga := range a.Value.Group() {
			fields = flattenAttr(fields, key, ga)
		}
		return fields
	}
	return append(fields, logwriter.Field{Key: key, Value: a.Value.Any()})
}

// toLogwriterLevel 把 slog 级别映射到 logwriter 的四档。非标准档位
// （如 Warn+1）按区间归入下一档，与旧 handler 的级别缩写渲染一致。
func toLogwriterLevel(l slog.Level) logwriter.Level {
	switch {
	case l >= slog.LevelError:
		return logwriter.ErrorLevel
	case l >= slog.LevelWarn:
		return logwriter.WarnLevel
	case l >= slog.LevelInfo:
		return logwriter.InfoLevel
	default:
		return logwriter.DebugLevel
	}
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	n := make([]logwriter.Field, len(h.attrs), len(h.attrs)+len(attrs))
	copy(n, h.attrs)
	for _, a := range attrs {
		n = flattenAttr(n, h.group, a)
	}
	return &handler{lw: h.lw, level: h.level, attrs: n, group: h.group, fileOnly: h.fileOnly}
}

func (h *handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	g := name
	if h.group != "" {
		g = h.group + "." + name
	}
	return &handler{lw: h.lw, level: h.level, attrs: h.attrs, group: g, fileOnly: h.fileOnly}
}
