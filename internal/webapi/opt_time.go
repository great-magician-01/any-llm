package webapi

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// optTime 三态时间，用于「可空 + 缺省保留现状」的字段（如上游有效期）：
//   - set == false：请求里没这个字段，保持现状不动；
//   - set == true 且 t == nil：显式给出的空值，清除该字段；
//   - set == true 且 t != nil：设置为该时刻。
//
// 为什么不能只用 *time.Time：JSON 的 null 与字段缺省都解成 nil，两种语义
// 混为一谈。而上游的 PATCH 场景确实两种都要——toggleEnabled 只发
// {"enabled":true}（缺省必须保留有效期），表单清空日期选择器则要显式清除。
type optTime struct {
	set bool
	t   *time.Time
}

// UnmarshalJSON 里 set 一律置 true：能走到这里说明字段在请求体里出现过。
// null 与 "" 都算显式清除（前端日期选择器清空时两种都可能发出来）。
func (o *optTime) UnmarshalJSON(b []byte) error {
	o.set = true
	o.t = nil
	s := strings.TrimSpace(string(b))
	if s == "null" || s == `""` {
		return nil
	}
	var t time.Time
	if err := json.Unmarshal(b, &t); err != nil {
		return fmt.Errorf("want RFC3339 timestamp or null: %w", err)
	}
	// 统一换成服务器本地时间再落地。带 Z / 非本地偏移的输入按原位置解析，
	// 绝对时刻是对的；但 PG 的 TIMESTAMP(0) 写入时会丢掉时区只存墙钟
	// （见 internal/db/pgtime.go），墙钟与原位置不一致就会偏几个时区。
	// 转成 time.Local 后墙钟与本地一致，PG/SQLite 两条路径都正确。
	// 截到秒：SQLite 按写入的文本原样存取，带纳秒会存出
	// "...:00.123456789+08:00"，前端日期选择器按秒级格式回填会解析失败；
	// PG 的 TIMESTAMP(0) 本来也会截断，两侧口径统一。
	t = t.In(time.Local).Truncate(time.Second)
	o.t = &t
	return nil
}

// MarshalJSON 供配置导出使用（configUpstream 导出/导入共用同一结构体）。
func (o optTime) MarshalJSON() ([]byte, error) {
	if o.t == nil {
		return []byte("null"), nil
	}
	return json.Marshal(*o.t)
}

// value 取出时刻；仅在 set 为 true 时有意义。
func (o optTime) value() *time.Time { return o.t }
