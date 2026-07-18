# any-llm

通用 LLM API 网关，将多个上游模型服务统一为 OpenAI/Anthropic 兼容接口。单二进制部署，内置管理界面。

## 特性

- **统一网关**：对外提供 OpenAI (`/v1/chat/completions`) 和 Anthropic (`/v1/messages`) 兼容 API
- **多上游管理**：通过 Web UI 管理多个模型服务商（OpenAI 兼容、Anthropic 兼容等）
- **协议转换**：请求经过内部 IR 层翻译，OpenAI/Anthropic 格式随意互转
- **API Key 管理**：创建和管理外部 API Key（`all-sk-*` 格式），可单独启用/禁用
- **用量统计**：按 Key 维度记录 Token 用量
- **单二进制**：Go 后端，内嵌 Vue 前端，SQLite 存储，零依赖运行

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

# 运行（数据库和日志持久化到宿主机）
docker run -d \
  -p 6718:6718 \
  -v $PWD/data:/data \
  -e ANY_LLM_PORT=6718 \
  -e ANY_LLM_DB_PATH=/data/any-llm.db \
  -e ANY_LLM_LOG_FILE=/data/logs/any-llm.log \
  -e ANY_LLM_MASTER_PASSWORD=your-password \
  -e ANY_LLM_SESSION_SECRET=$(openssl rand -hex 32) \
  --name any-llm \
  any-llm

# 查看容器日志
docker logs -f any-llm
```

## 配置

所有配置通过环境变量或 `.env` 文件设置（`.env` 不会覆盖已存在的环境变量）。

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `ANY_LLM_HOST` | `0.0.0.0` | 监听地址 |
| `ANY_LLM_PORT` | `6718` | 监听端口 |
| `ANY_LLM_DB_PATH` | `./any-llm.db` | SQLite 数据库路径 |
| `ANY_LLM_MASTER_PASSWORD` | `admin` | 管理员密码 |
| `ANY_LLM_SESSION_SECRET` | 随机生成 | 会话密钥，重启后丢失登录状态 |
| `ANY_LLM_LOG_FILE` | `./logs/any-llm.log` | 日志基础路径，实际写入 `{dir}/{日期}/{filename}`；留空仅输出到 stdout |
| `ANY_LLM_LOG_LEVEL` | `info` | 日志级别：`debug` / `info` / `warn` / `error` |

复制 `.env.example` 为 `.env` 并修改后重启服务即可。

## 使用

### 管理后台

访问 `http://localhost:6718`，使用管理员密码登录：

1. **Upstreams（上游服务）**：添加模型服务商，配置 API 地址、密钥、协议格式，支持自动拉取模型列表
2. **Keys（API 密钥）**：创建和管理外部 API Key，供客户端调用网关
3. **Usage（用量）**：查看各 Key 的 Token 消耗记录

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

- 模型格式为 `上游名称/模型名称`（如 `my-openai/gpt-4`）
- API Key 格式：`all-sk-` 前缀 + 32 位字符

### 可用端点

| 端点 | 说明 |
|------|------|
| `GET /v1/models` | 列出所有可用模型 |
| `POST /v1/chat/completions` | OpenAI 兼容聊天接口 |
| `POST /v1/messages` | Anthropic 兼容消息接口 |

## 开发

```bash
# 后端（终端 1）
go run ./cmd/any-llm/

# 前端（终端 2，HMR 开发服务器，代理到后端）
cd web && npm run dev
```

前端开发服务器默认代理 `/api` 和 `/v1` 到 `localhost:6718`。

```bash
# 运行测试
go test ./...
```

## 项目结构

```
cmd/any-llm/          # 入口，嵌入前端 dist
internal/
  auth/               # 会话认证（HMAC-SHA256）
  config/             # 环境变量加载
  db/                 # SQLite 初始化与迁移
  gateway/            # 公开 API 网关路由
  logger/             # slog 日志封装
  model/              # 数据模型与 CRUD
  translate/          # OpenAI ↔ Anthropic 格式翻译（IR 层）
  upstream/           # 上游 HTTP 客户端
  usage/              # 异步用量记录
  webapi/             # 管理后台 API
web/                  # Vue 3 前端（Naive UI + Vite）
```
