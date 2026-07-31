# Responses API 格式转换设计

日期：2026-07-31
状态：已确认（用户已批准方案 A 及全部四节设计）

## 目标

为 any-llm 网关增加 OpenAI Responses API 格式支持，**入站与上游两个方向都支持**，与现有 `openai`（chat completions）、`anthropic` 格式完全对称：

- **入站**：客户端可 `POST /v1/responses` 以 Responses 格式调用网关，网关转为任意上游格式。
- **上游**：上游可配置为 `responses` 格式，网关把请求转为 Responses 格式发出。
- 流式（SSE）与非流式都支持。
- thinking ↔ Responses `reasoning` 互转。
- token 明细（输入/输出/缓存命中/推理）跨格式完整透传。

## 架构

新建独立包 `internal/translate/responses/`，与 `openai/`、`anthropic/` 对称，通过 IR（`internal/translate/ir.go`）与其他格式互转。网关与上游每层只加一个分支。

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

入站忽略（IR 无对应载体）：`store`、`previous_response_id`（网关无状态）、`text.format`。

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

### 第二节：网关与上游接入

| 文件 | 改动 |
|---|---|
| `internal/gateway/router.go` | 新增路由：`POST /v1/responses` → `handleCompletion(w, r, "responses")` |
| `internal/gateway/handler_openai.go` | `decodeInbound` 加 `"responses"` 分支；`handleNonStream` 编码 switch 加 `responses.EncodeResponse`；`handleStream` encoder 选择加 `responses.NewStreamEncoder(realModel)`，事件循环结束后若 encoder 有 `Flush()` 则调用（type assertion） |
| `internal/gateway/errors.go` | 零改动（responses 错误形状与 OpenAI 一致，default 分支天然覆盖） |
| `internal/upstream/client.go` | `Call` 加 `case "responses"`：`responses.EncodeRequest`、path=`/responses`、`Authorization: Bearer`；不需要 `injectStreamOptions`（usage 随 `response.completed` 天然返回）；非流式解码加 `responses.DecodeResponse`；`streamLoop` 加 responses 分支（decoder + 从 message_delta 事件取全量 usage） |
| `internal/upstream/fetch.go` | header switch 的 `case "openai"` 改为 `case "openai", "responses"`（Bearer + `/models`，URL default 分支已覆盖） |
| `recordUsage` | 零改动（in_format/up_format 只是字符串流转） |

流式细节：keepalive ping（`: kp\n`）与流中错误帧（`data: {"error":{...}}`）走现有 `inFormat != "anthropic"` default 分支；anthropic 专属 content_block_start 合成块有 `if inFormat == "anthropic"` 守卫，responses 不误入。

### 第三节：DB 迁移与后台（用户调整后的方案）

**1. 表结构不加约束，校验收在应用层**

- `migrations.go` 两个 CREATE TABLE `upstreams`：移除 `CHECK(format IN ...)`，新库直接无约束。
- 旧库迁移新步骤 `dropUpstreamFormatCheck(d)`（在 `migrationSQLite`/`migrationPG` 之后、`migrateExtraCols` 之前执行；此时 SQLite `foreign_keys` 还是 OFF，外键文本不会被子表改写）：
  - **SQLite**：查 `sqlite_master` 中 `upstreams` 的建表 SQL，含 `CHECK(format IN` 才动手，备份→重建→还原流程整体在一个事务里：
    ```
    BEGIN;
    ALTER TABLE upstreams RENAME TO upstreams_bak;   -- ① 备份（旧表完整保留）
    CREATE TABLE upstreams (同列定义，去掉 CHECK);    -- ② 重建
    INSERT INTO upstreams (显式列名…) SELECT 同列名… FROM upstreams_bak;  -- ③ 还原
    DROP TABLE upstreams_bak;                        -- ④ 确认还原成功后才清理备份
    COMMIT;
    ```
    列名显式列出，防列序漂移；任何一步失败回滚，旧表（备份）还在。
  - **PG**：`ALTER TABLE upstreams DROP CONSTRAINT IF EXISTS upstreams_format_check`（PG 对未命名 CHECK 的自动命名 `<table>_<column>_check`；新库无此约束时幂等跳过，无需重建）。
- `internal/webapi/upstreams.go:42` 应用层校验改为 `openai | anthropic | responses`。
- 未来再加格式零表结构改动。

**2. 后台表单**

- `web/src/views/Upstreams.vue`：格式单选加 `<n-radio value="responses">Responses</n-radio>`（表格 format 标签走现有 `row.format === 'openai' ? 'info' : 'warning'`，responses 归 warning）。

### 第四节：测试

- **responses 包单测**：请求 decode/encode（工具、图片、system+instructions 合并）、响应 decode/encode（usage 明细、thinking→reasoning、tool_use→function_call）、流式解码（文本序列、arguments 跨 chunk 分段、thinking+文本混合、completed 的 usage、缺失 start 的合成）、流式编码（事件序列断言）、错误路径。
- **cross 测试**：Responses→IR→openai、Responses→IR→anthropic 往返（复用 `assertRequestsMatch`），usage 明细跨格式透传（沿用 `TestCrossUsage_*` 模式）。
- **网关/上游测试**：`/v1/responses` 路由；httptest mock 上游验证 `client.Call` responses 分支（非流式 + 流式 SSE）。
- **DB 迁移测试**：旧版建表 SQL（含 CHECK）建库 + 插入数据 → 走 `OpenSQLite` → 断言 CHECK 已移除、数据完好。
- **限制说明**：目前除 OpenAI 官方外没有 Responses 协议服务商可真实联调（DeepSeek 等只支持 chat completions/anthropic），上游方向真实调用用 httptest mock 覆盖；入站方向（Responses 客户端 → openai/anthropic 上游）可用现网 DeepSeek 实测。

## 已知限制

- `previous_response_id` / `store` 不支持（网关无状态，不维护会话）。
- 上游 responses 的 `reasoning.content`（完整思考文本）在转成 IR 时丢弃，只保留 summary；`reasoning_text.delta` 流式事件忽略。
- 入站 responses 的 `input_file` / `input_audio` 报 400。
- `text.format`（JSON schema 输出）不支持。
