# AI API 网关 Go 版 — 移植笔记

> 从 Node.js (E:\tools\qclaw\vps) 移植到 Go (E:\tools\qclaw\go)
> 移植日期: 2026-07-08

## 项目结构

```
E:\tools\qclaw\go\
├── main.go                      # 入口：路由注册 + CORS + 日志
├── ai-gateway.exe               # 编译产物 (~15.9 MB)
├── go.mod
├── data/                        # SQLite 数据目录（gitignore）
│   └── gateway.db
└── internal/
    ├── auth/auth.go             # 登录/登出/改密 + 登录限流
    ├── config/config.go         # 数据结构定义
    ├── db/db.go                 # SQLite KV 存储初始化
    ├── providers/providers.go   # 16个内建Provider + 智能路由
    ├── storage/storage.go       # Config/Session/Stats/Logs CRUD
    ├── utils/utils.go           # JSON/HTML/Cookie/密码哈希/Token生成
    ├── proxy/proxy.go           # 断路器/限流器/代理转发/auto模式
    ├── adapters/adapter.go      # OpenAI/Google/Cohere 协议适配器
    ├── api/api.go               # 配置/统计/Custom Provider CRUD
    └── web/web.go               # Vue 3 控制台（9个Tab）+ SIMPLE_MODE
```

## 核心功能清单

| 功能 | Node.js | Go |
|------|---------|----|
| 16个内建 Provider | ✅ | ✅ |
| UnifiedKey 认证 | ✅ | ✅ |
| Provider 配置 CRUD | ✅ | ✅ |
| Custom Provider | ✅ | ✅ |
| 模型列表拉取+缓存 | ✅ | ✅ |
| 智能路由(前缀匹配) | ✅ | ✅ |
| 断路器(5次/60s) | ✅ | ✅ |
| 速率限制(100/min) | ✅ | ✅ |
| Streaming SSE 透传 | ✅ | ✅ |
| 自动重试(2次) | ✅ | ✅ |
| 故障转移(failover) | ✅ | ✅ |
| auto 模式遍历 | ✅ | ✅ |
| 统计计数 | ✅ | ✅ |
| 请求日志 | ✅ | ✅ |
| 错误日志 | ✅ | ✅ |
| Provider 健康状态 | ✅ | ✅ |
| 多次失败指标 | ✅ | ✅ |
| Web 控制台(9Tab) | ✅ | ✅ |
| SIMPLE_MODE | ✅ | ✅ |
| 登录限流 | ✅ | ✅ |
| PBKDF2 密码哈希 | ✅ | ✅ |

## 2026-07-08 修复记录

### 移植时缺失的功能（已修复）
1. **路由**: Go `ServeMux` 不支持通配符，用 `mux.HandleFunc("/v1/", ...)` + `strings.HasPrefix` 实现
2. **模型前缀匹配**: 添加 gpt→openai, gemini→google 等前缀路由
3. **TimingSafeCompare**: 使用 `crypto/subtle.ConstantTimeCompare` 防时序攻击
4. **SIMPLE_MODE cookie**: `CreateSession` 返回值作为 cookie 值
5. **UnifiedKey 自动生成**: `GetConfig()` 首次调用时自动生成
6. **Web 控制台**: 完整重写为 9 个 Tab（原仅2个）
7. **Proxy streaming**: 添加 `StreamingProxyWithAdapter` + SSE 透传
8. **异常分类**: `classifyError` 函数（timeout/rate_limit/upstream/client_error）
9. **故障转移**: 使用 `ResolveProvider` 而非简单的第一个非排除 Provider

## 构建 & 运行

```powershell
# 构建
cd E:\tools\qclaw\go
go build -o ai-gateway.exe .

# 运行（SIMPLE_MODE 直接访问无需登录）
$env:PORT="4000"
$env:SIMPLE_MODE="true"
.\ai-gateway.exe

# 运行（完整模式需登录）
$env:PORT="4000"
$env:USERNAME="admin"
$env:PASSWORD="your-password"
.\ai-gateway.exe

# 环境变量
PORT          - 监听端口（默认 3000）
SIMPLE_MODE   - "true" 跳过登录验证
ALLOWED_ORIGIN - CORS 允许来源（默认 "*"）
DB_PATH       - SQLite 文件路径（默认 data/gateway.db）
```

## 端口说明
- Node.js 原版: 3000
- Go 版: 默认 3000（通过 `$env:PORT="4000"` 可改为 4000）

## 已知限制
- Streaming 模式下不统计 token 用量（与非 streaming 相同统计路径需额外实现）
- SIMPLE_MODE 下 Web 控制台为简化页面
