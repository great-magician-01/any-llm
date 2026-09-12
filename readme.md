# any-llm

通用 LLM API 网关，将多个上游模型服务统一为 OpenAI / Anthropic / Responses 兼容接口。单二进制部署，内置管理界面。

## 特性

- **统一网关**：对外提供 OpenAI（`/v1/chat/completions`）、Anthropic（`/v1/messages`）、Responses（`/v1/responses`）兼容 API，流式与非流式均支持
- **多上游管理**：通过 Web UI 管理多个模型服务商（OpenAI / Anthropic / Responses 格式），支持自动拉取模型列表、启用/禁用
- **协议转换**：请求经过内部 IR 层翻译，三种格式任意互转
- **模型别名**：固定对外模型名，绑定有序的「上游 + 真实模型」列表，按优先级自动故障转移
- **Token 限额**：外部 Key 与上游均可设置日 / 月 Token 配额（0 = 不限），超限返回 429
- **模型权限**：每个外部 Key 可限定可用模型白名单（别名或 `上游/模型`，留空 = 不限），越权请求返回 403，`/v1/models` 按 Key 过滤
- **API Key 管理**：创建和管理外部 API Key（`all-sk-*` 格式），可单独启用/禁用，一键复制调用示例或 Oh My Pi 配置
- **用量统计**：按 Key / 上游 / 模型维度记录 Token 用量与调用耗时（token/s），支持按日汇总图表
- **对话记录**：自动归档每次调用的完整请求/响应（仅 PostgreSQL），应用层按月分表存储（[设计文档](docs/conversation-sharding.md)）
- **余额快照**：定时抓取厂商余额/额度（DeepSeek 余额、Kimi for Coding 用量），保留历史趋势
- **配置备份**：上游与别名配置一键导出/导入
- **灵活存储**：支持 SQLite（默认，纯 Go）和 PostgreSQL
- **单二进制**：Go 后端，内嵌 Vue 前端，零 CGO 依赖运行
- **双主题界面**：经典深色与毛玻璃主题，界面内一键切换

## 快速开始

### 二进制运行

```bash
# 1. 构建前端（必需，dist 会被嵌入到 Go 二进制中）
cd web && npm run build && cd ..
# 2. 构建后端
go build -o any-llm ./cmd/any-llm/
# 3. 运行
./any-llm
```

服务默认监听 `0.0.0.0:6718`，浏览器打开 `http://localhost:6718` 进入管理界面。

默认管理员密码为 `admin`，建议通过环境变量修改。

### Docker 运行

```bash
# 构建镜像
docker build -t any-llm .

# 运行（数据库、日志和会话密钥持久化到宿主机）
docker run -d \
  -p 6718:6718 \
  -v $PWD/data:/data \
  -e ANY_LLM_PORT=6718 \
  -e ANY_LLM_DB_PATH=/data/any-llm.db \
  -e ANY_LLM_LOG_FILE=/data/logs/any-llm.log \
  -e ANY_LLM_MASTER_PASSWORD=your-password \
  -e ANY_LLM_SESSION_SECRET_FILE=/data/.session-secret \
  --name any-llm \
  any-llm

# 查看容器日志
docker logs -f any-llm
```

或使用仓库自带的 `docker compose`：

```bash
echo 'ANY_LLM_MASTER_PASSWORD=your-password' >> .env
docker compose up -d
```

> 从 `docker` 分支的 GitHub Actions 可直接下载预构建镜像（`.tar` 格式，非 `.tar.gz`）。
> 完整部署说明见 [docs/docker.md](docs/docker.md)。

## 配置

所有配置通过环境变量或 `.env` 文件设置（`.env` 不会覆盖已存在的环境变量）。

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `ANY_LLM_HOST` | `0.0.0.0` | 监听地址 |
| `ANY_LLM_PORT` | `6718` | 监听端口 |
| `ANY_LLM_DB_PATH` | `./any-llm.db` | SQLite 数据库路径 |
| `ANY_LLM_MASTER_PASSWORD` | `admin` | 管理员密码 |
| `ANY_LLM_SESSION_SECRET` | 见说明 | 会话密钥。留空时自动生成并保存到 `ANY_LLM_SESSION_SECRET_FILE` 指定的文件，重启后登录状态不丢失 |
| `ANY_LLM_SESSION_SECRET_FILE` | `./.session-secret` | 自动生成的会话密钥的保存路径（仅 `ANY_LLM_SESSION_SECRET` 为空时生效） |
| `ANY_LLM_SESSION_TTL` | `24h` | 管理员会话有效期：Go duration（`24h`、`168h`）或纯小时数（`24`）；`0` = 永不过期。滑动续期：剩余不足一半时自动重新签发，活跃期间不掉线 |
| `ANY_LLM_BALANCE_INTERVAL` | `10m` | 厂商余额/额度快照轮询间隔：Go duration（`10m`、`30m`）或纯小时数；`0` = 关闭自动抓取（含启动时），管理页手动刷新仍可用 |
| `ANY_LLM_LOG_FILE` | `./logs/any-llm.log` | 日志基础路径，实际写入 `{dir}/{日期}/{filename}`；留空仅输出到 stdout |
| `ANY_LLM_LOG_LEVEL` | `info` | 日志级别：`debug` / `info` / `warn` / `error` |
| `DB_TYPE` | `sqlite` | 数据库类型：`sqlite` 或 `postgres`（不区分大小写） |
| `DB_HOST` | `localhost` | PostgreSQL 主机（`DB_TYPE=postgres` 时生效） |
| `DB_PORT` | `5432` | PostgreSQL 端口 |
| `DB_USER` | `postgres` | PostgreSQL 用户名 |
| `DB_PASSWORD` | （空） | PostgreSQL 密码 |
| `DB_NAME` | `amanuensis` | PostgreSQL 数据库名 |
| `DB_SCHEMA` | `public` | PostgreSQL schema（不存在则自动创建） |

复制 `.env.example` 为 `.env` 并修改后重启服务即可。

## 使用

### 管理后台

访问 `http://localhost:6718`，使用管理员密码登录：

1. **Dashboard（总览）**：用量统计卡片、本月模型用量 Top、资源与快捷操作
2. **Upstreams（上游服务）**：添加模型服务商，配置 API 地址、密钥、协议格式，支持自动拉取模型列表、启用/禁用、日/月 Token 限额；DeepSeek / Kimi 上游自动抓取余额快照；支持配置导出/导入
3. **Keys（API 密钥）**：创建和管理外部 API Key，可设置日/月 Token 限额与可用模型白名单，一键复制调用示例或 Oh My Pi 配置
4. **Aliases（模型别名）**：维护固定对外模型名及绑定列表
5. **Conversations（对话记录）**：查看归档的完整请求/响应与 Token 明细（仅 PostgreSQL，SQLite 下显示禁用提示）
6. **Usage（用量）**：Token 消耗、调用耗时（token/s）与按日汇总图表

### 调用网关

获取 Key 后，像使用 OpenAI 一样调用：

```bash
curl http://localhost:6718/v1/chat/completions \
  -H "Authorization: Bearer all-sk-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "my-upstream/gpt-4",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

- 模型格式为 `上游名称/模型名称`（如 `my-openai/gpt-4`），或任意已配置的**模型别名**（优先于直连路由）
- API Key 格式：`all-sk-` 前缀 + 32 位字符，通过 `Authorization: Bearer ...` 或 `x-api-key` 头传递

### 可用端点

| 端点 | 说明 |
|------|------|
| `GET /v1/models` | 列出可用模型（含别名）；需携带 API Key，并按该 Key 的模型白名单过滤 |
| `POST /v1/chat/completions` | OpenAI 兼容聊天接口 |
| `POST /v1/messages` | Anthropic 兼容消息接口 |
| `POST /v1/responses` | Responses 格式聊天接口 |

## 开发

```bash
# 后端（终端 1）
go run ./cmd/any-llm/

# 前端（终端 2，HMR 开发服务器，代理到后端）
cd web && npm run dev

# 运行后端测试
go test ./...

# 运行前端测试
cd web && npm run test
```

前端开发服务器默认代理 `/api` 和 `/v1` 到 `localhost:6718`。

## 项目结构

```
cmd/any-llm/          # 入口，嵌入前端 dist
internal/
  auth/               # 会话认证（HMAC-SHA256，滑动续期）
  config/             # 环境变量加载
  db/                 # 数据库初始化与迁移（SQLite / PostgreSQL）
  gateway/            # 公开 API 网关路由
  logger/             # slog 日志封装
  model/              # 数据模型与 CRUD
  translate/          # OpenAI / Anthropic / Responses 格式翻译（IR 层）
  upstream/           # 上游 HTTP 客户端与余额快照轮询
  webapi/             # 管理后台 API
web/                  # Vue 3 前端（Naive UI + Vite，经典 + 毛玻璃双主题）
```
