# API Gateway 全模块检查 + 优化点审计

> 日期：2026-07-10 · 范围：`D:/tools/WorkBuddy/api-gateway` 全部 Go 后端模块
> 说明：纯只读审计 + 测试。本轮**未修改任何代码**（UI 也未动）。所有结论基于源码审阅 + 官方 API 文档核实 + `go build`/`go vet`/`go test` 验证。

## 1. 测试结果（全绿）

| 命令 | 结果 |
|------|------|
| `go build ./...` | BUILD_OK |
| `go vet ./...` | VET_OK |
| `go test ./...` | EXIT:0（adapters / api / proxy / storage 全过，含上一轮新增的 `auto_test.go`） |

> 注：测试在清除 `HTTP_PROXY` 等代理环境变量后运行，避免假上游被路由到死代理。

## 2. 模块状态一览

| 模块 | 状态 | 备注 |
|------|------|------|
| `main.go` | ✅ 正常 | 路由、CORS（`*` 与指定 origin 区分处理）、请求日志过滤（health/static/webfonts/login）均正确。 |
| `auth/auth.go` | ✅ 正常 | PBKDF2 哈希、timing-safe 校验、`/auth/login`、`/logout`、`/reset-password`、登录限流均正确。首次登录用户名固定 `admin`（`DefaultConfig` 写死），符合预期。 |
| `utils/utils.go` | ✅ 正常 | `VerifyPassword` 用 `subtle.ConstantTimeCompare`（防时序攻击）；`TimingSafeCompare` 已在代理鉴权路径使用。 |
| `proxy/proxy.go` | ✅ 基本正常 | `HandleProxy` 鉴权 → auto 分发 → 显式模型解析 → 熔断/限流预检 → 重试/故障转移（`MAX_RETRIES=2`）链路完整。**1 个中高危问题见 §3.2。** |
| `adapters/adapter.go` | ⚠️ 有问题 | OpenAI 兼容、Gemini（v1beta）路径正确。**Cohere 适配器整套是 v1 写法却在打 v2 端点 → 基本失效（见 §3.1）。** |
| `api/api.go` | ✅ 正常 | `HandleConfigPost`（含第 6 轮模型缓存清理）、`HandleModels`、`HandleResponses`（真流式 + tool_calls）均正确。 |
| `storage/storage.go` | ✅ 正常 | `GetConfig` 内存缓存（含 `cloneConfig` 隔离）、RMW 锁、模型缓存增删均正确。 |
| `providers/providers.go` | ✅ 正常 | `ResolveDefinition`/`SortedByPriority`/`GetFallbackProvider` 正确。 |
| `web/web.go` | ✅ 正常 | Vue 仪表盘 SPA（未改动）。`{{PROVIDER_DATA}}` 仅含内置 provider 静态定义，无用户输入注入，无 XSS 风险。 |
| `config/config.go` | ✅ 正常 | 结构清晰。 |

## 3. 发现的问题（按优先级）

### 3.1 【高危】Cohere 适配器 v1/v2 形态不匹配 —— Cohere 基本失效

`cohereProxy` / `cohereStreamProxy` / `convertToCohere` / `convertCohereToOpenAI` / `translateCohereStreamToOpenAI` 全部按 **Cohere v1** 形态编写，但实际请求打的是 **`/v2/chat`**（v2 端点）。经核对 Cohere 官方 v2 文档，三处全部对不上：

1. **请求体错误**：代码发 `{role:"USER", message:"...", preamble:"..."}`（v1 字段）。v2 `/v2/chat` 要求 `{role:"user", content:"..."}`（小写 role + `content` 字段）。v2 不认 `message`/`preamble`，模型会收到空内容或报错。
2. **非流式响应解析错误**：代码读 `cohereResp["text"]`（v1 顶层字段）。v2 响应结构是 `{message:{content:[{type:"text", text:"..."}]}, usage:{...}}`，**没有顶层 `text`** → 非流式 Cohere 返回**空内容**。
3. **流式事件解析错误**：代码监听 `event: text-generation` 并读 `chunk["text"]`（v1）。v2 流式发出的是 `event: content-delta`，数据是 `{"delta":{"message":{"content":{"text":"..."}}}}`（增量 delta）→ 流式 Cohere **完全不输出文本**（只发 role + `[DONE]`）。

**影响**：任何配置 Cohere 的请求都会失败/返回空。很可能是从未被实跑暴露的潜伏 bug（你一直用 OpenAI）。

**修复方向**：把 Cohere 适配器整体改写为 v2 形态——请求用 `role/content`；非流式从 `message.content[].text` 提取；流式解析 `content-delta` 的 `delta.message.content.text`（并按增量 delta 直接输出，不要做累积快照 diff）。工作量中等，单测可覆盖。

### 3.2 【中高危】4xx 响应会误触熔断，导致 provider 被整体 503 阻断

`proxy.go` 的 `executeNonStreamingProxy` / `executeStreamingProxy` 中，对 **4xx**（401 坏 key、404 模型不存在、400 错误请求等）也调用了 `cb.RecordFailure`（`proxy.go:399`、`:405`/`:499`）。一旦累计 5 次（阈值 `cbThreshold=5`，无时间窗重置），熔断器 `open` 该 provider **60 秒**，期间所有打到该 provider 的请求直接 503。

**后果**：调用方的偶发错误（比如拼错模型名、临时用错 key）会在 ~5 次内把整个 provider 打挂一分钟，造成"假宕机"。5xx / 网络错误才应该计入熔断（这才是瞬态上游健康问题）。

**修复方向（小改动）**：把 `cb.RecordFailure` 从 4xx 分支移出——4xx 仍 `RecordFailureMetric` + 写错误日志，但不计入熔断；只有网络错误与 5xx 才 `cb.RecordFailure`。

### 3.3 【低】Gemini 流式 text-diff 对非前缀分片会静默丢弃

`translateGeminiStreamToOpenAI` 用 `strings.HasPrefix(text, lastText)` 求增量 suffix（Gemini 是累积快照，正确）。但若某分片文本不以 `lastText` 为前缀（异常顺序/分片），`delta` 不被发出、该段被静默丢。Gemini 实际行为安全，但加一个兜底更稳：当非前缀时直接输出整段 `text` 并重置 `lastText`。

### 3.4 【低】Responses 流式路径未做熔断/限流预检

`HandleResponses`（流式）直接 `StreamingProxyWithProvider`，没有像 `HandleProxy` 那样先 `cb.IsOpen` / `rl.IsLimited` 预检。行为不一致，但对"找一个能用的"语义影响小（流式 header 已发无法回退）。可在入口补一次预检，与 chat 路径保持一致。

### 3.5 【低】登录限流 map 无清理，长期运行有轻微内存增长

`auth.go` 的 `loginRateLimit` 只在访问时惰性重置过期项，不主动 GC 已失效的 IP 项。对长运行服务、大量陌生 IP 时缓慢增长。可加一个定时/懒清理，或限制 map 容量。影响极小。

### 3.6 【低】非流式成功路径透传全部上游响应头

`executeNonStreamingProxy` 用 `w.Header().Add` 透传上游所有头（含 `Content-Length`/`Connection` 等 hop-by-hop）。Go 的 `http.ResponseWriter` 通常会忽略/接管这些头，实际无害，但严谨起见可只透传白名单（Content-Type、自定义头等）。

## 4. 结论

- **编译 / 静态检查 / 单测全绿**，OpenAI 兼容、Gemini、auto 模式、Responses 流式、鉴权、配置、缓存等核心路径均正常。
- **最值得修的两项**：① Cohere 适配器 v1/v2 不匹配（高危，Cohere 实际不可用）；② 4xx 误触熔断（中高危，偶发调用错误会假宕机 60s）。
- 其余为低优先级健壮性优化。

> 本轮仅做审计，未改动任何文件。是否要我着手修复 3.1 / 3.2（以及可选的 3.3–3.6）？纯后端、不动 UI。
