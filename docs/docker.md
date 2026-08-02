# Docker 部署指南

本仓库的 `docker` 分支配置了 GitHub Actions：每次 push 到 `docker` 分支都会自动构建镜像，并把镜像保存为**未压缩的 `.tar` 文件**（`docker save` 原始输出，不是 `.tar.gz`）上传到 Actions artifact，可直接下载。

## 1. 获取镜像

### 方式 A：从 CI 下载（推荐）

1. 打开 GitHub 仓库 → **Actions** → **Docker Build** 工作流
2. 选择最新一次运行（绿色 ✓），展开底部 **Artifacts**，下载 `any-llm-docker-image`
3. 解压下载的 zip（这是 GitHub 打包行为，内容本身是 tar），得到 `any-llm.tar`：

```bash
# 导入镜像（导出时未压缩，所以是 .tar 而非 .tar.gz）
docker load -i any-llm.tar

# 确认已导入
docker images | grep any-llm
```

镜像包含两个 tag：`any-llm:latest` 和 `any-llm:<commit-sha>`。

### 方式 B：本地构建

```bash
docker build -t any-llm:latest .
```

## 2. docker compose 运行（推荐）

仓库根目录已自带 `docker-compose.yml`：

```bash
# 在 .env 中设置管理员密码（可选，默认 admin）
echo 'ANY_LLM_MASTER_PASSWORD=your-password' >> .env

docker compose up -d
docker compose logs -f
```

启动后访问 `http://localhost:6718`。

`docker-compose.yml` 做了三件事：

- 端口映射 `6718:6718`（**应用实际监听 6718**）
- 数据持久化：SQLite 数据库和会话密钥存放在 named volume `any-llm-data`（挂载到容器 `/data`），容器重建不丢数据
- 日志走 stdout，用 `docker compose logs` 查看

## 3. docker run 直接运行

```bash
docker run -d --name any-llm \
  -p 6718:6718 \
  -v any-llm-data:/data \
  -e ANY_LLM_DB_PATH=/data/any-llm.db \
  -e ANY_LLM_SESSION_SECRET_FILE=/data/.session-secret \
  -e ANY_LLM_LOG_FILE= \
  -e ANY_LLM_MASTER_PASSWORD=your-password \
  --restart unless-stopped \
  any-llm:latest
```

## 4. 配置项参考

所有配置通过环境变量设置（镜像内没有 `.env` 文件；本地开发时程序会读工作目录的 `.env`，已存在的环境变量优先）。

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `ANY_LLM_HOST` | `0.0.0.0` | 监听地址 |
| `ANY_LLM_PORT` | `6718` | 监听端口（注意：Dockerfile 的 `EXPOSE 8080` 仅作声明，不影响实际端口） |
| `DB_TYPE` | `sqlite` | 数据库类型：`sqlite`（默认）或 `postgres` |
| `ANY_LLM_DB_PATH` | `./any-llm.db` | SQLite 文件路径，容器里建议放到挂载卷下 |
| `DB_HOST` / `DB_PORT` / `DB_USER` / `DB_PASSWORD` / `DB_NAME` / `DB_SCHEMA` | — | `DB_TYPE=postgres` 时的连接配置 |
| `ANY_LLM_MASTER_PASSWORD` | `admin` | 管理界面登录密码，**建议务必修改** |
| `ANY_LLM_SESSION_SECRET` | 空 | 会话密钥；为空时自动生成并持久化到 `ANY_LLM_SESSION_SECRET_FILE` |
| `ANY_LLM_SESSION_SECRET_FILE` | `./.session-secret` | 自动生成密钥的存放文件（容器里放到挂载卷下，否则重启登录失效） |
| `ANY_LLM_LOG_FILE` | `./logs/any-llm.log` | 日志文件路径；**容器里建议留空**，日志走 stdout 用 `docker logs` 查看 |
| `ANY_LLM_LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |

## 5. 使用 PostgreSQL（可选）

```yaml
services:
  any-llm:
    image: any-llm:latest
    ports:
      - "6718:6718"
    environment:
      DB_TYPE: postgres
      DB_HOST: postgres
      DB_PORT: "5432"
      DB_USER: postgres
      DB_PASSWORD: your-db-password
      DB_NAME: any_llm
      ANY_LLM_MASTER_PASSWORD: your-password
      ANY_LLM_LOG_FILE: ""
    depends_on:
      - postgres

  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: postgres
      POSTGRES_PASSWORD: your-db-password
      POSTGRES_DB: any_llm
    volumes:
      - pg-data:/var/lib/postgresql/data

volumes:
  pg-data:
```

## 6. 常见问题

**Q：下载的 artifact 是 zip？**
GitHub 打包 artifact 时会统一套一层 zip，这是 GitHub 行为；解压后里面就是 `.tar`。镜像导出阶段保证是未压缩 tar（`docker save` 输出，未经 gzip）。

**Q：为什么 `EXPOSE 8080` 但访问要 6718？**
`EXPOSE` 只是声明性信息；应用实际监听端口由 `ANY_LLM_PORT` 决定，默认 6718。映射 `8080:6718` 也可以从 8080 访问。

**Q：容器重启后登录失效？**
会话密钥被随机重新生成了。把 `ANY_LLM_SESSION_SECRET_FILE` 指向挂载卷（compose 文件已处理），或显式设置 `ANY_LLM_SESSION_SECRET`。

**Q：如何重新构建镜像？**
推一个新的提交到 `docker` 分支，或在 Actions 页面手动触发（Run workflow）。

## 7. CI 工作流说明

`.github/workflows/docker.yml` 在 push 到 `docker` 分支（或手动触发）时执行：

1. `docker/build-push-action` 多阶段构建（Vue 前端 + Go 后端 → alpine 运行镜像），带 GHA 层缓存
2. `docker save -o any-llm.tar` 导出为未压缩 tar
3. `file any-llm.tar` 校验导出格式
4. 上传 artifact `any-llm-docker-image`（保留 30 天）
