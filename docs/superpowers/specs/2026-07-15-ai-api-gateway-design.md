# any-llm: AI API 格式中转网关 设计文档

- 日期: 2026-07-15
- 状态: 已确认，待写实现计划

## 目标

构建一个 AI API 中转网关：上游可接入 OpenAI 或 Anthropic 格式的任意兼容服务，对外同时暴露 OpenAI 兼容与 Anthropic 兼容两套接口。客户端以任意一种格式请求，网关翻译成上游所需格式，再把响应翻译回客户端格式。同时强制获取每次请求的 token 用量并落库统计。

## 技术栈

- 后端: Go 1.25
- 前端: Vue 3 + TypeScript + Vite + Naive UI
- 数据库: SQLite
- 打包: 单体——前端 `vite build` 产物用 `go:embed` 嵌入 Go 二进制，一个可执行文件同时提供对外网关、管理 API 和管理页面。

## 已确认的需求决策

| 维度 | 决策 |
|------|------|
| 流式响应 | 非流式 + 流式(SSE) 全支持，含跨格式逐 chunk 翻译 |
| Token 用量 | 始终向上游强制获取 usage，每次请求落库，页面按 key/模型/时间统计 |
| 对外 key | 生成 + 校验(格式前缀 + DB 存在且 enabled)，纯身份识别，无限流/权限 |
| 管理页认证 | 主密码登录，HMAC 无状态 session |
| 对外 model 命名 | `上游name/真实model`，按首个 `/` 拆分路由 |
| 翻译内容范围 | 全量：文本、system、多轮、function calling、图片输入 |
| 翻译层架构 | 方案 A——规范化中间表示(IR)，每格式一对 Decoder/Encoder |
| 前端 UI 库 | Naive UI |
| 主密码首次启动 | 未配置时默认 `admin` 并打印警告，引导尽快修改 |
| 部署 | 单体嵌入 |

## 整体架构

单体二进制，同一 HTTP 服务承载三类流量：

- 对外网关 `/v1/*`：OpenAI 兼容 + Anthropic 兼容入口，校验对外 key、路由、翻译、转发、统计。
- 管理 API `/api/admin/*`：上游/key/用量/登录管理，需 session 认证。
- 管理页面：其余路径回退到嵌入的 SPA `index.html`。

### 目录结构

```
cmd/any-llm/main.go            # 入口：加载配置、初始化DB、embed前端、启动HTTP
internal/
  config/        # 配置：主密码、端口、DB路径、session密钥
  db/            # sqlite 打开 + 迁移
  model/         # 领域结构体 + CRUD（upstream/model/extkey/usage）
  auth/          # 主密码登录、session 签发/校验中间件
  gateway/       # 对外接口
    router.go            # 解析 model=name/realmodel → 选上游
    handler_openai.go    # POST /v1/chat/completions  GET /v1/models
    handler_anthropic.go # POST /v1/messages
  translate/     # 翻译核心
    ir.go                # 规范化 IR 类型
    openai/              # Decoder+Encoder（请求/响应/流）
    anthropic/           # Decoder+Encoder（请求/响应/流）
  upstream/      # 调上游 HTTP 客户端，强制获取 usage
  usage/         # 用量落库
  webapi/        # 管理 API：upstreams/models/keys/usage/login
web/             # Vue3 前端，build→dist 被 embed
```

## 数据模型（SQLite）

### `upstreams`
| 字段 | 说明 |
|------|------|
| id | 主键 |
| name | unique，路由用，不含 `/` |
| base_url | 上游基地址 |
| api_key | 上游密钥 |
| format | `openai` 或 `anthropic` |
| created_at / updated_at | |

### `upstream_models`
| 字段 | 说明 |
|------|------|
| id | 主键 |
| upstream_id | FK → upstreams |
| model_name | 真实模型名 |
| manual | bool，手动添加的标记（拉取覆盖时保留手动项） |

### `ext_keys`
| 字段 | 说明 |
|------|------|
| id | 主键 |
| key | unique 索引，明文存储（当主键查） |
| label | 备注 |
| enabled | bool |
| created_at / last_used_at | |

### `usage_records`
| 字段 | 说明 |
|------|------|
| id | 主键 |
| ext_key_id | FK → ext_keys |
| upstream_id / upstream_name | 冗余 name 便于查询 |
| model | 真实 model 名 |
| in_format / up_format | `openai`/`anthropic` |
| prompt_tokens / completion_tokens / total_tokens | |
| stream | bool |
| status | `ok` / `error` |
| created_at | |

统计通过对 `usage_records` 做 `GROUP BY` 聚合实现（按 key / 模型 / 时间维度）。

## IR（规范化中间表示）

翻译层核心。一套覆盖 OpenAI 与 Anthropic 全部特性的内部结构。

```go
type Request struct {
    Model       string
    System      []TextBlock
    Messages    []Message
    Tools       []Tool
    ToolChoice  *ToolChoice
    MaxTokens   int
    Temperature *float64
    TopP        *float64
    Stream      bool
    Stop        []string
    Extra       map[string]any   // 未显式建模的字段透传
}

type Message struct {
    Role    string
    Content []ContentBlock
}

type ContentBlock struct {
    Type       string  // "text" | "image" | "tool_use" | "tool_result"
    Text       string
    Image      *Image
    ToolUse    *ToolUse
    ToolResult *ToolResult
}

type Tool struct {
    Name        string
    Description string
    InputSchema json.RawMessage
}

type Response struct {
    ID         string
    Model      string
    Content    []ContentBlock
    StopReason string
    Usage      Usage
    Extra      map[string]any
}

type Usage struct {
    InputTokens  int
    OutputTokens int
}

type StreamEvent struct {
    Type string  // message_start | content_block_start | content_block_delta
                 // | content_block_stop | message_delta | message_stop
    // 按事件类型填充：message_start 带 message 元信息+input_tokens；
    // delta 带 text/tool_use 增量；message_delta 带 stop_reason + usage(output_tokens)。
}
```

### 关键设计点

1. **Content 统一用 block 数组**：OpenAI 的 `content: string | array` 与 Anthropic 的 `content: array` 都映射到 `[]ContentBlock`。纯文本也转成 `[{type:"text",text:...}]`，消除 string/array 双形态。

2. **function calling 归一**：OpenAI 的 `tool_calls`(assistant) + `tool`(role) ↔ Anthropic 的 `tool_use`(assistant content block) + `tool_result`(user content block)。IR 用 `tool_use`/`tool_result` block 统一表达，各格式 Encoder 自行展开。

3. **Extra 透传**：IR 未显式建模的字段（如 OpenAI 的 `logprobs`、Anthropic 的 `top_k`）放 `Extra`，按目标格式选取或保留。避免漏字段，也避免 IR 无限膨胀。

4. **流式事件用 Anthropic 风格的细粒度类型**：`message_start` → `content_block_start` → 多个 `content_block_delta` → `content_block_stop` → `message_delta`(usage/stop_reason) → `message_stop`。这是语义最完整的流协议；编码成 OpenAI SSE 时把它"折叠"成 `data: {choices:[{delta:...}]}` chunk。

5. **两套 Decoder/Encoder**：`translate/openai` 与 `translate/anthropic` 各含请求/响应/流三对编解码函数。处理流程：

```
入站 body → [in_format DecodeRequest] → ir.Request → [up_format EncodeRequest] → 上游 HTTP
上游响应 → [up_format DecodeResponse/Stream] → ir.Response/StreamEvent → [in_format Encode] → 返回客户端
```

## 网关与路由

### 对外端点

| 方法 路径 | 说明 |
|----------|------|
| `POST /v1/chat/completions` | OpenAI 兼容入口，in_format=openai |
| `POST /v1/messages` | Anthropic 兼容入口，in_format=anthropic |
| `GET /v1/models` | 列出对外模型，每上游 model 输出 `{id:"name/model", object:"model"}` |

### 路由流程

1. 校验对外 key（`Authorization: Bearer <key>` 或 `x-api-key` 头都接受）。不存在/disabled → 401。
2. 从 `body.model` 按首个 `/` 拆分 `name/realmodel`。拆不出 `/` → 400；name 不存在 → 404。
3. 查 upstream（name → 配置 + format）。
4. 确定 in_format（入口路径决定）、up_format（upstream.format）。
5. `irReq = translate(in_format).DecodeRequest(body)`，`irReq.Model = realmodel`。
6. 调上游（见上游调用章节）。
7. 响应/流反向翻译回 in_format 返回。
8. 落库 usage（见用量章节）。

### 同格式也走 IR

当 `in_format == up_format` 时仍走 IR 而非直接透传，以统一获取 usage、统一落库、统一错误处理。同格式 Decoder/Encoder 代价低，换取一致性。

### `/v1/models`

聚合所有启用的上游及其 `upstream_models`，每个输出 `{id:"<name>/<model_name>", object:"model", created:...}`。OpenAI 与 Anthropic 客户端均可调用。

## 上游调用与强制 usage

`upstream.Client` 按 `up_format` 构造请求：

- **OpenAI 上游**：`POST {base_url}/chat/completions`，body 由 `openai.EncodeRequest(irReq)` 生成。强制注入 `stream_options:{include_usage:true}`（OpenAI 流式默认不返回 usage，必须显式开启；非流式 usage 在顶层）。
- **Anthropic 上游**：`POST {base_url}/messages`，body 由 `anthropic.EncodeRequest(irReq)` 生成。流式在 `message_start` 带 `input_tokens`、`message_delta` 带 `usage.output_tokens`，两者拼起来落库；非流式顶层 `usage`。

### 流式处理

上游返回 `text/event-stream`，按 SSE 规范逐 `data:` 行解析成上游格式事件 → `DecodeStream` → IR `StreamEvent` → `EncodeStream` → 写给客户端。在 `message_delta`/最终 usage 事件处提取 token 数。客户端断开时 ctx cancel 传播到上游请求。

### usage 兜底

若上游因故未返回 usage（极端情况），落库记 `total_tokens=0, status=ok` 并打 warn 日志，不阻塞响应。

### 错误处理

上游非 2xx → 翻译成对应入口格式的错误体：
- OpenAI 风格：`{"error":{"message":...,"type":...,"code":...}}`
- Anthropic 风格：`{"type":"error","error":{"type":...,"message":...}}`

HTTP 状态码透传上游。

## Token 用量落库与统计

每次网关请求（成功或失败都记）落一条 `usage_records`，字段见数据模型章节。

### 统计 API

| 端点 | 说明 |
|------|------|
| `GET /api/admin/usage/summary?group_by=key\|model\|upstream&from=&to=` | 聚合计数 |
| `GET /api/admin/usage/records?page=&size=&filters...` | 明细列表，分页 |

### 前端统计页

按 key/模型/时间维度切换，展示总 token、请求次数、成功率，附按天折线图（前端引入 ECharts 按需使用）。

## 管理页面与 API

### 认证（主密码）

- 启动时从配置读 `MASTER_PASSWORD` 与 `SESSION_SECRET`。
- `POST /api/admin/login {password}` → 校验 → 签发 HMAC 签名、含过期时间的无状态 session token，写入 cookie `s`。
- 中间件 `RequireAuth` 校验所有 `/api/admin/*`（除 `/login`），未登录 → 401。
- 首次启动若未配置 `master_password`，默认 `admin` 并打印警告，引导尽快修改。

### 管理 API（`/api/admin/*`）

| 资源 | 端点 | 说明 |
|------|------|------|
| 上游 | `GET/POST/PUT/DELETE /api/admin/upstreams` | CRUD；POST 时可选 `fetch_models=true` 触发拉取 |
| 上游模型 | `POST /api/admin/upstreams/:id/fetch-models` | 调上游 `/models` 拉取覆盖（保留手动项） |
| | `GET/POST/DELETE /api/admin/upstreams/:id/models` | 手动增删模型 |
| 对外 key | `GET/POST/DELETE /api/admin/keys` | POST 生成新 key，返回明文仅一次；GET 返回脱敏 |
| 用量 | `GET /api/admin/usage/summary`、`/records` | |
| 登录 | `POST /api/admin/login`、`POST /api/admin/logout` | |

### 对外 key 生成与校验

- 格式：`all-sk-` 前缀 + 32 字节随机 base62，如 `all-sk-xxxx...`（`all` = any-llm，便于辨识）。
- 生成时落库明文并建唯一索引；GET 列表返回 `all-sk-xxxx****`（露前 12 位 + 末 4 位）。
- 网关校验"合规" = ①格式含 `all-sk-` 前缀；②DB 中存在且 enabled。

## 前端结构

在已有 Vue3+TS+Vite 脚手架上加 `vue-router` + Naive UI + `axios`。

```
web/src/
  router.ts
  api/            # axios 实例 + 各资源 client
  views/
    Login.vue
    Upstreams.vue     # 上游列表/编辑 + 模型管理 + 拉取按钮
    Keys.vue          # 对外 key 生成/列表(脱敏)/删除
    Usage.vue         # 用量统计（维度切换 + 图表 + 明细表）
  components/
    Layout.vue        # 侧边栏导航 + 登出
    ModelEditor.vue   # 上游模型增删子组件
  App.vue / main.ts
```

页面与需求对应：
1. **Upstreams 页** = 需求①（配 key/baseURL/格式/name + 拉取/手动维护模型）
2. **Usage 页** = 需求③ 的展示侧
3. **Keys 页** = 需求④

## 配置与启动

`config.yaml`（可选，也可全走环境变量）：

```yaml
server:
  port: 8080
  db_path: ./any-llm.db
master_password: "${MASTER_PASSWORD}"   # 为空则默认 admin + 警告
session_secret: "${SESSION_SECRET}"     # 为空则随机生成（重启失效）
```

启动流程：`load config → open db → run migrations → embed 前端 → http.ListenAndServe`。开发时前端 `vite dev` 走 proxy 到后端（vite config 加 `/api`、`/v1` 代理到 `localhost:8080`）。

## 不在本次范围内（YAGNI）

- 对外 key 限流 / 额度 / 模型白名单（纯身份识别，数据模型不预留权限字段，后续真需要再加）
- 多用户账号体系（单一主密码）
- 请求/响应日志存档（只存 token 用量，不存内容）
- 上游健康检查 / 自动故障转移
- 多 DB 支持（只 SQLite）
