# 会话视图（Conversation Sessions）设计

日期：2026-10-07
状态：已评审，实现中（分支 `feat/conversation-sessions`）

## 背景与目标

对话归档（`conversation_records` 月分表）是**按请求**记录的：多轮对话的每一轮
（甚至别名故障转移的每次候选尝试）各占一行，行与行之间没有任何关联字段。
浏览一个 agent 的多轮会话需要在列表里人工拼凑。

本设计新增**会话视图**：一张独立的聚合表 `conversation_sessions`（按月分表），
每行代表一个会话，随每轮请求 upsert（无则新增、有则修改）。管理端「对话记录」
页增加 tab：「按请求」（现有列表，原样保留）/「按会话」（新聚合视图）。

### 明确的非目标 / 约束

- **现有按轮记录完全不动**：`conversation_records` 的写入路径、schema、
  列表/详情 API、前端「按请求」tab 一行不改。会话表是叠加层，它的失败
  不影响主请求，也不影响按轮归档。
- **不替换写入模型**：按轮记录仍是审计/回放的事实来源；会话表面向浏览，
  是冗余的聚合副本。
- **没有会话 id 的流量不进会话表**：提取不到 id 时行为与现状完全一致
  （只有按轮记录）。
- SQLite 不建会话表（与按轮归档同门控：
  `Dialect.SupportsConversationArchive()`），API 返回 `disabled: true`。

## 会话 id 提取（`internal/gateway/convid.go`）

有序规则表（同 `detectHarness` 的风格），输入为请求头 + 入站格式 + IR 请求 +
responses 会话上下文。优先级从高到低，先中先得：

| # | 来源 | 规则 | 产出 id | 覆盖客户端 |
|---|------|------|---------|-----------|
| 1 | DSH 专用头 | `x-deepseek-harness-session-id` | `dsh:<值>` | deepseek-harness（llm-deepseek 路径，源码已确认：`packages/llm/llm-deepseek/src/adapter.ts` 每个会话请求都发送） |
| 2 | 会话亲和头 | `x-session-id` / `session_id` / `x-session-affinity`（按此顺序取第一个非空） | `hdr:<值>` | codex（`session_id` 头惯例）、deepseek-harness 的 pi-ai 路径（需 DSH 侧开启 `sendSessionAffinityHeaders`） |
| 3 | Responses 会话链 | 入站带 `previous_response_id=pid` → 在会话表按 `last_response_id = pid` 查所属会话；查不到/首轮 → 新会话 | `resp:<首轮respID>` | codex（responses 模式）及一切 `/v1/responses` 客户端 |
| 4 | Anthropic metadata | `metadata.user_id` 含 `session_` 子串 → 取其后的 uuid 段（claude-code 惯例 `user_xxx_account__session_<uuid>`） | `cc:<uuid>` | claude-code |
| 5 | 通用自定义头 | `x-conversation-id` | `hdr:<值>` | 其他可控客户端 |
| — | 均未命中 | 返回空，**不写会话表** | — | aider / cursor / continue / windsurf / gemini-cli 等 |

要点：

- id 一律加来源前缀（`dsh:`/`hdr:`/`resp:`/`cc:`），防止不同来源的值碰撞。
- 规则 3 依赖会话表上的 `last_response_id` 列：每轮 upsert 时把它更新为本轮
  的 `sess.respID`，续接请求借此找到所属会话（`resp:` id 固定为首轮 respID，
  整条链稳定）。
- 这些头会按现有 `copyForwardableHeaders` 行为透传给上游，无害，无需处理。
- 新增客户端支持 = 往规则表追加一条，不动其他代码。

### compaction（历史压缩）检测

- DSH 路径有显式信号：请求头 `x-deepseek-harness-compact: 1`。
- 通用兜底：`len(req.Messages) < 会话行已记录的 msg_count`（客户端重发的历史
  变短了）。
- 触发时任一种：当轮**全量**消息照常并入 `messages`，但在并入前插入一个边界
  标记块（`{"type":"text","text":"--- 历史已压缩 ---","role":"_marker"}` 形态的
  标记消息），链不断、内容不丢，`msg_count` 重置为当前请求消息数 +1（assistant
  输出）。

## 数据表：`conversation_sessions`（月分表）

物理表 `conversation_sessions_YYYY_MM`，按月分表，机制复用
`internal/store/conversation_shard.go` 的注册缓存/扇出/窗口分页模式
（泛化为参数化的 `shardRegistry`，`conversation_records` 与
`conversation_sessions` 各持一个实例）。SQLite 不建。

| 列 | 类型 | 说明 |
|----|------|------|
| `id` | BIGINT PK | 全局唯一；PG 用新序列 `conversation_sessions_id_seq`，MySQL 复用 `id_sequences` 计数器（`db.NextShardID`） |
| `session_id` | VARCHAR(255) NOT NULL | 带前缀的会话 id；**每张分表内唯一**（UNIQUE），跨分表唯一由写入路径「先查后插」保证 |
| `last_response_id` | VARCHAR(255) NULL | responses 链最近的 respID；普通索引，规则 3 定位用 |
| `ext_key_id` | BIGINT NULL | 最后使用的 key |
| `harness` | VARCHAR(64) | 规范客户端名（同按轮记录） |
| `in_format` | VARCHAR(32) | 入站格式 |
| `model` | VARCHAR(512) | 最后使用的实际上游模型 |
| `turn_count` | INT | 轮数，每轮 +1 |
| `prompt_tokens` / `completion_tokens` / `total_tokens` / `cache_read_tokens` / `cache_creation_tokens` / `reasoning_tokens` | BIGINT | 逐轮累加 |
| `status` | VARCHAR(32) | 最后一轮状态（ok / error） |
| `msg_count` | INT | `messages` 已累积的消息条数（增量切分 + compaction 检测） |
| `messages` | PG TEXT / MySQL LONGTEXT（`TypeLongText`） | 累积会话内容（JSON 数组文本，见下）。不用 JSON 列：只整存整取、不做 JSON 内查询，LONGTEXT 省掉 MySQL 的写入校验开销 |
| `created_at` | TIMESTAMP(0)/DATETIME(0) | 首轮时间（**分片归属键**：行落在首轮月份的分表，跨月续聊只更新原分表的行，不搬移） |
| `last_active_at` | 同上 | 最后活跃时间 |

索引（每张分表，索引名带月份后缀）：`UNIQUE(session_id)`、
`(last_response_id)`、`(last_active_at)`、`(ext_key_id)`。

### `messages` 累积内容

每轮追加「本轮新增 input 消息 + 本轮 assistant 输出块」：

- **responses 格式**：`sess.input` + 本轮输出（网关天然知道增量边界，
  `sess.prev` 是合并前历史）；
- **无状态格式（openai/anthropic）**：`req.Messages[msg_count:]` + 本轮输出
  （客户端重发全量历史，前 `msg_count` 条是已累积部分）；
- compaction 触发时见上节。

存的是 IR 的 `[]Message` JSON（与按轮记录的 `request_ir`/`response_ir` 同词表），
前端复用 `IrContent` 渲染。上限 16 MiB：超过后停止追加内容、只继续累计数
（`messages` 截断标记），避免个别超长会话把单行撑爆。

## 写入路径

挂在 `convCtx.finish` 的**同一个 `DoAsync` 闭包**里（db.Writer 单线程，
read-modify-write 无竞争），在 `InsertConversation` 之后执行：

1. `extractConversationID(...)` → 空则跳过（零开销）；
2. 按 `session_id` 跨分表扇出查（分表清单新→旧，每表走唯一索引）：
   - 命中 → UPDATE 该行（追加 messages、msg_count、turn_count+1、token 累加、
     status/model/ext_key_id/last_active_at/last_response_id 刷新）；
   - 未命中 → `EnsureSessionShard(当月)` 后 INSERT 到当月分表；
3. 任何失败只记日志，不影响主请求与按轮归档。

故障转移的多候选：每个候选的 `finish` 都会走到这里，但只有最终成功的候选
追加内容——**错误候选只更新计数（turn_count/token 不变）还是完全跳过？**
决定：**跳过**。会话视图面向浏览，失败的候选尝试已在按轮记录里，会话表只
反映用户实际看到的对话流（`finish` 的 `status == "ok"` 才追加内容并
turn_count+1；error 轮次只在会话已存在时刷新 status/last_active_at）。

**错误轮不推进 `last_response_id`**（实现细节，但语义关键）：网关的 responses
契约是「失败不保存会话，客户端带同一个 `previous_response_id` 重试」。若把失败
轮的 respID 写进 `last_response_id`，客户端按契约用上一成功轮的 pid 重试时按
`last_response_id` 就查不到本行，会以 pid 为根新开一行，把一条对话劈成两行。
留成最后成功轮的 id，重试才落回原会话（回归测试
`TestUpsertSessionTurn_FailedTurnKeepsChain`）。

## Admin API（只读）

- `GET /api/admin/conv-sessions?page=&size=` — 元数据列（不含 `messages`），
  跨分表分页复用 `convPageWindows` 机制（按 `last_active_at` 新→旧在各分表内
  排序，分表按月份新→旧拼接）。SQLite 返回 `{"data":[],"total":0,"disabled":true}`。
- `GET /api/admin/conv-sessions/:id` — 含 `messages`，跨分表 `WHERE id=?` 扇出
  （同 `GetConversation` 模式）。不存在返回 404。

## 前端

- `web/src/api/conversations.ts`：加 `listConvSessions` / `getConvSession` 与类型。
- `web/src/themes/classic/views/Conversations.vue` 与
  `web/src/themes/glass/views/GlassConversations.vue`：顶部加 tab——
  「按请求」（现有列表原样）/「按会话」（列：会话 id、harness、key、模型、
  轮数、token 合计、状态、首轮时间、最后活跃）。
- 会话详情：抽屉/弹层复用 `IrContent.vue` 渲染 `messages` 数组。
- 双主题 parity 测试同步加（同请求序列、同渲染要素）。

## 测试

- store（SQLite 单表路径仅单测）：upsert 首轮/续轮、`session_id` 跨分表查找、
  跨月归属（续轮更新旧分表）、`last_response_id` 定位、分页窗口、16 MiB 截断。
- gateway：`extractConversationID` 全规则单测（dsh 头 / 亲和头优先级 /
  responses 链 / claude-code metadata / 无 id 回退 / 前缀防碰撞）；
  compaction 检测（显式头 + msg_count 回退）；finish 接线集成（ok 追加、
  error 不追加、无 id 零副作用）。
- adminapi：两端点 + SQLite disabled。
- e2e（PG/MySQL，沿用 `DB_TEST_PG_DSN` / `DB_TEST_MYSQL_DSN` 门控）：
  建表、upsert、跨分表读、月分表自动创建。
- 前端：parity + tab 渲染。

## 文档

- 本文件；
- `docs/conversation-sharding.md` 增补会话表分表段；
- `AGENTS.md` 路由清单与分表说明更新（随代码分支一起走）。

## 已知限制

- **「重新生成」可能被误判为压缩**：增量切分是纯长度比较
  （`len(本轮历史) < msg_count` ⇒ 视为历史被压缩）。客户端 regenerate 时若重发
  的是**不含上一条 assistant** 的历史，长度同样变短，会多插一个压缩边界标记并把
  重发部分再并入一次。不是数据损坏（内容都在），只是浏览视图有噪音。精确区分需要
  前缀哈希（内容级判定），留作后续独立改动。
- **客户端流式 pipelining 会分裂会话**：流未结束就发下一轮时，两轮的 `finish`
  入队顺序可能与发生顺序相反，续接轮先执行 → 按 `last_response_id` 落空 → 以 pid
  为根另开一行。主流 SDK 不会这样用（都要等上一轮结束才发下一轮），暂不处理。
- **会话行归属首轮月份的分表，跨月续聊不搬移**：列表按分表月份新→旧拼接，因此
  「最后活跃」很新但首轮在上个月的老会话，会排在当月新会话之后。列表内排序（同一
  分表内 `last_active_at DESC`）不受影响。
- **每轮 upsert 是读-改-写**：会把该行 `messages`（上限 16 MiB）整列读回再写回。
  writer 单线程下这是吞吐上限，但会话数量与轮次频率远低于请求量，当前可接受；
  若成为瓶颈，可改为 SQL 侧 `CONCAT`/`LEFT` 拼接（PG/MySQL 都支持）。
- **跨分表查找随月份数线性扇出**：`session_id` / `last_response_id` 定位逐分表
  查（各表走索引，清单新→旧），活跃会话通常第一张表命中。月份数增长带来的额外
  空查在可接受范围。
- **会话 id 截断到 200 字符**（`sessionIDMaxLen`，列宽 `VARCHAR(255)` 含前缀）：
  现有来源（resp_/uuid/dsh 会话名）都远短于此，理论碰撞风险可忽略。
