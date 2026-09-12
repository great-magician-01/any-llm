# 对话记录分表设计（conversation_records 应用层按月分表）

> 状态：讨论稿 v2（应用层分表）。分支 `feat/conv-records-partition`，未经确认前不实施。
> 关联问题：对话归档表数据量过大导致查询卡；对话记录页只能看第一页（后者为前端 bug，与本设计无关，见文末附注）。

## 1. 背景与目标

`conversation_records` 是网关的对话归档表，**仅 PostgreSQL 建表**（SQLite 无此表，网关层门控）。每行包含元数据 + `request_ir`/`response_ir` JSONB + `request_raw`/`response_raw` BYTEA（响应上限 64 MiB）。只增不减、无清理任务、单行体积大。

**目标**：按时间对这张表分表，使查询/维护只触及相关时间段；**完全兼容存量数据**；**分表逻辑全部在应用层**（Go 代码负责路由与合并，不使用 PG 分区等数据库侧特性，不把业务下放数据库）。

## 2. 现状（读代码结论）

全表只有 4 条 SQL，都在 `internal/model/conversation.go`：

| 操作 | SQL 形态 | 备注 |
|---|---|---|
| 写入 | `INSERT INTO conversation_records (...) VALUES (...)` | 经 `db.Writer` 异步队列；失败即丢，无重试语义 |
| 计数 | `SELECT COUNT(*) FROM conversation_records` | 列表页 total |
| 列表 | `SELECT <19 个元数据列> ... ORDER BY id DESC LIMIT ? OFFSET ?` | 默认 size 50，上限 200 |
| 详情 | `SELECT <元数据列>, request_ir, response_ir ... WHERE id = ?` | raw 字节列永不返回 API |

无外键、无软删除、无 UPDATE/DELETE、无聚合查询。迁移机制为启动时幂等重放 DDL（`internal/db/db.go:92-103`）。

## 3. 方案概览

- **按月分表**：`conversation_records_YYYY_MM`（如 `conversation_records_2026_09`），每月一张普通物理表。
- **存量表原地不动**：现有 `conversation_records` 保留全部历史数据，作为"历史分表"参与读取；**零迁移、零数据搬迁、零改名**。上线即生效，旧数据无感。
- **写入按 `created_at` 月份路由**到对应月分表；表不存在时自动创建（应用层负责建表）。
- **分表注册缓存**：进程内全局变量（`internal/model` 包级，RWMutex 保护）。启动时从 catalog 一次性加载；每次建表后立即注册进缓存；读写路径只查缓存，不再每次打 `pg_tables`。
- **读取由应用层合并**：列表按分表块新→旧翻页拼接；详情跨分表按 id 查。
- **id 全局唯一**：所有分表共用一个序列（`conversation_records_id_seq`，存量库沿用旧表 BIGSERIAL 自带序列），`WHERE id = ?` 语义不变。

## 4. 详细设计

### 4.1 分表规则与命名

- 表名：`conversation_records_YYYY_MM`，月份取**服务器本地时区**（与全项目"时间戳一律本地墙钟"约定一致，见 `internal/db/pgtime.go`）。
- 分表集合 = { 各月分表 } ∪ { `conversation_records`（历史分表，若存在）}。
- 排序规则（新→旧）：月分表按表名**降序**（零填充月份保证字典序 = 时间序），历史分表永远排最后。纯字符串排序即可，无需解析。

### 4.2 分表结构

每张月分表是独立普通表，列定义与原表完全一致，两处差异：

```sql
CREATE TABLE IF NOT EXISTS conversation_records_2026_09 (
    id BIGINT NOT NULL DEFAULT nextval('conversation_records_id_seq') PRIMARY KEY,
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
);
CREATE INDEX IF NOT EXISTS idx_conv_2026_09_created  ON conversation_records_2026_09(created_at);
CREATE INDEX IF NOT EXISTS idx_conv_2026_09_ext_key  ON conversation_records_2026_09(ext_key_id);
CREATE INDEX IF NOT EXISTS idx_conv_2026_09_harness  ON conversation_records_2026_09(harness);
```

- **id 保留主键**（普通表无分区键限制），默认值指向共享序列 —— 详情查询 `WHERE id=?` 在每张分表上都走主键索引。
- 共享序列：建表前 `CREATE SEQUENCE IF NOT EXISTS conversation_records_id_seq`（存量库该序列已存在且归旧表所有，直接复用；全新库则新建）。
- 索引名带月份后缀（索引名 schema 级唯一）。
- 全新库：`migrationPG` 中**移除**原 `conversation_records` 建表 SQL，新库只有月分表、不再有 base 表。存量库的 base 表自动成为历史分表，无需任何操作。

### 4.3 写入路由

`InsertConversation` 改动：

1. 由 `r.CreatedAt`（缺省 `time.Now()`）算出月份键（`YYYY-MM`）；
2. **查注册缓存**拿到月分表名；命中 → 直接 `INSERT`；
3. 未命中 → 调 `EnsureConversationShard(d, month)` 建表并**注册进缓存**，然后 `INSERT`；若 `INSERT` 仍报 `undefined_table`（`42P01`，例如缓存刚加载后表被外部 drop）→ 重建一次再重试。

`db.Writer` 单 goroutine 串行写，进程内无建表竞态；`CREATE TABLE IF NOT EXISTS` + 重试对多实例竞态也容错。`main.go` 启动时（仅 PG）先 `LoadConvShards(d)` 加载缓存、再 `EnsureConversationShard(d, time.Now())` 预建当月分表，让常见路径不走建表分支。跨月由写入路径自愈，**不需要额外定时任务**。

### 4.4 分表注册缓存

进程内全局变量（`internal/model` 包级），结构：

```go
// months 按新→旧排列；byMonth 以 "2006-01" 月份键索引表名；
// hasBase 记录历史分表 conversation_records 是否存在。
var convShards struct {
    sync.RWMutex
    loaded  bool
    months  []string
    byMonth map[string]string
    hasBase bool
}
```

- **启动加载**：`LoadConvShards(d)` 从 catalog 一次性读出全部匹配表并填充缓存（可重复调用，每次全量重载——测试用它做 schema 隔离）：
  ```sql
  SELECT tablename FROM pg_tables
  WHERE schemaname = current_schema()
    AND (tablename = 'conversation_records' OR tablename ~ '^conversation_records_\d{4}_\d{2}$')
  ```
- **建表注册**：`EnsureConversationShard` 成功后把新表名插入缓存（保持 months 新→旧有序）。
- **惰性兜底**：读路径发现缓存未加载（如漏调启动加载）时自动从 catalog 加载一次再使用。
- 读路径拿到的表名一律再经白名单正则校验后才拼进动态 SQL（表名来自 catalog/自身生成，无注入面）。
- 表名零填充月份保证字典序 = 时间序：月分表名直接倒序排列、base 追加在最后即得新→旧顺序。

### 4.5 列表分页（核心逻辑，应用层合并）

全局顺序 = 分表块顺序（新→旧），块内 `ORDER BY created_at DESC, id DESC`（秒级精度下同秒需 id 决胜，保证翻页稳定）。因为新写入只进月分表、历史分表只读，分块顺序与全局时间序一致。

算法（`ConversationRecordsList` 重写）：

1. 从**注册缓存**取分表快照（新→旧）；
2. 一条 `UNION ALL` 取各分表行数：`SELECT 't1', COUNT(*) FROM t1 UNION ALL SELECT 't2', COUNT(*) FROM t2 ...`，求和得 total；
3. 纯函数 `convPageWindows(counts, offset, size)`：从头累计行数，跳过整块不足 offset 的分表，得出本页落在哪 1~2（极少数更多）张分表上、各自的 `LIMIT/OFFSET`；
4. 只对命中的分表发查询，按顺序拼接结果。

效果：翻页只触碰相关分表 + 各分表一次索引计数；深翻页（大 offset）也只多几次空跳计数，不会对全量数据排序。

### 4.6 详情查询

```sql
SELECT <cols>, request_ir, response_ir FROM conversation_records_2026_09 WHERE id = ?
UNION ALL
SELECT <cols>, request_ir, response_ir FROM conversation_records       WHERE id = ?
```

每个分支独立带 `WHERE id = ?`（显式下推，各自走主键索引），id 全局唯一 → 0 或 1 行，`QueryRow` 语义不变。

### 4.7 SQLite 与测试路径

- 分表机制仅 PG 启用（`db.DialectOf` 门控）。SQLite 下 `InsertConversation` / 列表 / 详情保持**旧的单表逻辑**（该路径只被单测使用；生产上 handler 层已门控，SQLite 永不进这些函数）。
- 现有 `internal/model/conversation_test.go`（SQLite 内存库、手工建单表）继续有效，覆盖非分表逻辑；分表逻辑由 PG e2e 与纯函数单测覆盖。

### 4.8 代码落点

| 文件 | 改动 |
|---|---|
| `internal/model/conversation_shard.go`（新） | 分表命名/建表（`EnsureConversationShard`）/注册缓存（`LoadConvShards` + 包级变量）/`convPageWindows` 纯函数 |
| `internal/model/conversation_shard_test.go`（新） | 命名、正则、排序、分页窗口计算、DDL 生成、注册缓存行为的单测 |
| `internal/model/conversation.go` | `InsertConversation` 缓存查表+建表重试；`ConversationRecordsList` 分块分页；`GetConversation` 跨分表 UNION ALL；三者加 PG/SQLite 分支 |
| `internal/db/migrations.go` | `migrationPG` 移除 base 表建表 SQL（`migrateSoftDeletePG` 的 FK 兜底保留，对存量库仍生效） |
| `cmd/any-llm/main.go` | PG 时启动 `LoadConvShards` + `EnsureConversationShard`（失败记 warn，不阻断——插入路径会兜底重试） |
| `internal/gateway/pg_conv_e2e_test.go` | e2e 断言改查月分表/经 model 层（原断言直查 base 表） |
| `AGENTS.md` | conversations 条目补充分表说明 |

## 5. 边界情况

| 场景 | 行为 |
|---|---|
| 存量数据 | 原地不动，读取自动并入历史分表，管理页无感 |
| 全新部署 | 无 base 表，只有月分表；序列显式创建 |
| 跨月 | 当月第一条写入触发自动建表并注册进缓存（或启动时已预建） |
| 停机数月后重启 | 中间月份无数据、不建表；启动加载只发现真实存在的表，查询不报错 |
| 并发实例同时建表 | `IF NOT EXISTS` + 重试容错。**注意**：分表注册缓存是进程内的——多实例部署时，A 实例建的表 B 实例要重启（或等其写入路径惰性加载）才可见；当前部署形态为单实例，可接受 |
| 显式回填历史月份（仅测试场景） | 会建对应月分表；与 base 表同月数据的相对次序不再严格按时间（生产路径 `created_at` 恒为当前时间，不触发；注释说明） |
| 清理旧数据 | 直接 `DROP TABLE conversation_records_YYYY_MM`，秒级，无死行（是否加自动保留策略留作后续独立改动） |
| 回滚 | 旧表未动；删掉月分表、代码回退即可 |

## 6. 验证

1. 单测：`convPageWindows` 各窗口边界（页跨分表、offset 跳过整表、空表、深翻页）、命名/排序/正则、DDL 生成。
2. 现有 SQLite 单测保持绿（非分表路径回归）。
3. PG e2e（`DB_TEST_PG_DSN` 存在才跑）：旧结构 + 数据 → 启动新版 → 断言旧数据经列表/详情可见、新写入落当月分表、翻页跨「月分表/历史分表」拼接正确、二次启动幂等。本机无 docker，需用户 PG 环境执行。
4. 回归：`go test ./...`、`go vet ./...`（gofmt 检查先 strip `\r`）。
5. 现网验证：部署重启 → 发新请求 → 确认落 `conversation_records_当月`；管理页历史数据/详情/翻页正常。

## 7. 已确认的决策

1. 分表周期：**按月**（数据量不大，库配置弱）。
2. 实现方式：**应用层分表**（用户拍板，不使用 PG 原生分区）。
3. 存量表：原地保留为历史分表，零迁移。
4. 自动清理：本期不做（`DROP TABLE` 即可手动清理）。
5. id：全部分表共享序列，月分表各自保留 `PRIMARY KEY (id)`。
6. 分表发现：**进程内注册缓存**（启动加载 + 建表注册 + 惰性兜底），不逐查询打 catalog。

## 附注：对话记录页只能看一页（独立前端 bug）

与分表无关，根因：`n-data-table` 缺少 `remote` 属性，naive-ui 忽略传入的 `itemCount: total`，按当前页 20 行计算页数并把页码钳回第 1 页（已核对 `naive-ui/es/data-table/src/use-table-data.mjs`）。后端 `page`/`size`/`total` 链路正常。

修复：4 处表格各加 `remote` 属性——`Conversations.vue`、`glass/GlassConversations.vue`、`Usage.vue`（请求明细，同款 bug）、`glass/GlassUsage.vue`。
