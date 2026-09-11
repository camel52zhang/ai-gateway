# AI Gateway 技术文档（操作与使用手册）

> 适用版本：v5（Go 实现）。本文档基于仓库代码撰写，路由、字段、环境变量均与源码一一对应。
> 最后一节附有完整排障索引，遇到问题可先查 §9。

---

## 目录

1. [项目简介与架构](#1-项目简介与架构)
2. [部署方式](#2-部署方式)
3. [环境变量与启动参数](#3-环境变量与启动参数)
4. [首次登录与账号体系](#4-首次登录与账号体系)
5. [控制台功能模块详解](#5-控制台功能模块详解)
6. [对外 API 使用指南](#6-对外-api-使用指南)
7. [安全模型](#7-安全模型)
8. [运维手册（备份 / 升级 / 监控）](#8-运维手册)
9. [排障 FAQ](#9-排障-faq)
10. [API 端点速查表](#10-api-端点速查表)

---

## 1. 项目简介与架构

AI Gateway 是一个**单用户自托管**的 LLM API 网关：把多家模型厂商（OpenAI、Gemini、Groq、OpenRouter、Ollama 等）聚合在**一个 OpenAI 兼容端点**后面，用一把「统一 Key」鉴权，并提供带健康检查、失败统计、请求日志的 Web 控制台。

**技术栈与关键设计：**

| 层 | 实现 | 说明 |
|---|---|---|
| 语言 | Go（静态编译，`CGO_ENABLED=0`） | 单二进制，无运行时依赖 |
| 存储 | SQLite（`modernc.org/sqlite` 纯 Go 驱动） | 单文件 `gateway.db`，存配置/会话/日志/统计 |
| 前端 | Vue 3（生产构建）+ 构建期 Tailwind CSS | 由 Go 程序内嵌渲染（`internal/web`），无独立前端服务 |
| 容器 | Alpine + su-exec 降权 | 运行用户 `app`(uid 100)，入口脚本自动修数据目录属主 |
| 资源占用 | 空闲 ~13MiB 内存 / 0% CPU | 最低配 VPS（512MB）可运行 |

**代码结构：**

```
main.go                  # 路由注册、中间件链、优雅关闭、--reset-password
internal/auth/           # 登录/登出/改密/恢复码、登录限流
internal/api/            # /api/* 管理接口、/v1/responses
internal/proxy/          # /v1/* 代理、流式转换、auto 模型路由、健康熔断
internal/adapters/       # 各厂商协议适配（openai/google/cohere）
internal/providers/      # 内置 Provider 目录（17 家）与分类
internal/storage/        # SQLite 读写、KV、会话、恢复码摘要
internal/web/            # 控制台 HTML/Vue 模板
internal/utils/          # ClientIP、Cookie、CORS、密码哈希等工具
```

---

## 2. 部署方式

### 2.1 本地开发（从源码构建）

```bash
git clone https://github.com/camel52zhang/ai-gateway.git
cd ai-gateway
cp .env.example .env            # 按需修改（见 §3）
docker compose up -d --build    # 构建并启动
# 浏览器打开 http://localhost:7000
```

带版本号构建（横幅里显示 commit）：

```bash
VERSION=$(git rev-parse --short HEAD) docker compose build
docker compose up -d
```

### 2.2 VPS / NAS（免构建，拉 CI 镜像）

只需两个文件：`vps-docker-compose.yml` + `.env.example`（改名为 `.env`）。

```bash
# 最少在 .env 里设置 ADMIN_PASSWORD
docker compose -f vps-docker-compose.yml pull
docker compose -f vps-docker-compose.yml up -d

# 之后升级（数据在命名卷里，不丢）：
docker compose -f vps-docker-compose.yml pull && \
docker compose -f vps-docker-compose.yml up -d
```

VPS 版与本地版的差异：镜像固定为 `camel52zhang/ai-gateway:latest`（GitHub Actions 在每次 push 后自动构建并跑全量 `go test`）、无 `build` 段、`ALLOW_FIRST_RUN_ANY_PASSWORD` 强制为 `0`（公网禁止宽松首登）。

**公网部署安全清单（逐条核对）：**

1. `.env` 里**必须**设置 `ADMIN_PASSWORD`——公网上首次运行若不设密，任何人都可能「占位」接管。
2. 放 nginx/Caddy 反代后面并启用 HTTPS；此时必须 `TRUST_PROXY=1`（见 §3，否则限流会退化为全局）。
3. 反代与本容器同机时，把 `vps-docker-compose.yml` 的 `ports` 改成 `"127.0.0.1:${PORT:-7000}:7000"`，网关完全不暴露公网、仅反代可达。
4. 防火墙/安全组只放行必要端口。

### 2.3 裸二进制（不用 Docker）

```bash
CGO_ENABLED=0 go build -o ai-gateway .
PORT=7000 ADMIN_PASSWORD=你的密码 ./ai-gateway
# 数据库默认写在 ./data/gateway.db，可用 DB_PATH 改路径
```

---

## 3. 环境变量与启动参数

| 变量 | 默认值 | 说明 |
|---|---|---|
| `PORT` | `7000` | 监听端口。**容器内固定 7000**，宿主机换端口用 compose 的 `PORT` 映射 |
| `ADMIN_PASSWORD` | 空 | 首次启动用它初始化 `admin` 账号。**未设置且库中无密码时登录被拒绝（403）**，这是有意的安全默认 |
| `ALLOWED_ORIGIN` | 空（任意来源） | CORS 允许来源。单机/本地可留空；对外服务建议设为你的前端域名。注意：网关**不支持跨域携带 Cookie 凭据**（与 `SameSite=Strict` 的安全默认一致） |
| `TRUST_PROXY` | `0` | 置 `1` 表示信任前置代理的 `X-Real-IP` / `X-Forwarded-For`。**nginx 等反代后必须开启**，否则登录限流按代理 IP 全局计数（10 次失败即可锁死管理员），请求日志里也只剩代理地址 |
| `RESET_PASSWORD` | `0` | 置 `1` 时本次启动重置密码为 `ADMIN_PASSWORD`（未设置则生成随机密码打印到日志）。**用完立即移除**，否则每次重启都会重置 |
| `ALLOW_FIRST_RUN_ANY_PASSWORD` | `0` | ⚠️ 危险开关：置 `1` 恢复旧的「任意密码首登」行为。仅用于无 `.env` 能力的受限场景；公网严禁开启 |
| `DB_PATH` | `data/gateway.db` | SQLite 数据库文件路径（裸跑时可自定义；容器内固定 `/app/data/gateway.db`） |
| `WAL_CHECKPOINT_SECONDS` | 内置默认 | WAL 检查点周期（秒）。程序会先探针验证 WAL 可用，不可用时自动回退 DELETE 日志模式，无需干预 |

**CLI 参数（仅裸跑二进制）：**

```bash
./ai-gateway --reset-password
# 生成一个 20 位随机密码打印到终端后退出，不影响正在运行的容器/进程
# 若同时设置了 ADMIN_PASSWORD，则改用该值重置（不打印）
```

---

## 4. 首次登录与账号体系

**登录用户名固定为 `admin`**（单用户设计，没有多账号）。

### 4.1 首次初始化（推荐顺序）

1. 启动前在 `.env` 设置 `ADMIN_PASSWORD=你的密码` → 启动即完成初始化。
2. 已错过这一步？登录会被拒绝（403，页面会提示实例未初始化）。任选其一补救：
   - 改 `.env` 加 `ADMIN_PASSWORD` 后重启容器；
   - `docker compose run --rm ai-gateway --reset-password` → 生成随机密码打印在终端；
   - `.env` 设 `ADMIN_PASSWORD=...` + `RESET_PASSWORD=1`，重启一次，**用完立即移除**。

### 4.2 忘记密码的三条本地恢复路径

| 路径 | 操作 | 特点 |
|---|---|---|
| ① `--reset-password` | `docker compose run --rm ai-gateway --reset-password` | 生成随机密码并打印，改完即退出，不影响运行中的容器 |
| ② 环境变量重置 | `ADMIN_PASSWORD=新密码` + `RESET_PASSWORD=1`，重启一次 | 简单直接；**改完必须移除** |
| ③ 离线恢复码 | 登录页点「忘记密码？用恢复码」输入一个未使用的码 | 不需要进服务器，每个码一次性 |

三条路径都会**失效所有已存在的登录会话**（包括其它浏览器/设备的会话），并给你发一个新会话——避免「重置了密码旧会话还在」的隐患。

### 4.3 恢复码机制（建议首次登录就配置）

- 在控制台 **设置 → 离线恢复码** 点「生成新的恢复码」，一次生成 10 个；
- **明文只显示这一次**，离开页面后无法再查看，服务端只存 SHA-256 摘要；
- 重新生成会**覆盖作废**整组旧码；剩余数量显示在按钮旁；
- 每个码只能消费一次，用掉即废。

---

## 5. 控制台功能模块详解

登录后进入 `http://<host>:7000/`，共 6 个标签页。

### 5.1 概览（Overview）

- **统一 API 密钥**：一把对所有下游客户端生效的 Key（即 OpenAI SDK 里的 `api_key`）。一键复制；页面右上角「重置密钥」按钮可轮换（**轮换后旧 Key 立即失效**，需要更新所有客户端）。
  同时展示调用端需要的 **Base URL**（`http://<host>:7000/v1`）与对话端点（`/v1/chat/completions`）。
- **用量统计卡**：总请求数 / 总 Token / Prompt Tokens / Completion Tokens（累计值，重启不清零，存在库里）。
- **系统健康摘要**：整体状态（健康/降级/熔断）、已配置 Provider 数、累计失败数、最新窗口请求量。
- **Provider 健康状态**：每个已配置 Provider 一行——健康点（绿=健康 / 黄=降级 / 红=熔断打开）、最近延迟 ms、暂停状态标记。熔断说明见 §5.2。
- **失败统计**：按 Provider 汇总的失败次数，并按类别细分（超时 / 限流 / 上游错误 / 客户端错误 / 未知）。

### 5.2 提供商（Providers）

**内置 Provider 目录**（按分类过滤 + 搜索）：

- 分类：`official`（OpenAI、Google Gemini、Groq、Cohere、Mistral、xAI）、`enterprise`（Cerebras、NVIDIA）、`aggregator`（OpenRouter、Routeway、BazaarLink）、`local`（Ollama）、`community`（Pollinations、Kilo AI、Agnes AI、AI Native）。
- 选中某个 Provider 后显示：官网 / 文档 / **获取 Key** 直达链接、BaseURL，以及密钥输入框。

**接入一个内置 Provider 的完整流程：**

1. 列表中点击目标 Provider（高亮选中）；
2. 粘贴该厂商的 API Key → 点「添加密钥」（已有则显示「更新密钥」）；
3. 网关自动用该 Key 调上游 `/models` 拉取模型列表，模型出现在「模型」标签页；
4. 已配置的 Provider 出现在下方列表：显示密钥尾 4 位、可**暂停/恢复**、可**移除**。

**暂停（Pause）的语义**：暂停后该 Provider 的所有模型不再进入 `/v1/models` 列表、不参与 `auto` 路由、显式请求也不可用——但配置和 Key 保留，恢复即回来。适合某家欠费/故障时临时摘除。

**自定义 Provider**（页面下半部分，黄色区块）：

- 点「添加」，填：**ID**（唯一标识，如 `my-relay`）、**显示名称**、**Base URL**、**适配器**（OpenAI 兼容 / Google Gemini / Cohere）、**优先级**（1-100，影响 auto 路由顺序）、**模型列表**（逗号分隔，可不填——留空且有 Key 时会尝试拉取）、**API Key**（可选）。
- **Base URL 拼装规则**（不自动注入 `/v1`，原样拼接）：填 `https://api.example.com/v1` → 请求 `https://api.example.com/v1/chat/completions`；填裸域名 `https://api.example.com` → 请求 `https://api.example.com/chat/completions`。请按你的上游实际路径填写。
- 每个 Provider 卡片可：补/换 Key、**编辑**（除 ID 外全字段）、暂停/恢复、删除（**应用内二次确认**：点垃圾桶后变「确认? / 取消」，防误删）。

**健康/熔断机制**：代理请求失败会被分类记录（超时/限流/上游错误/客户端错误/未知）；连续失败达到阈值后该 Provider 进入「熔断打开」状态，一段时间内不再被路由选中（概览页显示红色），期间请求会自动落到其它可用 Provider 或直接报错。恢复探测成功后自动回到健康。

### 5.3 模型（Models）

- **模型状态表**：所有已配置 Provider（内置 + 自定义）的模型汇总，每行显示模型名 / 所属 Provider / 状态（可用/已隐藏）/ 启用开关。
- **启用开关（toggle）**：点击隐藏或启用某模型。**隐藏是三级隔离的**：
  1. `/v1/models` 不再列出；
  2. `auto` 路由不再选中它；
  3. 即使显式指定模型名请求，也返回 404——对下游来说它彻底不存在。
- **显示已隐藏模型**：勾选后表格会列出被隐藏的模型，方便重新开启。
- **刷新全部**：重新对所有 Provider 拉取模型列表（新增 Provider 或上游上新后使用）。

### 5.4 测试（Playground）

不写代码即可验证模型通路：

- **选择模型**：可选 **🚀 Auto**（由网关自动挑选当前可用模型）或按 Provider 分组列出所有已启用模型；
- **参数**：System Prompt、User Message、Temperature（0-2）、Max Tokens（1-32000）、Top P（0-1）；
- **Stream 开关**：开启后走 SSE 流式输出，实时显示；若模型返回思考过程（reasoning），单独显示在「思考过程」区块；
- **响应区**：输出文本 + 统计卡（Tokens、Prompt/Completion、延迟）。

> 测试请求走的是与生产完全相同的代理链路（含统一 Key 鉴权），所以「测试通了 = 客户端能用」。

### 5.5 日志（Logs）

两栏，都支持筛选：

- **最近请求**：模型 / Provider / tokens / 耗时 ms / 时间 / requestId。筛选：时间（全部/1h/6h/24h/7d）+ Provider。
- **错误日志**：Provider / 模型 / HTTP 状态码（4xx 黄色、5xx 红色）/ 错误正文摘要 / 分类标签 / requestId。筛选：时间 + Provider + **错误分类**（超时/限流/上游错误/客户端错误/未知）+ **状态码段**（4xx/5xx/其他）。
- `requestId` 用于把一次请求在「最近请求 / 错误日志 / 容器标准输出日志」之间对齐，排查问题时先拿它。

### 5.6 设置（Settings）

- **账号安全**：修改登录密码（当前密码 + 新密码 ≥6 位 + 确认）。保存后**其它所有会话立即失效**（本会话保留）。
- **会话信息**：登录用户 `admin`、会话有效期 **24 小时**、密码存储算法 **PBKDF2-SHA256（10 万轮）**；退出登录按钮。
- **离线恢复码**：见 §4.3。

---

## 6. 对外 API 使用指南

所有下游客户端只见网关一个端点，协议为 **OpenAI 兼容**。

### 6.1 鉴权：统一 Key

- 请求头带 `Authorization: Bearer <统一Key>`（就是 OpenAI SDK 的 `api_key` 参数）；
- 统一 Key 在「概览」页查看/复制；点页面右上角「重置密钥」轮换（**轮换后旧 Key 立即失效**，需要更新所有客户端）；
- 无 Key / 错 Key 返回 `401 Unauthorized`。

### 6.2 对话补全 `/v1/chat/completions`

```bash
curl http://localhost:7000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <统一Key>" \
  -d '{
    "model": "gpt-4o-mini",
    "messages": [{"role": "user", "content": "你好"}],
    "stream": true
  }'
```

- **模型名格式**：直接用模型裸名（如 `gpt-4o-mini`）。若多个 Provider 提供同名模型，按优先级（自定义 Provider 的 priority / 内置默认值）路由。
- **流式**：`"stream": true` 返回标准 SSE `data:` 帧。若某上游忽略 stream 参数返回了单个 JSON，网关会自动把它包装成 SSE 帧，下游始终看到一致的流式协议。
- **非 OpenAI 兼容上游**（Google Gemini、Cohere）由适配器做双向协议转换，下游无感。

### 6.3 自动路由 `model: "auto"`

```json
{ "model": "auto", "messages": [...] }
```

网关在所有**可用**候选（已配置 Key、未暂停、未被熔断、模型已启用）里按优先级挑选并转发。适合「不在乎用哪家，能用就行」的客户端。注意：`auto` 是一个**虚拟模型名**，会出现在 `/v1/models` 列表里方便客户端发现；当所有 Provider 都暂停时它会自动从列表消失且不可调用。

### 6.4 模型列表 `/v1/models`

```bash
curl http://localhost:7000/v1/models -H "Authorization: Bearer <统一Key>"
```

返回所有**当前可用**的模型（已配置 + 未暂停 + 已启用；被隐藏的模型不会出现）。

### 6.5 Responses API `/v1/responses`

支持 OpenAI 新版 Responses API（POST），同样用统一 Key 鉴权，路由逻辑与 chat/completions 一致。

### 6.6 客户端接入示例

任何支持「自定义 OpenAI Base URL」的客户端（LobeChat / NextChat / Cherry Studio / 各类 SDK）都可直接接入：

```
API Base URL: http://<host>:7000/v1
API Key:      <概览页的统一Key>
模型:         手动填，或拉取 /v1/models 自动获取
```

---

## 7. 安全模型

| 层面 | 机制 |
|---|---|
| 控制台认证 | 单用户 `admin`；Session Cookie（`HttpOnly`、`SameSite=Strict`、Secure 视 TLS 而定），有效期 24h |
| 密码存储 | PBKDF2-SHA256，100,000 轮；明文/可逆形式从不落库 |
| 登录限流 | 按客户端 IP 计数（读 `utils.ClientIP`：优先 `X-Real-IP`，其次 XFF 最后一跳，否则 socket 对端去端口）；连续失败达阈值后锁定一段时间。**反代后必须 `TRUST_PROXY=1`**，否则按代理 IP 全局限流，敌手 10 次失败即可把管理员锁在门外 |
| 首登安全 | 未初始化（库中无密码）时拒绝登录，杜绝公网「占位设密」；`ALLOW_FIRST_RUN_ANY_PASSWORD` 是仅限内网的逃生门 |
| 恢复码 | 只存 SHA-256 摘要、一次性、整组覆盖式再生成 |
| 会话失效 | 改密 / 恢复码重置都会失效全部旧会话 |
| CORS | 默认不回 `Allow-Credentials`（与 SameSite=Strict 一致）；来源由 `ALLOWED_ORIGIN` 控制 |
| 容器 | 非 root 运行（app, uid 100）、`no-new-privileges`、`init: true` 转发信号、日志滚动（10MB × 3） |
| 传输安全 | 由部署层负责：公网务必走 HTTPS 反代 |

---

## 8. 运维手册

### 8.1 数据与备份

所有持久化数据在一个 SQLite 文件里：

```bash
# Docker 部署：数据在命名卷 gateway-data（容器内 /app/data/gateway.db）
# 快速备份（在线安全，SQLite 一致性由引擎保证）：
docker compose exec -T ai-gateway sh -c 'cat /app/data/gateway.db' > backup-$(date +%F).db

# 恢复：停容器 → 把文件放回 /app/data/（属主 100:101）→ 起容器
# 入口脚本会自动 chown，通常无需手动处理
```

### 8.2 升级

```bash
# VPS/NAS（推荐）
docker compose -f vps-docker-compose.yml pull && \
docker compose -f vps-docker-compose.yml up -d

# 本地源码
git pull && docker compose up -d --build
```

升级不丢数据（命名卷）；优雅关闭：`docker stop` 发 SIGTERM 后网关排空在途请求（含长流式）再退出，`stop_grace_period: 15s`。

### 8.3 健康检查与监控

- `GET /health` → 200（附运行状态 JSON；DB 读异常时仍返 200 并带 `dbError` 标记，供探针与人工区分）；
- compose 自带 healthcheck（30s 间隔，wget 探测），`docker ps` 显示 `healthy/unhealthy`；
- 编排系统（群晖/Unraid/watchtower 等）可直接依赖 healthcheck 状态做自愈。

### 8.4 日志

- 容器标准输出：启动横幅（版本号 / DB 路径 / journal 模式）、请求日志（时间、方法、路径、状态、耗时、真实客户端 IP、requestId）、错误详情；
- compose 已配置 json-file 滚动（max 10MB × 3 个文件），不会撑爆磁盘；
- 查看：`docker compose logs -f ai-gateway`；定位单次请求用 requestId。

### 8.5 资源画像（实测参考）

空闲 ~13MiB 内存 / 0% CPU；镜像 34MB；数据库初始 <100KB（按日志量缓慢增长）。512MB 内存的 VPS 足够。

---

## 9. 排障 FAQ

**Q1：登录页提示无法登录 / 返回 403？**
实例未初始化（未设 `ADMIN_PASSWORD` 且库中无密码）。按 §4.1 三选一设置密码。这不是 bug，是有意的安全默认。

**Q2：`/health` 正常但登录一直 429？**
限流被锁。两种触发：① 密码连输错多次；② 在 nginx 后没开 `TRUST_PROXY=1`，所有访客共享代理 IP 的限流额度。开启 `TRUST_PROXY` 并重启即可（等锁定期过或重启容器清空内存限流表）。

**Q3：新增了 Provider/模型但客户端看不到？**
检查顺序：Provider 是否暂停 → 模型是否被隐藏（模型页勾选「显示已隐藏模型」查看）→ 控制台「模型」页点「刷新全部」→ `/v1/models` 确认 → 客户端重新拉取。

**Q4：显式请求某模型返回 404，但列表里其它模型正常？**
该模型被隐藏（三级隔离：列表/auto/显式请求全不可用）。到「模型」页重新启用。

**Q5：容器 `unhealthy` / `/health` 500？**
看容器日志首行横幅下的 journal 模式：若曾是 WAL 相关报错（`out of memory (14)`），新版已自动探针回退 DELETE 模式，通常属主/权限问题——确认 `/app/data` 属主为 `100:101`（入口脚本会自愈，除非挂载本身只读）。

**Q6：流式响应乱码/一次性吐出？**
下游客户端需按 SSE 解析（按 `\n` 切帧）。若自写客户端，确认逐行读取 `data:` 帧。

**Q7：反向代理后请求日志里全是同一个 IP？**
`TRUST_PROXY` 未开启，见 Q2；同时确认反代确实重写了 `X-Real-IP`/`X-Forwarded-For`。

**Q8：忘记密码且没生成过恢复码？**
`docker compose run --rm ai-gateway --reset-password`（§4.2 路径①），不需要进容器。

**Q9：想彻底重置（含配置）？**
⚠️ 会丢掉所有 Provider/Key/统计：`docker compose down -v`（删除命名卷）后重新初始化。一般密码问题用 §4.2 即可，**不要**动 `-v`。

---

## 10. API 端点速查表

**公开（无需登录）：**

| 端点 | 方法 | 用途 |
|---|---|---|
| `/health` | GET | 健康检查 |
| `/login` | GET | 登录页 |
| `/auth/login` | POST | 登录（JSON：username/password） |
| `/auth/recovery` | POST | 恢复码重置密码 |
| `/v1/chat/completions` | POST | 对话补全（统一 Key，OpenAI 兼容，支持流式） |
| `/v1/models` | GET | 模型列表（统一 Key） |
| `/v1/responses` | POST | Responses API（统一 Key） |
| `/v1/*` | * | 其余路径按 OpenAI 兼容代理转发 |

**控制台会话认证：**

| 端点 | 方法 | 用途 |
|---|---|---|
| `/` | GET | 控制台（未登录 302 → `/login`） |
| `/auth/logout` | POST | 退出登录 |
| `/auth/reset-password` | POST | 修改密码（需当前密码） |
| `/api/config` | GET/POST | 读/写全量配置（Provider、Key、模型、统一 Key） |
| `/api/key/regenerate` | POST | 轮换统一 Key |
| `/api/recovery/generate` | POST | 生成新一组恢复码 |
| `/api/providers` | GET | 内置 Provider 目录 |
| `/api/providers/custom` | GET/POST/DELETE | 自定义 Provider 增删改查 |
| `/api/models` | GET | 模型列表（管理视角，需 `?type=<provider>`） |
| `/api/stats` | GET | 用量/健康/失败统计 |

---

*本文档与代码同步维护：改了路由/环境变量/功能时请同步更新对应章节。*
