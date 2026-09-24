# Docker 部署指南

本仓库的 `docker` 分支配置了 GitHub Actions：每次 push 到 `docker` 分支都会自动构建镜像，并把镜像保存为**未压缩的 `.tar` 文件**（`docker save` 原始输出，不是 `.tar.gz`）上传到 Actions artifact，可直接下载。每次构建的 tag 都是唯一的（`any-llm:<日期>-<短 sha>`），不产出会被互相覆盖的 `latest`。

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

镜像只有一个 tag：`any-llm:<构建日期>-<短 sha>`（如 `any-llm:2026.09.24-3e3a51a`）。每次构建都是新 tag，不会覆盖之前的镜像，也不会在导入时覆盖你本地已有的同名镜像；这次构建用的是哪个 tag，见该次运行页面的 step summary。

导入后如果要用下面的 compose 文件（它的 `image` 字段是 `any-llm:latest`），先手动 retag 一次：

```bash
docker tag any-llm:2026.09.24-3e3a51a any-llm:latest
```

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
- 数据持久化：SQLite 数据库、会话密钥和日志存放在 named volume `any-llm-data`（挂载到容器 `/data`），容器重建不丢数据
- 日志写入挂载卷 `/data/logs`（logger 同时输出到 stdout，`docker compose logs` 也能看）

## 3. docker run 直接运行

```bash
docker run -d --name any-llm \
  -p 6718:6718 \
  -v any-llm-data:/data \
  -e ANY_LLM_DB_PATH=/data/any-llm.db \
  -e ANY_LLM_SESSION_SECRET_FILE=/data/.session-secret \
  -e ANY_LLM_LOG_FILE=/data/logs/any-llm.log \
  -e ANY_LLM_MASTER_PASSWORD=your-password \
  --restart unless-stopped \
  any-llm:latest
```

## 4. 配置项参考

所有配置通过环境变量设置（镜像内没有 `.env` 文件；本地开发时程序会读工作目录的 `.env`，已存在的环境变量优先）。

镜像的最终 stage 用 `ENV` 预置了一整套默认值（照 `.env.example` 的值，数据库改为 PostgreSQL，并指向部署用的库与 schema）——下表的「默认值」列就是镜像里的值，`-e KEY=...` 可逐个覆盖。

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `ANY_LLM_HOST` | `0.0.0.0` | 监听地址 |
| `ANY_LLM_PORT` | `6718` | 监听端口（与 Dockerfile 的 `EXPOSE` 一致；`EXPOSE` 仅作声明，改这个值要同步改端口映射） |
| `DB_TYPE` | `postgres`（镜像预置）／`sqlite`（代码默认） | 数据库类型：`sqlite` / `postgres` / `mysql` |
| `ANY_LLM_DB_PATH` | `./any-llm.db` | SQLite 文件路径，容器里建议放到挂载卷下 |
| `DB_HOST` / `DB_PORT` / `DB_USER` / `DB_PASSWORD` / `DB_NAME` / `DB_SCHEMA` | `localhost` / `5432` / `chat_user` / 空 / `chat_db` / `any_llm` | `DB_TYPE=postgres` / `mysql` 时的连接配置（`DB_SCHEMA` 仅 PG 有效）。`DB_PASSWORD` 刻意不预置（镜像 ENV 用 `docker inspect` 就能看到），用 `-e DB_PASSWORD=...` 传入；换成你自己的实例用 `-e` 覆盖上面几个值（容器里的 `localhost` 是容器自身） |
| `ANY_LLM_MASTER_PASSWORD` | `admin` | 管理界面登录密码，**建议务必修改** |
| `ANY_LLM_SESSION_SECRET` | 镜像预置固定值 | admin 会话 cookie 的 HMAC 签名密钥。镜像里预置了一个固定值，也就是**所有用这份镜像的部署共用同一把密钥**，且它明文躺在仓库里 —— 对外提供服务的部署建议用 `-e ANY_LLM_SESSION_SECRET=$(openssl rand -hex 32)` 换成各自的随机值；覆盖为空串则自动生成并持久化到 `ANY_LLM_SESSION_SECRET_FILE` |
| `ANY_LLM_SESSION_SECRET_FILE` | `./.session-secret` | 自动生成密钥的存放文件（容器里放到挂载卷下，否则重启登录失效） |
| `ANY_LLM_LOG_FILE` | `./logs/any-llm.log` | 日志文件路径；容器里建议指向挂载卷，如 `/data/logs/any-llm.log`（logger 会按日期自动建子目录，同时输出到 stdout） |
| `ANY_LLM_LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |
| `TZ` | `Asia/Shanghai` | 不是应用变量，而是镜像的 `ENV` 默认值；日志按日轮转建目录、日/月额度窗口都按容器本地时间算，运行时可用 `-e TZ=UTC` 等覆盖 |

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

## 6. 使用 MySQL（可选）

```yaml
services:
  any-llm:
    image: any-llm:latest
    ports:
      - "6718:6718"
    environment:
      DB_TYPE: mysql
      DB_HOST: mysql
      DB_PORT: "3306"
      DB_USER: root
      DB_PASSWORD: your-db-password
      DB_NAME: any_llm
      ANY_LLM_MASTER_PASSWORD: your-password
      ANY_LLM_LOG_FILE: ""
    depends_on:
      - mysql

  mysql:
    image: mysql:8.0
    command:
      # 归档的 response_raw 单行可达 64 MiB，默认 64 MiB 的 packet 上限会写失败。
      - --max_allowed_packet=256M
      - --character-set-server=utf8mb4
      - --collation-server=utf8mb4_bin
    environment:
      MYSQL_ROOT_PASSWORD: your-db-password
      MYSQL_DATABASE: any_llm
    volumes:
      - mysql-data:/var/lib/mysql

volumes:
  mysql-data:
```

注意：需要 **MySQL 8.0.13+** —— schema 渲染器依赖 MySQL 对 TEXT/JSON 列的「表达式默认值」支持（`DEFAULT ('')`），8.0.13 之前的版本只接受裸字面量默认值，会报错误 1101。

注意：`DB_SCHEMA` 在 MySQL 下无效——MySQL 的 database 就是 schema，库名由 `DB_NAME` 给。启动时若设置了 `DB_SCHEMA`，日志会打一条 `db_schema_ignored`。表由 `OpenMySQL` 在启动时自动创建（幂等，可重复重启）。

## 7. 常见问题

**Q：下载的 artifact 是 zip？**
GitHub 打包 artifact 时会统一套一层 zip，这是 GitHub 行为；解压后里面就是 `.tar`。镜像导出阶段保证是未压缩 tar（`docker save` 输出，未经 gzip）。

**Q：改了 `ANY_LLM_PORT` 之后访问不到？**
`EXPOSE` 只是声明性信息（镜像里声明的是默认的 6718），应用实际监听端口由 `ANY_LLM_PORT` 决定。改了它必须同步改端口映射，例如 `ANY_LLM_PORT=9000` 就映射 `9000:9000`；反过来映射 `8080:6718` 也可以从 8080 访问。

**Q：导入镜像后 `docker compose up` 为什么在本地重新构建？**
CI 产出的 tag 是唯一的（`any-llm:<日期>-<短 sha>`），而 compose 的 `image` 字段写的是 `any-llm:latest`。本地没有这个 tag 时，compose 看到 `build: .` 会走本地构建 —— 慢，而且构建出的不一定是你下载的那份代码。先 `docker tag any-llm:<tag> any-llm:latest`，或把 compose 的 `image` 改成带 tag 的名字。

**Q：容器重启后登录失效？**
会话密钥被随机重新生成了。把 `ANY_LLM_SESSION_SECRET_FILE` 指向挂载卷（compose 文件已处理），或显式设置 `ANY_LLM_SESSION_SECRET`。镜像预置了固定密钥，所以直接用镜像启动不会遇到这个；把 `ANY_LLM_SESSION_SECRET` 覆盖成空串才会（那时才需要上面的办法）。

**Q：如何重新构建镜像？**
推一个新的提交到 `docker` 分支，或在 Actions 页面手动触发（Run workflow）。

## 8. CI 工作流说明

`.github/workflows/docker.yml` 在 push 到 `docker` 分支（或手动触发）时执行：

1. 计算本次构建的唯一 tag `any-llm:<UTC 日期>-<短 sha>`，写进该次运行的 step summary
2. `docker/build-push-action` 多阶段构建（Vue 前端 + Go 后端 → alpine 运行镜像），带 GHA 层缓存，只打这一个 tag
3. `docker save -o any-llm.tar` 把该 tag 导出为未压缩 tar
4. `file any-llm.tar` 校验导出格式
5. 上传 artifact `any-llm-docker-image`（保留 30 天）
