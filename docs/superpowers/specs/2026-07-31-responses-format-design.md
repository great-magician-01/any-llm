# Responses API 格式转换设计

日期：2026-07-31
状态：已确认（用户已批准方案 A 及全部设计节；会话存储定为 DB 表存储）

## 目标

为 any-llm 网关增加 OpenAI Responses API 格式支持，**入站与上游两个方向都支持**，与现有 `openai`（chat completions）、`anthropic` 格式完全对称：

- **入站**：客户端可 `POST /v1/responses` 以 Responses 格式调用网关，网关转为任意上游格式。
- **上游**：上游可配置为 `responses` 格式，网关把请求转为 Responses 格式发出。
- 流式（SSE）与非流式都支持。
- thinking ↔ Responses `reasoning` 互转。
- token 明细（输入/输出/缓存命中/推理）跨格式完整透传。
- **有状态会话**（`store` + `previous_response_id`）由网关在 DB 中维护，历史不丢失。

## 架构

新建独立包 `internal/translate/responses/`，与 `openai/`、`anthropic/` 对称，通过 IR（`internal/translate/ir.go`）与其他格式互转。网关与上游每层只加一个分支。会话存储是网关层功能（`internal/gateway/session.go`），不进入 translate 包。

### 第一节：`internal/translate/responses` 包

四个文件：`types.go` / `decode.go` / `encode.go` / `stream.go`。

#### 请求映射（Responses ↔ IR）

| Responses 字段 | 方向 | IR |
|---|---|---|
| `instructions`（字符串或数组）+ input 中 system 角色消息 | 双 | `System []TextBlock` |
| `input[]` 中 `{"role":"user"/"assistant","content":[...]}` | 双 | 普通消息；part：`input_text`→text 块，`input_image`→image 块 |
| `input[]` 中 `{"type":"function_call",...}` | 双 | assistant 消息 + tool_use 块（ID=call_id，arguments 原样 JSON） |
| `input[]` 中 `{"type":"function_call_output",...}` | 双 | user 消息 + tool_result 块 |
| `tools[]`（function 类型） | 双 | `Tools` |
| `tool_choice`（auto/required/none/`{"type":"function","name"}`） | 双 | `ToolChoice` |
| `max_output_tokens` / `stream` | 双 | `MaxTokens` / `Stream` |
| `previous_response_id` / `store` | 入站→`req.Extra` | 网关层消费（见第五节），**encode 时显式跳过这两个 key 不转发给上游** |

入站忽略（IR 无对应载体）：`text.format`。

出站无载体的丢弃/拒绝：
- assistant 历史消息中的 thinking 块：丢弃（Responses input 无思考载体）。
- 入站 `input_file` / `input_audio` 等未知 part 类型：报 400（不静默丢数据）。

工具调用走顶层 item（`function_call` / `function_call_output`），与聊天历史消息平级——Responses 与 chat completions 最大的结构差异。

#### 响应映射

- 非流式 `output[]` → `Content`：
  - `message` → text 块（`output_text` part）
  - `function_call` → tool_use 块（ID=call_id，arguments 原样 JSON）
  - `reasoning` → thinking 块（Thinking=summary 文本合并，Signature=item id，与现有 `reasoning_content`→thinking 的做法一致）
  - 未知 item/part 类型跳过
- `status` → `StopReason`：completed→`stop`（output 含 function_call 时推 `tool_calls`，与 chat completions 一致）；incomplete（reason=max_output_tokens）→`max_tokens`；failed→`stop` + 日志（错误主要由 HTTP/事件层报告）。
- usage：`input_tokens`→Input、`output_tokens`→Output、`input_tokens_details.cached_tokens`→CacheRead、`output_tokens_details.reasoning_tokens`→Reasoning；`cache_creation` 恒 0（Responses 无此字段）。
- 出站构造完整 response 对象：`id:"resp_"+hex`、`created_at`、status（stop/tool_calls→completed，max_tokens→incomplete+`incomplete_details`）、output 数组（连续 text 块合并进一个 message item、thinking→reasoning item 只带 summary、tool_use→function_call item）、usage 明细（0 值省略）。
- **id 生成在网关层**：`responses.NewID()`（`resp_` + crypto/rand 十六进制）。非流式由网关写入 `IR.Response.ID` 后编码器直接使用；流式由网关生成后传入 `NewStreamEncoder(realModel, id)`。保证客户端看到的 id 与网关存的会话 key 一致。

#### 流式

**StreamDecoder**（上游 Responses SSE → IR 事件）：
- `response.created` / `response.in_progress` → message_start
- `output_item.added`（message/reasoning/function_call）→ content_block_start
- `output_text.delta` → text_delta
- `function_call_arguments.delta` → input_json_delta
- `reasoning_summary_text.delta` → thinking_delta（`reasoning_text.delta` 忽略，只保留 summary）
- `output_item.done` → content_block_stop（function_call 若从未收到 arguments delta——上游只在 done 里带完整 arguments——补发完整 input_json_delta，沿用 openai 解码器 first-chunk 修复的模式）
- `response.completed` → message_delta（带完整 usage：input/output/cache/reasoning）+ message_stop
- `response.failed` / `response.errored` → error 事件

**StreamEncoder**（IR 事件 → Responses SSE），有状态：
- 首事件时发 `response.created` + `response.in_progress`
- text/thinking/tool_use 各自合成 `output_item.added`（带 output_index 序号）→ 对应 delta → done 三连
- tool_use start 块若自带 arguments 片段，先补发一段 `function_call_arguments.delta`
- 缺失 content_block_start 的 delta 自动补合成（自包含，不需要网关侧合成）
- `response.completed` 在流结束时发：encoder 累积 output items 与 usage（input 来自 message_start、output 来自 message_delta），暴露可选 `Flush()` 接口，网关在事件循环结束后调用（避免上游不发 message_stop 时 completed 发不出去）
- 暴露可选 `Content() []translate.ContentBlock`：返回累积的模型输出（text/tool_use/thinking），供网关保存会话

### 第二节：网关与上游接入

| 文件 | 改动 |
|---|---|
| `internal/gateway/router.go` | 新增路由：`POST /v1/responses` → `handleCompletion(w, r, "responses")` |
| `internal/gateway/handler_openai.go` | `decodeInbound` 加 `"responses"` 分支；`handleNonStream` 编码 switch 加 `responses.EncodeResponse`；`handleStream` encoder 选择加 `responses.NewStreamEncoder(realModel, respID)`，事件循环结束后调用 encoder 的可选 `Flush()` / `Content()` 接口（type assertion） |
| `internal/gateway/errors.go` | 零改动（responses 错误形状与 OpenAI 一致，default 分支天然覆盖） |
| `internal/upstream/client.go` | `Call` 加 `case "responses"`：`responses.EncodeRequest`、path=`/responses`、`Authorization: Bearer`；不需要 `injectStreamOptions`（usage 随 `response.completed` 天然返回）；非流式解码加 `responses.DecodeResponse`；`streamLoop` 加 responses 分支（decoder + 从 message_delta 事件取全量 usage） |
| `internal/upstream/fetch.go` | header switch 的 `case "openai"` 改为 `case "openai", "responses"`（Bearer + `/models`，URL default 分支已覆盖） |
| `internal/gateway/session.go`（新） | 会话存储，见第五节 |
| `recordUsage` | 零改动（in_format/up_format 只是字符串流转） |

流式细节：keepalive ping（`: kp\n`）与流中错误帧（`data: {"error":{...}}`）走现有 `inFormat != "anthropic"` default 分支；anthropic 专属 content_block_start 合成块有 `if inFormat == "anthropic"` 守卫，responses 不误入。

### 第三节：DB 迁移与后台（用户调整后的方案）

**1. 表结构不加约束，校验收在应用层**

- `migrations.go` 两个 CREATE TABLE `upstreams`：移除 `CHECK(format IN ...)`，新库直接无约束。
- 旧库迁移新步骤 `dropUpstreamFormatCheck(d)`（在 `migrationSQLite`/`migrationPG` 之后、`migrateExtraCols` 之前执行；此时 SQLite `foreign_keys` 还是 OFF，外键文本不会被子表改写）：
  - **SQLite**：查 `sqlite_master` 中 `upstreams` 的建表 SQL，含 `CHECK(format IN` 才动手，备份→重建→还原流程整体在一个事务里：
    ```
    BEGIN;
    PRAGMA legacy_alter_table=ON;                    -- ① 防止 RENAME 改写其他表的 REFERENCES
    ALTER TABLE upstreams RENAME TO upstreams_bak;   -- ② 备份（旧表完整保留）
    CREATE TABLE upstreams (同列定义，去掉 CHECK);    -- ③ 重建
    INSERT INTO upstreams (显式列名…) SELECT 同列名… FROM upstreams_bak;  -- ④ 还原
    DROP TABLE upstreams_bak;                        -- ⑤ 确认还原成功后才清理备份
    PRAGMA legacy_alter_table=OFF;
    COMMIT;
    ```
    列名显式列出，防列序漂移；任何一步失败回滚，旧表（备份）还在。**实现时实测确认**：SQLite 3.25+（modernc v1.53.0）的 `ALTER TABLE RENAME` 改写其他表的 REFERENCES 子句由 `legacy_alter_table` 控制、与 foreign_keys 设置无关——必须临时 `legacy_alter_table=ON`，否则 `upstream_models` 的 REFERENCES 会被改成指向已删除的 `upstreams_bak`（迁移"成功"但产出坏库）。
  - **PG**：`ALTER TABLE upstreams DROP CONSTRAINT IF EXISTS upstreams_format_check`（PG 对未命名 CHECK 的自动命名 `<table>_<column>_check`；新库无此约束时幂等跳过，无需重建）。
- `internal/webapi/upstreams.go:42` 应用层校验改为 `openai | anthropic | responses`。
- 未来再加格式零表结构改动。

**2. 会话表**（新增，两个 dialect 的 CREATE TABLE IF NOT EXISTS 都加，旧库启动时自动建表，无额外迁移步骤）：

```sql
CREATE TABLE IF NOT EXISTS response_sessions (
    id TEXT PRIMARY KEY,                                    -- resp_xxx（客户端回传的 previous_response_id）
    messages TEXT NOT NULL,                                 -- 累积会话 JSON（[]translate.Message）
    created_at TIMESTAMP(0) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at TIMESTAMP(0) NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_resp_sessions_used ON response_sessions(last_used_at);
```

**3. 后台表单**

- `web/src/views/Upstreams.vue`：格式单选加 `<n-radio value="responses">Responses</n-radio>`（表格 format 标签走现有 `row.format === 'openai' ? 'info' : 'warning'`，responses 归 warning）。

### 第四节：会话存储（DB）

**为什么需要**：Responses 有状态模式（`store: true` + `previous_response_id`）下，客户端只发新一轮内容，历史由服务器持有。网关转给上游的调用始终无状态、带全量历史，因此网关必须自己累积并存储会话；否则下一轮只剩新内容，历史丢失，工具调用链（`function_call_output` 引用的 `call_id` 来自上一轮 assistant 输出）也会断裂。

**存放位置**：`internal/gateway/session.go`，`SessionStore` 封装 `*sql.DB`（同步读写，走 `g.db`；`sql.DB` 并发安全，与 writer 异步队列互不干扰）。

**流程**：
1. 入站解码后（仅 inFormat=="responses"）：`previous_response_id` 非空 → `Get(id)`：
   - 命中：`irReq.Messages = append(历史, 新 input...)`，更新 `last_used_at`
   - 未命中或已过期（TTL 24h 空闲）：`WriteError 400, "invalid_previous_response_id"`（沿用 OpenAI 错误词表，明确报错不静默丢历史）
2. 网关生成 `respID = responses.NewID()`，非流式写入 `result.Response.ID`、流式传入 encoder。
3. 上游调用**成功后**（status ok）保存会话：`Put(id, 旧历史 + 新 input + 本轮模型输出)`。模型输出从非流式的 `result.Response.Content` 或流式 encoder 的 `Content()` 取。**失败不保存**（客户端会带同一 `previous_response_id` 重试，若已保存会造成 input 重复）。
4. `Put` 时顺带惰性清扫：`DELETE FROM response_sessions WHERE last_used_at < now - 24h`（有索引，代价小）。

**语义放宽说明**：`store: false` 也照常存（客户端可能 store 与 previous_response_id 混用），由 TTL 淘汰兜底；`store` 字段本身不改变网关行为。会话不按 ext key 隔离（128 位随机 id 即访问凭证，与 OpenAI 一致）。

### 第五节：测试

- **responses 包单测**：请求 decode/encode（工具、图片、system+instructions 合并、previous_response_id/store 进 Extra 且 encode 不转发）、响应 decode/encode（usage 明细、thinking→reasoning、tool_use→function_call）、流式解码（文本序列、arguments 跨 chunk 分段、thinking+文本混合、completed 的 usage、缺失 start 的合成）、流式编码（事件序列断言、`Content()` 累积正确）、错误路径。
- **cross 测试**：Responses→IR→openai、Responses→IR→anthropic 往返（复用 `assertRequestsMatch`），usage 明细跨格式透传（沿用 `TestCrossUsage_*` 模式）。
- **网关/上游测试**：`/v1/responses` 路由；httptest mock 上游验证 `client.Call` responses 分支（非流式 + 流式 SSE）。
- **会话测试**：Put/Get/过期/清扫；**工具循环集成**——mock 上游：第一轮返回 function_call → 第二轮带 `previous_response_id` + `function_call_output`，断言上游收到的请求包含完整两轮历史（含 assistant 工具调用块）；未知 previous_response_id → 400。
- **DB 迁移测试**：旧版建表 SQL（含 CHECK）建库 + 插入数据 → 走 `OpenSQLite` → 断言 CHECK 已移除、数据完好；response_sessions 表自动创建。
- **限制说明**：目前除 OpenAI 官方外没有 Responses 协议服务商可真实联调（DeepSeek 等只支持 chat completions/anthropic），上游方向真实调用用 httptest mock 覆盖；入站方向（Responses 客户端 → openai/anthropic 上游）可用现网 DeepSeek 实测。

## 已知限制

- `store` 字段不改变网关行为：所有 responses 请求都存会话（TTL 24h 空闲淘汰兜底），语义比 OpenAI 更宽松但完全兼容。
- 会话 TTL 到期或 DB 表被清空后，`previous_response_id` 返回 400 `invalid_previous_response_id`，客户端应带全量历史重试。
- 上游 responses 的 `reasoning.content`（完整思考文本）在转成 IR 时丢弃，只保留 summary；`reasoning_text.delta` 流式事件忽略。
- 入站 responses 的 `input_file` / `input_audio` 报 400。
- `text.format`（JSON schema 输出）不支持。
- 同一会话的并发请求读改写可能交错（客户端不应并行推进同一会话）。
