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

仓库根目录已提供 `docker-compose.yml` 与 `.env.example`：

```bash
cp .env.example .env        # 可选：改宿主机端口 / 跨域来源
docker compose up -d --build
# 浏览器打开 http://localhost:7000
```

首次运行会生成 `data/gateway.db`，数据通过命名卷 `gateway-data` 持久化（容器重建 / 升级不丢）。

---

## 首次登录与获取统一 Key

1. 访问 `http://localhost:7000` → 跳转到 `/login`
2. 用户名 `admin`，**首次用任意密码登录**后即被哈希保存（之后需用该密码）
3. 进入「设置」页复制 **统一 Key（Unified Key）** —— 下游客户端用它做鉴权

---

## 配置（环境变量）

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `PORT` | `7000` | 监听端口（**容器内固定 7000**，宿主机映射用 compose 的 `${PORT:-7000}`） |
| `ALLOWED_ORIGIN` | 空（允许任意来源） | CORS 允许来源；生产建议设为你的前端域名，例如 `https://gw.example.com` |

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
| `GET/POST /api/config` | 配置读写（含统一 Key） |
| `GET/POST/DELETE /api/providers/custom` | 自定义 Provider 增删查 |
| `GET /api/models?type=<id>` | 某 Provider 的模型列表 |
| `GET /v1/models` | OpenAI 兼容模型列表（含虚拟 `auto`） |
| `POST /v1/chat/completions` · `/v1/responses` | OpenAI 兼容对话 / Responses API（Bearer 统一 Key） |

---

## 从源码构建

```bash
# 本地直接运行
go build -o ai-gateway .
./ai-gateway                 # 默认监听 :7000

# 或构建 Docker 镜像
docker build -t ai-gateway:latest .
```

> 本项目使用纯 Go 版 SQLite，构建时无需 gcc；本地 `go build` 与 Docker 构建均使用 `CGO_ENABLED=0`。

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
├── docker-compose.yml      # 部署示例（App 容器 + 数据卷 + 健康检查）
├── .env.example            # 环境变量样例
├── main.go                 # 路由与启动入口（端口 7000）
├── internal/               # Go 业务代码（api / auth / proxy / web / db ...）
├── static/                 # 本地托管的 Vue / Tailwind / Font Awesome
└── webfonts/               # 字体文件
```
