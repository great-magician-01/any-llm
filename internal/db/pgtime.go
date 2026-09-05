package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// timestampLocalCodec 包装 pgtype.TimestampCodec：PostgreSQL `timestamp`
// （不带时区）列在 pgx 下写入时会丢弃时区只存墙钟（pgtype.discardTimeZone），
// 读回时却一律标成 UTC。本项目的写入方全部传服务器本地时间，因此扫描时把
// 墙钟从 UTC 标签重标为 time.Local，读写两侧才对得上：
// JSON 序列化会输出带本地偏移的 RFC3339（前端据此转换时区），
// time.Time.Unix()/time.Since 等也得到正确的绝对时刻。
type timestampLocalCodec struct{ pgtype.TimestampCodec }

func (c timestampLocalCodec) PlanScan(m *pgtype.Map, oid uint32, format int16, target any) pgtype.ScanPlan {
	inner := c.TimestampCodec.PlanScan(m, oid, format, target)
	if inner == nil {
		return nil
	}
	return timestampLocalScanPlan{inner: inner}
}

// timestampLocalScanPlan 先按内建 plan 扫描（结果标为 UTC 的墙钟），
// 再把墙钟原样重标为本地时区。
type timestampLocalScanPlan struct{ inner pgtype.ScanPlan }

func (p timestampLocalScanPlan) Scan(src []byte, dst any) error {
	// 经由临时 Timestamp 中转：内建 scan plan 只要求 dst 实现
	// TimestampScanner，临时值满足；扫描后统一重标再交付给原目标。
	var tmp pgtype.Timestamp
	if err := p.inner.Scan(src, &tmp); err != nil {
		return err
	}
	if tmp.Valid && tmp.InfinityModifier == pgtype.Finite {
		tmp.Time = relabelLocal(tmp.Time)
	}
	return dst.(pgtype.TimestampScanner).ScanTimestamp(tmp)
}

// relabelLocal 保留墙钟（年月日时分秒不变），仅把时区标签改为 time.Local。
func relabelLocal(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.Local)
}

// registerLocalTimestamp 在每条新 PG 连接上覆盖 `timestamp` 类型的 codec。
// 编码路径沿用内建实现（丢弃时区存墙钟），解码路径换成 timestampLocalCodec。
func registerLocalTimestamp(_ context.Context, conn *pgx.Conn) error {
	conn.TypeMap().RegisterType(&pgtype.Type{
		Name:  "timestamp",
		OID:   pgtype.TimestampOID,
		Codec: timestampLocalCodec{},
	})
	return nil
}
