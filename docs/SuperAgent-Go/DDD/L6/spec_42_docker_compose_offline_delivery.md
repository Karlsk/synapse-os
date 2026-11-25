# spec 42 | 商业私有化双形态交付（非 root 安全镜像、单库 PG17 Compose 与离线 Tarball）— L6 交付工程规格

- **状态**：`draft`
- **篇号顺序**：42（L6 Server 装配、生命周期与商业交付层 · 第 2/2 篇，全套 42 篇收官篇）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` §3 看板中的 `SA-42`
- **预估工作量**：`1.5 人天`（单人可 Review）
- **上位契约**：
  - [01_Go工程架构_目录结构与核心底座设计.md](../../01_Go工程架构_目录结构与核心底座设计.md) §5（企业代理/私有 CA/镜像源）
  - [05_前端兼容策略_部署运维与分期研发实施路线.md](../../05_前端兼容策略_部署运维与分期研发实施路线.md) §2（双形态商业私有化交付：Docker Compose + 独立二进制发行包）、§3（M5 商业交付验收门）
  - [_epic/L6/spec_14_server_bootstrap_lifecycle_docker_delivery.md](../_epic/L6/spec_14_server_bootstrap_lifecycle_docker_delivery.md)（归档模块总设对照）
- **前置依赖**：
  - [L6/spec_41 (SA-41)](./spec_41_server_cli_wire_lifecycle.md)（`synapse-server` 单二进制 CLI：`serve`/`migrate`/`bootstrap-org`/`version`、`/actuator/health`、`/actuator/prometheus`、30s 优雅停机与 `configs/config.example.yaml`）
  - [L2/spec_19 (SA-19)](../L2/spec_19_platform_private_node_cli_installer.md)（容器内 `/opt/cli` 可写前缀与在线 `npm install -g` CLI 升级契约）
  - [L5/spec_38 (SA-38)](../L5/spec_38_web_gateway_mcp_ui_embed.md)（单二进制 `//go:embed` 静态 UI 嵌入）
- **下游消费者**：无（本篇为全仓最终商业化发布与客户现场 15 分钟交付验收入口）

---

## 1. 背景与范围

本篇定义 `synapse-os`（SuperAgent Go 企业私有化商业版）的 **双形态商业交付工程 (`synapse-build/`)**，完整等价重构并强化原版 Java `docker/` 与 `superagent-server/src/main/dist/` + `release-tgz.xml`：

1. **形态一：非 root 安全多阶段 Docker 镜像 + 单库 PG17 Compose (`synapse-build/docker/`)**：
   - **多阶段静态编译**：`golang:1.26-bookworm`（`CGO_ENABLED=0`，编译内嵌静态 UI 与 `goose` SQL 的单二进制）→ `node:24-bookworm-slim` 运行镜像（彻底移除原版 JRE/Maven 体积负担，冷启动从 90s 降至秒级）；
   - **非 root 安全运行**：固定 UID/GID `10001:10001`（`synapse`），满足 Claude Code 在 `uid=0` 下禁止免确认权限标志的安全要求（对齐 `docker/Dockerfile:L32-L35`）；
   - **僵尸进程回收与 CLI 在线升级**：`tini` 作为 PID 1 回收 CLI 子进程树；`NPM_CONFIG_PREFIX=/opt/cli` 属主为 `synapse:synapse`，预装 V1 GA 双 CLI（`@anthropic-ai/claude-code@latest` + `@openai/codex@latest`），并保障 Web 页面内「在线升级 CLI」具备持久化写入权限；
   - **企业网络深度适配**：`entrypoint.sh` 自动识别 `/app/certs/corp-ca.pem` 或 `SYNAPSE_CA_BUNDLE_FILE` 并导出 `NODE_EXTRA_CA_CERTS` + `SSL_CERT_FILE`；`docker-compose.yml` **仅编排 `postgres:17-alpine` + `synapse` 两个服务（零 Redis 容器、零 ClickHouse 容器）**。
2. **形态二：跨平台独立二进制离线发行包 (`synapse-build/dist/` + `scripts/package-tarball.sh`)**：
   - 产出 `synapse-os-<version>-linux-{amd64,arm64}.tar.gz`，内含单二进制、默认配置模板、环境文件与 POSIX 兼容启停脚本；
   - `start.sh` 免除 Java 运行时检查，通过 `setsid` 脱离终端后台启动并轮询 `/actuator/health` 直至 HTTP 200（`START_TIMEOUT=60`）；
   - `stop.sh` 发 `SIGTERM` 并等待 **`STOP_TIMEOUT=35s`**（严格大于 `SA-41` 服务端 30s 优雅停机宽限）后再兜底 `SIGKILL`，配合 `ps -p "$pid" -o command=` 二次校验防止 PID 复用误杀。
3. **商业私有化运维手册 (`synapse-build/README.md`)**：
   - 固化 15 分钟标准交付 SOP：Compose / Tarball 启动 → `bootstrap-org` 初始化首个租户与管理员 → 首登强制改密 → 配置企业代理/自签 CA/私有 npm 镜像 → 配置飞书/钉钉与 Prometheus 抓取 → 备份与升级回滚。

**本篇边界**：
- 不修改任何 Go 业务源码（Go 服务端入口、装配与探针已由 `SA-41` 冻结）；
- 不引入 Kubernetes Helm Chart、Redis、ClickHouse 或公网 SaaS 控制面脚本（超出 V1 企业单机/容器私有化交付范围）。

---

## 2. 本篇交付物表

| # | 交付物 | 落位 | 类型 | 对应 Java 参考源 |
|---|---|---|---|---|
| 1 | 多阶段商业私有化 `Dockerfile`（Go 1.26 → Node 24 + `uid=10001` + `tini` + 双 GA CLI） | `synapse-build/docker/Dockerfile` | 新增 | `docker/Dockerfile:L1-L84` |
| 2 | 容器前台入口脚本（自签 CA 注入 + 透传全部 CLI 子命令 `exec`） | `synapse-build/docker/entrypoint.sh` | 新增 | `docker/entrypoint.sh:L1-L12` |
| 3 | 单库 PG17 `docker-compose.yml`（仅 `postgres:17-alpine` + `synapse`，零 Redis） | `synapse-build/docker/docker-compose.yml` | 新增 | `docker/docker-compose.yml:L1-L53` |
| 4 | Compose 环境变量模板（含代理、镜像源、`SECRET_KEY`、`POSTGRES_PASSWORD`） | `synapse-build/docker/.env.example` | 新增 | `docker/.env.example:L1-L11` |
| 5 | 独立发行包公共函数库（路径定位、环境加载、PID 命令行校验、`stop_server`） | `synapse-build/dist/bin/common.sh` | 新增 | `superagent-server/src/main/dist/bin/superagent-common.sh:L1-L65` |
| 6 | 独立发行包启动脚本（支持 `start.sh [force]`、`setsid`、`/actuator/health` 60s 就绪探测） | `synapse-build/dist/bin/start.sh` | 新增 | `superagent-server/src/main/dist/bin/superagent-start.sh:L1-L80` |
| 7 | 独立发行包停机脚本（`SIGTERM` 35s 优雅等待 > 30s 服务端宽限） | `synapse-build/dist/bin/stop.sh` | 新增 | `superagent-server/src/main/dist/bin/superagent-stop.sh:L1-L8` |
| 8 | 独立发行包环境变量模板 | `synapse-build/dist/config/synapse.env` | 新增 | `superagent-server/src/main/dist/config/superagent.env:L1-L22` |
| 9 | 跨平台 Release Tarball 构建与 SHA-256 校验脚本（`linux/amd64`, `linux/arm64`） | `synapse-build/scripts/package-tarball.sh` | 新增 | `superagent-server/src/assembly/release-tgz.xml:L1-L58` |
| 10 | 商业私有化 15 分钟运维部署手册 | `synapse-build/README.md` | 新增 | 商业交付新增文档 |

---

## 3. 数据库设计

本篇不新增或变更任何数据库表结构；在交付编排层面对 PG17 施加如下硬约束：

1. **单库极简拓扑**：`docker-compose.yml` 仅包含 `postgres:17-alpine`（数据卷 `pg-data:/var/lib/postgresql/data`，强制 `TZ=UTC`，`pg_isready -U synapse -d synapse` 健康探针）与 `synapse`（`depends_on: postgres: condition: service_healthy`）；**严禁引入 Redis 或 ClickHouse 容器**。
2. **首次启动迁移策略**：容器与 Tarball 默认配置 `storage.auto_migrate: true`，应用启动时由 `SA-02`/`SA-41` 自动向前执行内嵌 `goose` 迁移；高安全客户亦可设 `SYNAPSE_AUTO_MIGRATE=false` 后先运行一次性 `docker compose run --rm synapse migrate`。
3. **备份与恢复约定**：仅需对单一 PG17 实例执行 `pg_dump -Fc` 以及打包 `/app/data`（租户工作区、附件、Skills、密钥文件）即可完成全量灾备。

---

## 4. 冻结契约

### 4.1 Docker 镜像安全、权限与健康契约

| 维度 | 冻结规范 | 理由 / Java 对齐 |
|---|---|---|
| **基础镜像** | Build: `golang:1.26-bookworm`；Runtime: `node:24-bookworm-slim` | 静态编译零 CGO；Node 24 运行 Claude/Codex CLI |
| **运行身份** | `USER synapse`（固定 `uid=10001`, `gid=10001`） | Claude Code 禁止在 `uid=0` 使用免确认权限标志（`docker/Dockerfile:L32-L35`） |
| **PID 1** | `ENTRYPOINT ["tini", "--", "/app/bin/docker-entrypoint.sh"]` | 防止 CLI 子进程树产生僵尸进程，正确转发 `SIGTERM` |
| **CLI 安装目录** | `NPM_CONFIG_PREFIX=/opt/cli`（属主 `synapse:synapse`），`PATH=/opt/cli/bin:/app/bin:$PATH` | 预装 `@anthropic-ai/claude-code@latest` + `@openai/codex@latest`，且允许 Web UI 在线升级 |
| **持久化卷** | `VOLUME ["/app/data", "/app/logs", "/home/synapse"]` | 保留租户文件、日志与 CLI 登录态/配置 |
| **自签 CA 挂载** | 只读挂载 `/app/certs/corp-ca.pem:ro` 或 `SYNAPSE_CA_BUNDLE_FILE` | `entrypoint.sh` 自动导出 `SYNAPSE_CA_BUNDLE_FILE`、`NODE_EXTRA_CA_CERTS`、`SSL_CERT_FILE` |
| **健康检查** | `HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3` 探测 `http://127.0.0.1:${SERVER_PORT}/actuator/health` | Go 秒级冷启动（较 Java 90s 缩减 3x） |

### 4.2 Compose 服务与环境变量契约

- `services` 列表严格限定为 **`postgres`** 与 **`synapse`**（共 2 个服务，**零 Redis**）；
- `POSTGRES_PASSWORD` 必须通过 `.env` 显式注入（`${POSTGRES_PASSWORD:?set POSTGRES_PASSWORD in .env}`，杜绝弱口令硬编码）；
- `NO_PROXY` 固定包含 `localhost,127.0.0.1,postgres`，防止容器内 `/mcp` 自检或数据库连接被企业代理误转发；
- `stop_grace_period: 35s`（严格大于 `synapse-server` 默认 `shutdown_timeout: 30s`），保障 4 阶段逆序停机完整落盘。

### 4.3 独立二进制 Tarball 目录与启停脚本契约

```text
synapse-os-<version>-linux-<arch>/
├── bin/
│   ├── synapse-server       # 静态编译单二进制（内嵌 UI + goose SQL）
│   ├── common.sh            # 0644 公共函数与环境加载
│   ├── start.sh             # 0755 启动与健康等待（支持 force 重启）
│   └── stop.sh              # 0755 35s 优雅停机
├── config/
│   ├── config.yaml          # 复制自 synapse-server/configs/config.example.yaml
│   └── synapse.env          # 0600 环境变量覆盖模板
├── data/                    # 租户数据、附件、工作区、密钥文件
└── logs/                    # console.out / synapse.log / synapse.pid
```

- **静检门禁**：全部 Shell 脚本（`dist/bin/*.sh`、`docker/entrypoint.sh`、`scripts/package-tarball.sh`）必须通过 `shellcheck` **零 Error / 零 Warning**；
- **停机预算倒挂防护**：`STOP_TIMEOUT=35` > 服务端 `shutdown_timeout=30s`；
- **PID 防误杀**：`running_pid()` 在向 `$pid` 发信号前必须校验 `ps -p "$pid" -o command=` 包含 `$APP_BIN`；
- **构建产物完整性**：`package-tarball.sh` 同步输出 `.tar.gz` 与 `.tar.gz.sha256` 校验文件。

### 4.4 商业私有化 15 分钟交付冒烟契约

客户现场（或 CI 验收环境）从零到可用必须稳定收敛于 4 步：
1. `cp .env.example .env` 并设置 `POSTGRES_PASSWORD` 与 32B Base64 `SECRET_KEY` → `docker compose up -d`；
2. `docker compose exec synapse synapse-server bootstrap-org --id acme --name "Acme 科技"` 获取一次性 14 位初始密码；
3. 访问 `http://<host>:18080/login` 确认无公网注册入口 → 首登强制改密 → 配置模型 Provider 或 CLI → 完成一次 Agent 对话与任务执行；
4. `docker compose stop -t 35 synapse` 确认日志完整输出 Phase 1–4 且进程以退出码 `0` 结束、无残留僵尸进程。

---

## 5. 行为语义与交付产物实现规格

### 5.1 多阶段安全 `synapse-build/docker/Dockerfile`

对应 Java：`docker/Dockerfile:L1-L84`（升级为 Go 1.26 多阶段静态构建，裁剪原版 10 CLI 预装为 V1 GA 双 CLI：Claude Code + Codex）。

```dockerfile
# Stage 1: Go 1.26.1 静态编译单二进制（内嵌静态 UI 与 goose SQL 迁移）
ARG GO_IMAGE=golang:1.26-bookworm
ARG NODE_IMAGE=node:24-bookworm-slim

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS builder
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG VERSION=1.0.0
ARG COMMIT=unknown
ARG BUILD_TIME=unknown
ARG GOPROXY=https://proxy.golang.org,direct

ENV GOPROXY=${GOPROXY} \
    CGO_ENABLED=0
WORKDIR /src/synapse-server
COPY synapse-server/go.mod synapse-server/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY synapse-server/ .
RUN --mount=type=cache,target=/root/.cache/go-build \
    GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath \
    -ldflags="-s -w -X main.Version=${VERSION} -X main.Commit=${COMMIT} -X main.BuildTime=${BUILD_TIME}" \
    -o /out/synapse-server ./cmd/synapse-server

# Stage 2: Node 24 运行镜像 + 非 root uid=10001 + tini + V1 GA 双 CLI
FROM ${NODE_IMAGE} AS runtime

ARG NPM_REGISTRY=
ARG CLI_REFRESH=unset
ARG HTTP_PROXY=
ARG HTTPS_PROXY=
ARG NO_PROXY=

ENV LANG=C.UTF-8 \
    NPM_CONFIG_PREFIX=/opt/cli \
    NPM_CONFIG_UPDATE_NOTIFIER=false \
    NPM_CONFIG_FUND=false \
    NPM_CONFIG_AUDIT=false \
    PATH=/opt/cli/bin:/app/bin:${PATH} \
    SERVER_PORT=18080

RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl git ripgrep tini procps less \
 && rm -rf /var/lib/apt/lists/*

# Claude Code 禁止在 root 下启用免确认权限标志，固定使用非 root 用户 synapse (10001:10001)
RUN groupadd --gid 10001 synapse \
 && useradd --uid 10001 --gid synapse --create-home --home-dir /home/synapse --shell /bin/bash synapse \
 && mkdir -p /opt/cli /app/bin /app/config /app/data /app/logs /app/certs \
 && chown -R synapse:synapse /opt/cli /app

USER synapse

# 可写 /opt/cli 前缀：保证容器内 Web 页面「在线升级 CLI」具备写入权限；V1 预装双 GA CLI
RUN echo "CLIs refresh=${CLI_REFRESH}" \
 && if [ -n "${NPM_REGISTRY}" ]; then npm config set registry "${NPM_REGISTRY}"; fi \
 && npm install -g \
      @anthropic-ai/claude-code@latest \
      @openai/codex@latest \
 && npm cache clean --force \
 && node --version && claude --version && codex --version

WORKDIR /app
COPY --from=builder --chown=synapse:synapse /out/synapse-server /app/bin/synapse-server
COPY --chown=synapse:synapse synapse-server/configs/config.example.yaml /app/config/config.yaml
COPY --chown=synapse:synapse --chmod=0755 synapse-build/docker/entrypoint.sh /app/bin/docker-entrypoint.sh

LABEL org.opencontainers.image.title="Synapse OS (SuperAgent Go)" \
      org.opencontainers.image.description="Commercial on-premises AI collaborative work platform"

VOLUME ["/app/data", "/app/logs", "/home/synapse"]
EXPOSE 18080

HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 \
  CMD curl -fsS "http://127.0.0.1:${SERVER_PORT}/actuator/health" > /dev/null || exit 1

ENTRYPOINT ["tini", "--", "/app/bin/docker-entrypoint.sh"]
CMD ["serve", "--config", "/app/config/config.yaml"]
```

### 5.2 容器入口脚本 `synapse-build/docker/entrypoint.sh`

对应 Java：`docker/entrypoint.sh:L1-L12`。

```sh
#!/bin/sh
set -eu
cd /app

# 企业内网自定义 CA 支持：挂载 /app/certs/corp-ca.pem 或通过 SYNAPSE_CA_BUNDLE_FILE 指定
if [ -n "${SYNAPSE_CA_BUNDLE_FILE:-}" ] && [ -f "${SYNAPSE_CA_BUNDLE_FILE}" ]; then
  export NODE_EXTRA_CA_CERTS="${SYNAPSE_CA_BUNDLE_FILE}"
  export SSL_CERT_FILE="${SYNAPSE_CA_BUNDLE_FILE}"
elif [ -f "/app/certs/corp-ca.pem" ]; then
  export SYNAPSE_CA_BUNDLE_FILE="/app/certs/corp-ca.pem"
  export NODE_EXTRA_CA_CERTS="/app/certs/corp-ca.pem"
  export SSL_CERT_FILE="/app/certs/corp-ca.pem"
fi

# 支持 docker compose run --rm synapse bootstrap-org ... / migrate / 默认 serve
exec /app/bin/synapse-server "$@"
```

### 5.3 单库 PG17 `synapse-build/docker/docker-compose.yml` 与 `.env.example`

对应 Java：`docker/docker-compose.yml:L1-L53`、`docker/.env.example:L1-L11`（仅 `postgres:17-alpine` + `synapse`，**零 Redis 容器**）。

```yaml
services:
  postgres:
    image: postgres:17-alpine
    restart: unless-stopped
    environment:
      POSTGRES_DB: synapse
      POSTGRES_USER: synapse
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:?set POSTGRES_PASSWORD in .env}
      TZ: UTC
    volumes:
      - pg-data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U synapse -d synapse"]
      interval: 5s
      timeout: 5s
      retries: 20

  synapse:
    image: ${SYNAPSE_IMAGE:-synapse-os:latest}
    restart: unless-stopped
    stop_grace_period: 35s
    depends_on:
      postgres:
        condition: service_healthy
    ports:
      - "${SYNAPSE_PORT:-18080}:18080"
    environment:
      SERVER_PORT: "18080"
      SYNAPSE_PG_DSN: "postgres://synapse:${POSTGRES_PASSWORD}@postgres:5432/synapse?sslmode=disable&TimeZone=UTC"
      SECRET_KEY: ${SECRET_KEY:-}
      SYNAPSE_BASE_URL: ${SYNAPSE_BASE_URL:-}
      SYNAPSE_COOKIE_SECURE: ${SYNAPSE_COOKIE_SECURE:-false}
      SYNAPSE_LOG_FILE: "/app/logs/synapse.log"
      SYNAPSE_PROMETHEUS_TOKEN: ${SYNAPSE_PROMETHEUS_TOKEN:-}
      HTTP_PROXY: ${HTTP_PROXY:-}
      HTTPS_PROXY: ${HTTPS_PROXY:-}
      NO_PROXY: "localhost,127.0.0.1,postgres"
      NPM_CONFIG_REGISTRY: ${NPM_CONFIG_REGISTRY:-}
      SYNAPSE_NODE_DIST_MIRROR: ${SYNAPSE_NODE_DIST_MIRROR:-https://nodejs.org/dist}
    extra_hosts:
      - "host.docker.internal:host-gateway"
    volumes:
      - synapse-data:/app/data
      - synapse-logs:/app/logs
      - synapse-home:/home/synapse
      # 企业自签根证书按需只读挂载：
      # - ./corp-ca.pem:/app/certs/corp-ca.pem:ro

volumes:
  pg-data:
  synapse-data:
  synapse-logs:
  synapse-home:
```

```dotenv
# synapse-build/docker/.env.example
SYNAPSE_IMAGE=synapse-os:latest
SYNAPSE_PORT=18080

# 必填：PostgreSQL 17 数据库密码（建议使用 openssl rand -hex 24 生成）
POSTGRES_PASSWORD=replace-with-strong-password

# 推荐：32 字节 Base64 主密钥（openssl rand -base64 32）；留空则自动生成在 /app/data/secret.key
SECRET_KEY=

# 企业访问域名与 HTTPS Cookie
SYNAPSE_BASE_URL=
SYNAPSE_COOKIE_SECURE=false

# 可选：Prometheus 独立抓取 Token
SYNAPSE_PROMETHEUS_TOKEN=

# 可选：企业出站代理与镜像源
HTTP_PROXY=
HTTPS_PROXY=
NPM_CONFIG_REGISTRY=
SYNAPSE_NODE_DIST_MIRROR=https://nodejs.org/dist
```

### 5.4 独立二进制发行包启停脚本 (`synapse-build/dist/bin/`)

对应 Java：`superagent-server/src/main/dist/bin/{superagent-common.sh,superagent-start.sh,superagent-stop.sh}` 与 `superagent-server/src/main/dist/config/superagent.env`。

```sh
# synapse-build/dist/bin/common.sh
#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
APP_HOME=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
APP_BIN="$APP_HOME/bin/synapse-server"
APP_CONFIG="$APP_HOME/config/config.yaml"
ENV_FILE="$APP_HOME/config/synapse.env"
PID_FILE="$APP_HOME/logs/synapse.pid"
CONSOLE_LOG="$APP_HOME/logs/console.out"

mkdir -p "$APP_HOME/logs" "$APP_HOME/data"
cd "$APP_HOME"

if [ -f "$ENV_FILE" ]; then
  set -a
  # shellcheck disable=SC1090
  . "$ENV_FILE"
  set +a
fi

SERVER_PORT="${SERVER_PORT:-18080}"
START_TIMEOUT="${START_TIMEOUT:-60}"
STOP_TIMEOUT="${STOP_TIMEOUT:-35}" # 严格大于服务端 30s 优雅停机宽限

running_pid() {
  [ -f "$PID_FILE" ] || return 1
  pid=$(cat "$PID_FILE" 2>/dev/null || true)
  [ -n "$pid" ] || return 1
  kill -0 "$pid" 2>/dev/null || return 1
  # 二次校验进程命令行，防止 PID 被操作系统复用导致误杀其他进程
  cmd=$(ps -p "$pid" -o command= 2>/dev/null || true)
  case "$cmd" in
    *"$APP_BIN"*|*"synapse-server"*) echo "$pid"; return 0 ;;
    *) return 1 ;;
  esac
}

stop_server() {
  pid=$(running_pid || true)
  if [ -z "$pid" ]; then
    rm -f "$PID_FILE"
    echo "synapse-server is not running."
    return 0
  fi

  echo "Stopping synapse-server (pid=$pid, grace=${STOP_TIMEOUT}s)..."
  kill -TERM "$pid" 2>/dev/null || true

  elapsed=0
  while [ "$elapsed" -lt "$STOP_TIMEOUT" ]; do
    if ! kill -0 "$pid" 2>/dev/null; then
      rm -f "$PID_FILE"
      echo "synapse-server stopped cleanly in ${elapsed}s."
      return 0
    fi
    sleep 1
    elapsed=$((elapsed + 1))
  done

  echo "Graceful shutdown timed out after ${STOP_TIMEOUT}s; sending SIGKILL to pid=$pid." >&2
  kill -KILL "$pid" 2>/dev/null || true
  rm -f "$PID_FILE"
}
```

```sh
# synapse-build/dist/bin/start.sh
#!/bin/sh
set -eu
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
# shellcheck source=./common.sh
. "$SCRIPT_DIR/common.sh"

MODE="${1:-}"
if pid=$(running_pid || true) && [ -n "$pid" ]; then
  if [ "$MODE" = "force" ]; then
    stop_server
  else
    echo "synapse-server is already running (pid=$pid). Use '$0 force' to restart." >&2
    exit 1
  fi
fi

if [ ! -x "$APP_BIN" ]; then
  echo "Binary not executable: $APP_BIN" >&2
  exit 1
fi

# 脱离终端会话后台启动，将 stdout/stderr 追加至 console.out
if command -v setsid >/dev/null 2>&1; then
  setsid "$APP_BIN" serve --config "$APP_CONFIG" >>"$CONSOLE_LOG" 2>&1 </dev/null &
else
  nohup "$APP_BIN" serve --config "$APP_CONFIG" >>"$CONSOLE_LOG" 2>&1 </dev/null &
fi
pid=$!
echo "$pid" >"$PID_FILE"
echo "Starting synapse-server (pid=$pid) on port $SERVER_PORT..."

elapsed=0
while [ "$elapsed" -lt "$START_TIMEOUT" ]; do
  if ! kill -0 "$pid" 2>/dev/null; then
    echo "synapse-server exited prematurely. Check $CONSOLE_LOG" >&2
    rm -f "$PID_FILE"
    exit 1
  fi
  if curl -fsS "http://127.0.0.1:${SERVER_PORT}/actuator/health" >/dev/null 2>&1; then
    echo "synapse-server is UP (pid=$pid, ${elapsed}s)."
    exit 0
  fi
  sleep 1
  elapsed=$((elapsed + 1))
done

echo "Timed out waiting for /actuator/health after ${START_TIMEOUT}s. Check $CONSOLE_LOG" >&2
exit 1
```

```sh
# synapse-build/dist/bin/stop.sh
#!/bin/sh
set -eu
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
# shellcheck source=./common.sh
. "$SCRIPT_DIR/common.sh"
stop_server
```

### 5.5 跨平台离线打包脚本 `synapse-build/scripts/package-tarball.sh`

对应 Java：`superagent-server/src/assembly/release-tgz.xml:L1-L58`。

```sh
#!/bin/sh
set -eu

VERSION="${VERSION:-1.0.0}"
ARCHES="${ARCHES:-amd64 arm64}"
COMMIT="${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo unknown)}"
BUILD_TIME="${BUILD_TIME:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
OUT_DIR="${OUT_DIR:-$ROOT_DIR/synapse-build/out}"
mkdir -p "$OUT_DIR"

for arch in $ARCHES; do
  PKG_NAME="synapse-os-${VERSION}-linux-${arch}"
  STAGE_DIR="$OUT_DIR/$PKG_NAME"
  rm -rf "$STAGE_DIR"
  mkdir -p "$STAGE_DIR/bin" "$STAGE_DIR/config" "$STAGE_DIR/data" "$STAGE_DIR/logs"

  (
    cd "$ROOT_DIR/synapse-server"
    CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath \
      -ldflags="-s -w -X main.Version=${VERSION} -X main.Commit=${COMMIT} -X main.BuildTime=${BUILD_TIME}" \
      -o "$STAGE_DIR/bin/synapse-server" ./cmd/synapse-server
  )

  cp "$ROOT_DIR/synapse-build/dist/bin/common.sh" "$STAGE_DIR/bin/common.sh"
  cp "$ROOT_DIR/synapse-build/dist/bin/start.sh"  "$STAGE_DIR/bin/start.sh"
  cp "$ROOT_DIR/synapse-build/dist/bin/stop.sh"   "$STAGE_DIR/bin/stop.sh"
  chmod 0644 "$STAGE_DIR/bin/common.sh"
  chmod 0755 "$STAGE_DIR/bin/start.sh" "$STAGE_DIR/bin/stop.sh" "$STAGE_DIR/bin/synapse-server"

  cp "$ROOT_DIR/synapse-server/configs/config.example.yaml" "$STAGE_DIR/config/config.yaml"
  cp "$ROOT_DIR/synapse-build/dist/config/synapse.env"      "$STAGE_DIR/config/synapse.env"
  chmod 0600 "$STAGE_DIR/config/synapse.env"

  TAR_FILE="$OUT_DIR/${PKG_NAME}.tar.gz"
  tar -C "$OUT_DIR" -czf "$TAR_FILE" "$PKG_NAME"
  (cd "$OUT_DIR" && sha256sum "${PKG_NAME}.tar.gz" > "${PKG_NAME}.tar.gz.sha256")
  rm -rf "$STAGE_DIR"
  echo "Built $TAR_FILE"
done
```

---

## 6. 任务清单（TDD / 验证驱动粒度）

- [ ] **T1（多阶段安全 `Dockerfile` 与 `entrypoint.sh`）**：编写 `synapse-build/docker/Dockerfile` 与 `entrypoint.sh`，校验 `shellcheck` 零告警；执行 `docker build` 验证镜像层大小、`id -u == 10001`、`/opt/cli` 属主可写、`tini` PID 1、`claude --version` 与 `codex --version` 正常输出。
- [ ] **T2（单库 PG17 `docker-compose.yml` 与 `.env.example`）**：编写 `synapse-build/docker/docker-compose.yml` 与 `.env.example`，执行 `docker compose config` 语法校验，确认仅含 `postgres` + `synapse`（零 Redis）、`stop_grace_period: 35s`、`NO_PROXY` 保底项与自签 CA 挂载示例。
- [ ] **T3（独立二进制发行包脚本与环境模板）**：编写 `synapse-build/dist/bin/{common.sh,start.sh,stop.sh}` 与 `dist/config/synapse.env`，运行 `shellcheck` 零告警，并编写离线启停冒烟脚本验证 `start.sh` 幂等保护、`start.sh force` 重启、`stop.sh` 35s 等待与 PID 命令行校验防误杀。
- [ ] **T4（跨平台 Tarball 构建脚本）**：编写 `synapse-build/scripts/package-tarball.sh`，运行产出 `linux/amd64` 与 `linux/arm64` 双包及 `.sha256` 校验文件，校验目录权限（`synapse.env` 为 `0600`、`bin/start.sh` 为 `0755`）。
- [ ] **T5（商业私有化 15 分钟运维部署手册与全链路验收）**：编写 `synapse-build/README.md`，按 §4.4 执行完整 15 分钟商业交付闭环实测。

---

## 7. 文档同步

- [ ] [DDD/README.md](../README.md) §3 看板：合入后将 `SA-42` 状态更新为 `已实施（commit <hash>）`；
- [ ] [05_前端兼容策略_部署运维与分期研发实施路线.md](../../05_前端兼容策略_部署运维与分期研发实施路线.md)：核对 Docker/Tarball 交付路径、环境变量表、挂载卷与 M5 交付验收清单 100% 同步；
- [ ] 产出 `synapse-build/README.md` 供客户实施工程师直接使用。

---

## 8. 测试清单

| # | 测试点 | 落位 | 关键断言 |
|---:|---|---|---|
| 1 | Shell 脚本静态检查 | CI / 本地 `shellcheck` | `shellcheck synapse-build/docker/entrypoint.sh synapse-build/dist/bin/*.sh synapse-build/scripts/package-tarball.sh` 零 Error、零 Warning |
| 2 | Compose 声明与零 Redis 校验 | CI / `docker compose config` | `docker compose -f synapse-build/docker/docker-compose.yml config --services` 精确输出 `postgres` 与 `synapse` 两行（零 Redis/ClickHouse） |
| 3 | Docker 镜像非 root 与双 CLI 冒烟 | CI / `docker build` + `docker run` | `id -u` 返回 `10001`；`test -w /opt/cli` 成功；`claude --version`、`codex --version`、`synapse-server version` 均返回退出码 `0` |
| 4 | 自签 CA 注入验证 | 容器启动冒烟 | 挂载临时 PEM 至 `/app/certs/corp-ca.pem:ro`，容器内 `env` 包含 `SYNAPSE_CA_BUNDLE_FILE`、`NODE_EXTRA_CA_CERTS`、`SSL_CERT_FILE` 指向 `/app/certs/corp-ca.pem` |
| 5 | 独立发行包构建与校验和 | `package-tarball.sh` | 产出 `amd64` + `arm64` `.tar.gz` 与 `.sha256` 且 `sha256sum -c` 通过；解压后目录与文件权限符合 §4.3 |
| 6 | Tarball 启停脚本与 PID 防误杀 | 本地/容器 Linux 冒烟 | `start.sh` 等待 `/actuator/health` 200 后返 `0`；二次调用 `start.sh` 报错拒绝重复启；`start.sh force` 平滑重启；`stop.sh` 在 35s 内优雅停止并清理 `synapse.pid` |
| 7 | 15 分钟商业交付端到端闭环 | Compose 全链路实测 | `up -d` → `bootstrap-org` → `/login` 无注册入口 → 强制改密 → 创建 Agent 对话/任务 → `/actuator/prometheus` 抓到 6 项 `superagent_*` → `stop -t 35` 零报错零僵尸进程 |

---

## 9. 验收门

- [ ] §2 交付物 10 项逐项存在且符合命名与权限规范
- [ ] `shellcheck` 对全部交付脚本 0 Error / 0 Warning
- [ ] `docker compose config` 校验通过，且仅含 `postgres:17-alpine` + `synapse` 两个服务（**严格零 Redis**）
- [ ] §8 测试清单 7 项冒烟与端到端闭环全部通过并留存验收日志
- [ ] 客户现场运维手册 `synapse-build/README.md` 已覆盖安装、开户、首登改密、代理/CA 配置、监控抓取、数据备份与版本升级全流程

---

## 10. 可提交节点

- 单 PR `SA-42` 合入，建议拆为 2 个原子提交：
  1. `build(docker): add non-root multi-stage Dockerfile, CA entrypoint and PG17-only docker-compose (spec 42, PR SA-42)`
  2. `build(release): add standalone tarball scripts, packaging pipeline and on-prem deployment guide (spec 42, PR SA-42)`
