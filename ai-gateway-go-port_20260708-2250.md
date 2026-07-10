# AI API 网关 Node.js → Go 移植完成

**时间**: 2026-07-08 22:50
**目标路径**: `E:\tools\qclaw\go`
**API 端口**: 4000 (Go 版)，原 Node.js 版已占 3000
**产物**: ai-gateway.exe (15.9 MB)

## 项目结构

```
go/
├── main.go                          # 入口 + 路由分发 + CORS/日志中间件
├── go.mod / go.sum
└── internal/
    ├── config/config.go             # 所有数据类型定义 (Config, Provider, Stats 等)
    ├── db/db.go                     # SQLite 初始化 + KVAdapter (Get/Put/Delete/List)
    ├── utils/utils.go               # PBKDF2 哈希、Session、Cookie、掩码、datetime
    ├── storage/storage.go           # 持久层: config/session/stats/logs/latency/health/failures
    ├── auth/auth.go                 # 登录 / 登出 / 密码重置 + 登录限流
    ├── providers/providers.go       # 16 个内置 Provider 注册 + 搜索/分类/合并
    ├── adapters/adapter.go          # OpenAI / Google Gemini / Cohere 三套协议适配
    ├── proxy/proxy.go               # 代理转发 + 熔断器 + 限流器 + 自动 failover
    ├── api/api.go                   # 管理 API: config/keys/providers/stats/Responses API
    └── web/web.go                   # HTML 模板: 登录页 + Vue3 控制台仪表盘
```

## 核心架构

1. **存储**: pure Go SQLite (`modernc.org/sqlite`)，免 CGO，单 exe 部署
2. **密码**: PBKDF2-SHA256 (100k iterations)，对标 Node.js `pbkdf2.pbkdf2Sync`
3. **适配器**: OpenAI-compatible (直通) / Gemini (协议转换) / Cohere (协议转换)
4. **代理**: 熔断器 (5 次失败 → 60s 冷却) + 限流 (100/min) + 自动 failover (最多 2 次重试)
5. **前端**: 内嵌 Vue3 + TailwindCSS CDN，单 HTML 渲染 (登录页 + 控制台)

## 编译验证

- `go build -o ai-gateway.exe .` → 通过，0 错误 0 警告
- `/health` 端点 → 200 OK，JSON 响应正常
- 简单模式 (SIMPLE_MODE=true) 跳过登录，方便测试

## 后续待做

- 流式响应 (SSE) 代理 — 当前 Go 版仅支持非流式
- Google/Cohere 适配器的实际协议测试
- 原 VPS Node.js 版 `E:\tools\qclaw\vps` 的进程建议 kill 后在 4000 启动 Go 版
