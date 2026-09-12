package model

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// conversation_records 应用层按月分表（仅 PG；SQLite 走单表路径，见
// conversation.go）。设计见 docs/conversation-sharding.md：
//
//   - 月分表 conversation_records_YYYY_MM，每月一张普通物理表；
//   - 存量库的旧表 conversation_records 原地保留为「历史分表」，零迁移；
//   - 写入按 created_at 月份路由，缺表时自动建（EnsureConversationShard）；
//   - 分表集合由进程内注册缓存维护（convShardCache）：启动时 LoadConvShards
//     从 catalog 全量加载，建表后立即注册，读写路径不再逐查询打 pg_tables；
//   - 全部分表共享序列 conversation_records_id_seq（存量库沿用旧表
//     BIGSERIAL 自带序列），id 全局唯一，详情查询 WHERE id=? 语义不变。
const (
	convBaseTable = "conversation_records"
	convSeqName   = "conversation_records_id_seq"
	convShardPfx  = "conversation_records_"
)

// convShardNameRe 校验月分表名。来自 catalog 或自身生成的表名必须过此
// 白名单才允许拼进动态 SQL，杜绝注入面。
var convShardNameRe = regexp.MustCompile(`^conversation_records_\d{4}_\d{2}$`)

// ConvShardName 返回 t 所属月份的分表名（本地时区，与全项目墙钟约定一致）。
func ConvShardName(t time.Time) string {
	return convShardPfx + t.Format("2006_01")
}

// convMonthKey 返回注册缓存用的月份键（"2006-01"）。
func convMonthKey(t time.Time) string { return t.Format("2006-01") }

// convShardNameToKey 把表名转回月份键；调用前需已过 convShardNameRe。
func convShardNameToKey(name string) string {
	return strings.Replace(name[len(convShardPfx):], "_", "-", 1)
}

// convShardCache 分表注册缓存（包级全局变量）。months 按新→旧排列
// （零填充月份保证表名字典序=时间序）；byMonth 以月份键索引表名；
// hasBase 记录历史分表是否存在。
//
// 注意：缓存是进程内的——多实例部署时，其他实例建的表要重启（或等本实例
// 写入路径惰性加载）后才出现在读路径里。当前部署形态为单实例，可接受。
var convShardCache struct {
	sync.RWMutex
	loaded  bool
	months  []string
	byMonth map[string]string
	hasBase bool
}

// LoadConvShards 从 catalog 全量重载分表注册缓存。仅 PG 调用；启动时调一次，
// 可重复调用（每次重载）——PG e2e 测试用它做 schema 隔离。
func LoadConvShards(d *sql.DB) error {
	rows, err := d.Query(`SELECT tablename FROM pg_tables
		WHERE schemaname = current_schema()
		  AND (tablename = 'conversation_records' OR tablename ~ '^conversation_records_\d{4}_\d{2}$')`)
	if err != nil {
		return fmt.Errorf("list conversation shards: %w", err)
	}
	defer rows.Close()
	var months []string
	byMonth := make(map[string]string)
	hasBase := false
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		if name == convBaseTable {
			hasBase = true
			continue
		}
		if !convShardNameRe.MatchString(name) {
			continue
		}
		months = append(months, name)
		byMonth[convShardNameToKey(name)] = name
	}
	if err := rows.Err(); err != nil {
		return err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(months)))
	convShardCache.Lock()
	convShardCache.months = months
	convShardCache.byMonth = byMonth
	convShardCache.hasBase = hasBase
	convShardCache.loaded = true
	convShardCache.Unlock()
	return nil
}

// convShardSnapshot 返回分表清单快照（月分表新→旧，历史分表若存在排最后）。
// 缓存未加载时惰性从 catalog 加载（兜底；正常路径启动时已加载）。
func convShardSnapshot(d *sql.DB) ([]string, error) {
	convShardCache.RLock()
	if convShardCache.loaded {
		out := append([]string(nil), convShardCache.months...)
		if convShardCache.hasBase {
			out = append(out, convBaseTable)
		}
		convShardCache.RUnlock()
		return out, nil
	}
	convShardCache.RUnlock()
	if err := LoadConvShards(d); err != nil {
		return nil, err
	}
	return convShardSnapshot(d)
}

// convShardForMonth 查月份键对应的分表名。
func convShardForMonth(key string) (string, bool) {
	convShardCache.RLock()
	name, ok := convShardCache.byMonth[key]
	convShardCache.RUnlock()
	return name, ok
}

// registerConvShard 把新分表注册进缓存，保持 months 新→旧有序。幂等。
func registerConvShard(key, name string) {
	convShardCache.Lock()
	defer convShardCache.Unlock()
	if convShardCache.byMonth == nil {
		convShardCache.byMonth = make(map[string]string)
	}
	if _, ok := convShardCache.byMonth[key]; ok {
		return
	}
	convShardCache.byMonth[key] = name
	// 二分找插入点：months 降序，插到第一个比 name 小的元素之前。
	i := sort.Search(len(convShardCache.months), func(i int) bool {
		return convShardCache.months[i] < name
	})
	convShardCache.months = append(convShardCache.months, "")
	copy(convShardCache.months[i+1:], convShardCache.months[i:])
	convShardCache.months[i] = name
}

// convShardDDL 生成一张月分表的完整 DDL（共享序列 + 建表 + 三个索引）。
// 索引名 schema 级唯一，故带月份后缀。全部幂等（IF NOT EXISTS）。
func convShardDDL(name string) []string {
	if !convShardNameRe.MatchString(name) {
		return nil
	}
	suffix := name[len(convShardPfx):]
	return []string{
		fmt.Sprintf(`CREATE SEQUENCE IF NOT EXISTS %s`, convSeqName),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
    id BIGINT NOT NULL DEFAULT nextval('%s') PRIMARY KEY,
    ext_key_id BIGINT,
    upstream_id BIGINT,
    upstream_name TEXT NOT NULL,
    model TEXT NOT NULL,
    in_format TEXT NOT NULL,
    up_format TEXT NOT NULL,
    harness TEXT NOT NULL,
    user_agent TEXT NOT NULL,
    stream INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'ok',
    prompt_tokens INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens INTEGER NOT NULL DEFAULT 0,
    cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
    reasoning_tokens INTEGER NOT NULL DEFAULT 0,
    request_ir JSONB NOT NULL DEFAULT '{}'::jsonb,
    response_ir JSONB NOT NULL DEFAULT '{}'::jsonb,
    request_raw BYTEA NOT NULL,
    response_raw BYTEA NOT NULL,
    created_at TIMESTAMP(0) NOT NULL DEFAULT CURRENT_TIMESTAMP
)`, name, convSeqName),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_conv_%s_created ON %s(created_at)`, suffix, name),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_conv_%s_ext_key ON %s(ext_key_id)`, suffix, name),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_conv_%s_harness ON %s(harness)`, suffix, name),
	}
}

// EnsureConversationShard 确保 t 所属月份的分表存在并注册进缓存。幂等。
// 仅 PG 调用；由启动流程（预建当月）与写入路径（缺表兜底）触发，
// 跨月自愈，无需定时任务。
func EnsureConversationShard(d *sql.DB, t time.Time) error {
	name := ConvShardName(t)
	stmts := convShardDDL(name)
	if stmts == nil {
		return fmt.Errorf("bad shard name %q", name)
	}
	for _, s := range stmts {
		if _, err := d.Exec(s); err != nil {
			return fmt.Errorf("ensure conversation shard %s: %w", name, err)
		}
	}
	registerConvShard(convMonthKey(t), name)
	return nil
}

// isUndefinedTable 判断 PG「表不存在」错误（SQLSTATE 42P01）。
func isUndefinedTable(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "42P01"
}

// convWindow 描述一页结果在某张分表上的截取范围。
type convWindow struct {
	shard  int // 分表在清单（新→旧）中的下标
	offset int // 该表内的偏移
	limit  int // 从该表取的行数
}

// convPageWindows 把全局 offset/size 映射到分表序列（counts 与清单同序，
// 新→旧）上：跳过整块不足 offset 的分表，返回本页命中的分表窗口，
// 按返回顺序拼接即得该页。深翻页只多几次空跳计数，不触碰无关分表数据。
func convPageWindows(counts []int, offset, size int) []convWindow {
	var ws []convWindow
	for i, n := range counts {
		if size <= 0 {
			break
		}
		if offset >= n {
			offset -= n
			continue
		}
		take := min(size, n-offset)
		ws = append(ws, convWindow{shard: i, offset: offset, limit: take})
		size -= take
		offset = 0
	}
	return ws
}
