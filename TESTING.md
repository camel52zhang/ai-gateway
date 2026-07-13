# AI Gateway —— 测试指南

本仓库同时支持**两层测试**：本机单元测试（快、验证逻辑）+ Docker 容器集成冒烟（验证生产级部署）。

## 1. 单元测试（本机 Go，无需 Docker）

本机已装 Go 1.26，直接运行：

```bash
go test ./... -count=1
```

覆盖包：`internal/adapters`、`internal/api`、`internal/proxy`、`internal/storage`。
主要验证：代理故障转移 / 熔断 / 限流 / 流式回退 / Responses API 转换。

> 测试用 `TestMain` 自建临时 SQLite，不污染真实 `data/gateway.db`，并清掉代理环境变量确保 httptest 上游直连。

## 2. 容器化集成冒烟（Docker）

适合验证「镜像能 build、容器能跑、健康检查过、Web/API 真实可用」。

```bash
# 构建并后台启动（自动建网络/命名卷 gateway-data，持久化 data）
cp .env.example .env          # 可选：改宿主机端口 / ALLOWED_ORIGIN
docker compose up -d --build

# 查看健康状态（容器内 busybox wget 探 /health）
docker compose ps             # STATUS 应为 healthy
curl -i http://localhost:7000/health   # 期望 200

# 冒烟测试（登录页 / 鉴权 / 各 API / 静态资源）
bash smoke-test.sh http://localhost:7000
# 自定义首次登录密码：GW_TEST_PASSWORD=xxx bash smoke-test.sh
```

停止 / 清理：

```bash
docker compose down           # 保留数据卷
docker compose down -v        # 连数据卷一起删
```

## 已知约定 / 踩坑

- 服务监听 `7000`（`main.go`），compose 映射 `${PORT:-7000}:7000`。
- `/api/models` **必须带 `?type=`**，缺参返回 `400` 是正常校验，非 bug。
- 首次登录任意密码被哈希保存（默认用户 `admin`），数据存于命名卷，重建容器不丢。
- Windows Git Bash 下 `curl -o /tmp/...` 会静默写失败；`%{redirect_url}` 不加 `-L` 为空 —— `smoke-test.sh` 已规避。
