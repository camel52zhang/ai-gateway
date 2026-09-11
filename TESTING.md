# AI Gateway —— 测试指南

本仓库同时支持**两层测试**：本机单元测试（快、验证逻辑）+ Docker 容器集成冒烟（验证生产级部署）。

## 1. 单元测试（本机 Go，无需 Docker）

本机已装 Go 1.26，直接运行：

```bash
go test ./... -count=1
```

覆盖包：`internal/adapters`、`internal/api`、`internal/auth`、`internal/proxy`、
`internal/storage`、`internal/utils`。
主要验证：代理故障转移 / 熔断 / 限流 / 流式回退 / Responses API 转换 / 客户端 IP 解析 /
恢复码重置。

> 测试用 `TestMain` 自建临时 SQLite，不污染真实 `data/gateway.db`，并清掉代理环境变量确保 httptest 上游直连。

## 2. 容器化集成冒烟（Docker）

适合验证「镜像能 build、容器能跑、健康检查过、Web/API 真实可用」。

```bash
# 构建并后台启动（自动建网络/命名卷 gateway-data，持久化 data）
cp .env.example .env          # 可选：改宿主机端口 / ALLOWED_ORIGIN / ADMIN_PASSWORD
docker compose up -d --build

# 查看健康状态（容器内 busybox wget 探 /health）
docker compose ps             # STATUS 应为 healthy
curl -i http://localhost:7000/health   # 期望 200

# 冒烟测试（登录页 / 鉴权 / 各 API / 静态资源）
# 需要传入实例的管理员密码；未设 ADMIN_PASSWORD 的实例会拒绝登录（403），
# 脚本会识别这种情况并提示如何初始化，而不是误报失败。
GW_TEST_PASSWORD=<你的密码> bash smoke-test.sh http://localhost:7000
```

停止 / 清理：

```bash
docker compose down           # 保留数据卷
docker compose down -v        # 连数据卷一起删
```

## 已知约定 / 踩坑

- 服务监听 `7000`（`main.go`），compose 映射 `${PORT:-7000}:7000`。
- `/api/models` **必须带 `?type=`**，缺参返回 `400` 是正常校验，非 bug。
- 登录用户固定为 `admin`。**未设 `ADMIN_PASSWORD` 且库中无密码哈希时登录返回 403**
  （不再是「任意密码即可进入」）；设 `ALLOW_FIRST_RUN_ANY_PASSWORD=1` 才恢复旧行为。
- 前端静态资源是**构建期产物**，改动后必须重新生成：
  - `static/tailwind.css` ← Tailwind CLI 扫描 `internal/web/web.go`（见 `.workbuddy/tailwind-build/README.md`）
  - `static/vue.global.prod.js` ← Vue 生产构建（非开发版）
- Windows Git Bash 下 `curl -o /tmp/...` 会静默写失败；`%{redirect_url}` 不加 `-L` 为空 —— `smoke-test.sh` 已规避。
