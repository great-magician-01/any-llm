package logger

import (
	"fmt"
	"strings"
	"time"

	"github.com/great-magician-01/logwriter"
)

const logTimeFormat = "2006-01-02 15:04:05.000"

// entryFormatter 把 logwriter 条目渲染成与旧 slog handler 逐字节相同的格式：
// "2006-01-02 15:04:05.000 INF msg k=v ..."。color 为 true 时给时间戳和级别
// 缩写加 ANSI 色（仅控制台输出），文件输出保持纯文本。
//
// 它运行在 logwriter 的后台消费者里，请求热路径不做任何格式化。
func entryFormatter(color bool) logwriter.FormatterFunc {
	return func(e logwriter.Entry) ([]byte, error) {
		var b strings.Builder

		if color {
			b.WriteString(colorGray)
		}
		b.WriteString(e.Time.Format(logTimeFormat))
		if color {
			b.WriteString(colorReset)
		}
		b.WriteByte(' ')

		if color {
			b.WriteString(levelColor(e.Level))
		}
		b.WriteString(levelAbbrev(e.Level))
		if color {
			b.WriteString(colorReset)
		}

		b.WriteByte(' ')
		b.WriteString(e.Message)
		for _, f := range e.Fields {
			appendField(&b, f)
		}
		b.WriteByte('\n')
		return []byte(b.String()), nil
	}
}

func levelAbbrev(l logwriter.Level) string {
	switch l {
	case logwriter.ErrorLevel:
		return "ERR"
	case logwriter.WarnLevel:
		return "WRN"
	case logwriter.InfoLevel:
		return "INF"
	default:
		return "DBG"
	}
}

func levelColor(l logwriter.Level) string {
	switch l {
	case logwriter.ErrorLevel:
		return "\033[31m"
	case logwriter.WarnLevel:
		return "\033[33m"
	case logwriter.InfoLevel:
		return "\033[32m"
	default:
		return "\033[90m"
	}
}

const colorReset = "\033[0m"
const colorGray = "\033[90m"

// appendField 追加 " key=value"。duration_ms（int64）沿用旧 handler 的特殊
// 渲染：dur=500ms / dur=1.500s，比原始毫秒数更易读。
func appendField(b *strings.Builder, f logwriter.Field) {
	if f.Key == "duration_ms" {
		if ms, ok := f.Value.(int64); ok {
			if ms >= 1000 {
				fmt.Fprintf(b, " dur=%.3fs", float64(ms)/1000.0)
			} else {
				fmt.Fprintf(b, " dur=%dms", ms)
			}
			return
		}
	}

	b.WriteByte(' ')
	b.WriteString(f.Key)
	b.WriteByte('=')

	switch v := f.Value.(type) {
	case string:
		if needQuote(v) {
			fmt.Fprintf(b, "%q", v)
		} else {
			b.WriteString(v)
		}
	case int64:
		fmt.Fprintf(b, "%d", v)
	case uint64:
		fmt.Fprintf(b, "%d", v)
	case float64:
		fmt.Fprintf(b, "%g", v)
	case bool:
		fmt.Fprintf(b, "%v", v)
	case time.Time:
		b.WriteString(v.Format("2006-01-02 15:04:05"))
	case time.Duration:
		b.WriteString(v.String())
	default:
		// 与旧实现的 slog KindAny 渲染保持一致（Value.String() = fmt.Sprint）：
		// error/Stringer、TextMarshaler、[]byte 数字列表（含换行的字节串不会
		// 拆行注入伪造日志）等一律交给 fmt 的既有语义，方法 panic 也由 fmt
		// 兜住（typed-nil 的 MarshalText 在这里安全降级而不是炸掉消费者协程、
		// 丢掉整条记录）。不要在这里手写类型分支绕开 fmt。
		fmt.Fprint(b, v)
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
