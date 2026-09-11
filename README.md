# AI Gateway (Go)

一个轻量的 **OpenAI 兼容 API 网关**，自带管理后台：统一管理多个 AI Provider（内置 + 自定义）、自动拉取上游模型、支持 `auto` 智能路由，并给下游客户端（Codex / WorkBuddy / Cursor 等）提供单一统一的接入端点与 API Key。

纯 Go 实现（modernc.org/sqlite，零 CGO），编译为静态二进制，运行在精简的 Alpine 镜像里。

---

## 功能特性

- **多 Provider 管理**：内置主流厂商 + 自定义 Provider（任意 OpenAI 兼容上游，填 `BaseURL` + `Key` 即可）
- **模型自动拉取**：添加 Provider 后自动拉取上游模型，并同步显示在「模型」模块与「测试」下拉
- **Auto 智能路由**：选 `auto` 模型，由网关按优先级自动挑选可用 Provider，带熔断 / 限流跳过 + 自动降级
- **统一 API Key**：下游客户端只需一个「统一 Key」即可访问所有已配置 Provider
- **管理后台**：6 个标签页（概览 / 提供商 / 模型 / 测试 / 日志 / 设置），含请求日志与健康检查
- **OpenAI 兼容接口**：`/v1/chat/completions`、`/v1/models`、`/v1/responses` 等，Bearer 鉴权
- **零外部依赖**：静态资源（Vue / Tailwind / Font Awesome）全部本地托管，无 CDN

---

## 快速开始（Docker Compose）

仓库根目录提供两份 Compose 文件，按场景二选一：

**本地开发（从源码构建）** —— `docker-compose.yml`：

```bash
cp .env.example .env        # 可选：改宿主机端口 / 跨域来源
docker compose up -d --build
# 浏览器打开 http://localhost:7000
```

**VPS / NAS 部署（免构建，直接拉 CI 镜像）** —— `vps-docker-compose.yml`：

```bash
# 只需拷贝 vps-docker-compose.yml 和 .env.example（改名 .env）到服务器
docker compose -f vps-docker-compose.yml pull
docker compose -f vps-docker-compose.yml up -d
# 之后升级：pull && up -d（数据在命名卷里，升级不丢）
```

VPS 版与本地版的差异：镜像固定为 `camel52zhang/ai-gateway:latest`（GitHub Actions push 后自动构建，CI 已跑全量测试）、无 `build` 段、并固化了公网安全约束（`ALLOW_FIRST_RUN_ANY_PASSWORD` 强制为 `0`，注释里附公网安全清单）。

首次运行会生成 `data/gateway.db`，数据通过命名卷 `gateway-data` 持久化（容器重建 / 升级不丢）。

---

## 首次登录与获取统一 Key

1. 在 `.env` 里设置 `ADMIN_PASSWORD=你的密码`；若已错过这一步，用 `docker compose run --rm ai-gateway --reset-password` 生成一个随机密码（会打印在终端）
2. 访问 `http://localhost:7000` → 跳转到 `/login`，用户名 `admin` + 上面设置的密码
3. 进入「设置」页复制 **统一 Key（Unified Key）** —— 下游客户端用它做鉴权
4. 顺手在「设置」页点一次 **生成恢复码**，把 10 个一次性恢复码存进密码管理器

> 为安全起见，**未配置密码的实例会拒绝登录**（不再是"任意密码即可进入"）。忘记密码时有三条本地恢复路径：
> `--reset-password`、`RESET_PASSWORD=1` + `ADMIN_PASSWORD`，或在登录页点「忘记密码？用恢复码」自助重置 —— 都不依赖短信或邮箱。

---

## 配置（环境变量）

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `PORT` | `7000` | 监听端口（**容器内固定 7000**，宿主机映射用 compose 的 `${PORT:-7000}`） |
| `IMAGE` | `ai-gateway:latest` | ⚠️ 已废弃：镜像名现由各自的 compose 文件固定（本地版构建 `ai-gateway:latest`，VPS 版拉取 `camel52zhang/ai-gateway:latest`），无需再通过环境变量切换 |
| `ALLOWED_ORIGIN` | 空（允许任意来源） | CORS 允许来源；生产建议设为你的前端域名，例如 `https://gw.example.com` |
| `ADMIN_PASSWORD` | 空 | 首次启动时用它初始化 `admin` 账号（**建议设置**）。未设置且库中无密码时，登录会被拒绝 |
| `RESET_PASSWORD` | `0` | 置 `1` 时本次启动会重置密码（用 `ADMIN_PASSWORD`；未设置则生成随机密码并打印到日志）。**用完请立即移除** |
| `ALLOW_FIRST_RUN_ANY_PASSWORD` | `0` | ⚠️ 置 `1` 时首次运行接受任意密码登录（旧的宽松行为）。**公网环境切勿开启** |
| `TRUST_PROXY` | `0` | 部署在 nginx 等反向代理后面时置 `1`，登录限流**与请求日志**才按真实客户端 IP 计数（否则所有请求都来自代理 IP，限流退化为全局、日志里也只剩代理地址） |
| `VERSION` | `dev` | 仅构建期生效，注入启动横幅打印的版本号：`VERSION=$(git rev-parse --short HEAD) docker compose build` |

数据持久化：命名卷 `gateway-data` 挂载到容器 `/app/data`（SQLite 数据库）。

---

## 作为 OpenAI 兼容端点使用（Codex / WorkBuddy / Cursor 等）

- **Base URL**：`http://<你的 host>:7000/v1`
- **API Key**：网关的**统一 Key**（从「设置」页复制）
- **模型**：直接选 **Auto**（或手填 `auto`）—— 网关按优先级自动路由并降级；也可直接选上游具体模型名

> 只要网关里至少有一个**带 Key** 的 Provider，`auto` 就会出现在 `/v1/models` 列表里。

---

## API 速览

| 方法 & 路径 | 说明 |
| --- | --- |
| `GET /health` | 健康检查 |
| `POST /auth/login` · `/auth/logout` | 登录 / 注销 |
| `POST /auth/recovery` | 用一次性恢复码重置密码（无需登录） |
| `POST /api/recovery/generate` | 生成 10 个一次性恢复码，明文仅返回一次（需登录） |
| `GET/POST /api/config` | 配置读写（含统一 Key） |
| `GET/POST/DELETE /api/providers/custom` | 自定义 Provider 增删查 |
| `GET /api/models?type=<id>` | 某 Provider 的模型列表 |
| `GET /v1/models` | OpenAI 兼容模型列表（含虚拟 `auto`） |
| `POST /v1/chat/completions` · `/v1/responses` | OpenAI 兼容对话 / Responses API（Bearer 统一 Key） |

---

## 本地构建镜像

本项目**只支持用 Docker Compose 运行**，本地与 VPS 完全一致；仓库不再提供直接运行二进制的脚本。

```bash
docker compose up -d --build          # 本地从源码构建并启动
docker build -t ai-gateway:latest .   # 或只构建镜像
```

> 使用纯 Go 版 SQLite（modernc.org/sqlite），构建时无需 gcc —— `CGO_ENABLED=0` 已在 `Dockerfile` 中设定。
> 单元测试仍可在宿主机运行（CI 也是这么跑的）：`CGO_ENABLED=0 go test ./...`，详见 `TESTING.md`。

---

## Docker Hub 镜像

已发布到 Docker Hub，可直接拉取使用：

```bash
docker pull camel52zhang/ai-gateway:latest
```

---

## CI / 自动构建（GitHub Actions）

仓库已配置 `.github/workflows/docker-publish.yml`：

- **push 到 `main`** → 跑 `go test ./...` + 构建并推送 `latest` 与 commit SHA 短标签
- **打 `v*.*.*` 标签** → 额外推送语义化版本标签
- 使用 Docker Hub 官方 action（`setup-buildx` / `login` / `metadata` / `build-push`），并启用 GitHub Actions 缓存加速

**使用前需在仓库 `Settings → Secrets and variables → Actions` 配置两个 Secret：**

| Secret | 说明 |
| --- | --- |
| `DOCKERHUB_USERNAME` | 你的 Docker Hub 用户名（如 `loveyou`） |
| `DOCKERHUB_TOKEN` | Docker Hub 个人访问令牌（PAT，非登录密码） |

---

## 目录结构

```
.
├── Dockerfile              # 多阶段构建（golang:1.26-alpine → alpine:3.20）
├── docker-compose.yml        # 本地部署配置（源码构建 + 命名卷 + 健康检查）
├── vps-docker-compose.yml    # VPS/NAS 部署配置（免构建拉镜像 + 公网安全清单）
├── docker-entrypoint.sh    # 修正数据目录属主后降权到 app 用户
├── .env.example            # 环境变量样例
├── main.go                 # 路由与启动入口（端口 7000）
├── internal/               # Go 业务代码（api / auth / proxy / web / db ...）
├── static/                 # 本地托管的 Vue / Tailwind / Font Awesome / favicon
├── webfonts/               # 字体文件
├── nginx-ai-gateway.conf   # VPS 上的 nginx 反代配置（宿主机侧使用）
├── smoke-test.sh           # 对已启动实例做冒烟测试
└── TESTING.md              # 测试指南（单元测试 + 容器冒烟）
```
