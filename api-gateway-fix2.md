# API Gateway — 第 7 轮修复（auto 熔断误触发 + Cohere v2 不匹配）

> 纯后端修改，UI 未动。基于第 6 轮全模块审计（`api-gateway-audit.md`）发现的两个高优先问题。
> 验证：`go build` / `go vet` / `go test ./...` 全绿（EXIT:0）。

---

## 1. 4xx 误触熔断（🟠 中高危 → 已修）

**问题**：`proxy.go` 里流式主路径（`executeProxy`）和 auto 路径（`handleAutoProxy`）对 4xx（401 坏 key、404 模型不存在、400 错误请求、429 限流）也调用了 `cb.RecordFailure`。累计 5 次（熔断阈值）就会把整个 provider 熔断 **60 秒**，期间所有该 provider 请求直接 503。后果：拼错一次模型名、临时用错 key，5 次内即可让整个 provider "假宕机"一分钟。非流式主路径此前已正确排除 4xx，但流式/auto 路径遗漏。

**修复**（`internal/proxy/proxy.go`）：
- 新增 `breakerWorthy(networkErr error, statusCode int) bool` 判定助手：仅 **网络错误** 与 **5xx** 计入熔断；4xx 一律不计入。
- 流式主路径的 4xx 分支移除 `cb.RecordFailure`（仅写回上游状态码 + 记指标/日志）。
- auto 路径的两处失败分支（流式 4xx、非流式 4xx、以及网络错误分支统一用 `breakerWorthy` 守卫）改为仅在 `breakerWorthy` 为真时记录熔断失败。
- 4xx 仍照常返回上游状态码、记失败指标与错误日志，仅不再触发熔断。

效果：坏 key / 模型名错误 / 限流只影响当次请求，不会再"连坐"整个 provider。

---

## 2. Cohere 适配器 v1/v2 不匹配（🔴 高危 → 已修）

**问题**：整套 Cohere 代码是 **v1 写法**（`convertToCohere` 发 `message`/`USER`/`preamble`；`convertCohereToOpenAI` 读顶层 `text`；`translateCohereStreamToOpenAI` 解析 `text-generation` 累积文本），但请求打的是 **`/v2/chat`**（v2 端点）。对照 Cohere 官方 v2 文档核实：
- v2 请求要小写 `role`/`content`（v1 的 `USER`/`CHATBOT`/`message`/`preamble` v2 不认）→ 对话被无视；
- v2 非流式响应是 `message.content[].text`（**无顶层 `text`**）→ 非流式返回 **空内容**；
- v2 流式是 `event: content-delta`（`data.delta.message.content.text` 为**逐 token 增量**，非 `text-generation` 累积）→ 流式 **完全不输出文本**。

即任何配置 Cohere 的请求都失败/返回空（之前一直用 OpenAI 没暴露）。

**修复**（`internal/adapters/adapter.go`）：
- `convertToCohere`：改为 v2 小写 `role`（user/assistant/system/tool 透传）、`content` 字符串；去掉 `preamble`/`message`/`USER`/`CHATBOT`。
- `convertCohereToOpenAI`：从 `message.content[].text` 取文本（兼容 `content` 为纯字符串的变体），不再读不存在的顶层 `text`；用量改用 `cohereUsageToOpenAI`。
- `cohereUsageToOpenAI`：优先读 v2 `usage.tokens.{input,output}_tokens`，兼容旧 `usage.billed_units` 与 `meta.billed_units`。
- `cohereProxy`：改用新签名 `cohereUsageToOpenAI(cohereResp)` 取用量。
- `translateCohereStreamToOpenAI`：解析 v2 `content-delta` 事件，直接用 `delta.message.content.text` 增量 token 发出（不再做累积 diff）；`message-end`/`stream-end` 事件取用量（多路径兜底）；`event:` 行缺失时回退到 data 内 `type` 字段。

**新增测试守护**：
- `stream_test.go`：`TestTranslateCohereStream`（v2 content-delta 增量 → "Hello world" + usage）、`TestTranslateCohereStreamNoEventType`（无 event 行、type 内嵌的回退路径）。
- `convert_test.go`：`TestConvertCohereToOpenAIV2`（message.content[].text + usage.tokens 解析）、`TestConvertToCohereV2Request`（小写 role、无 preamble/message 字段）。

---

## 验证

```
go build ./...                 → BUILD_OK
go vet ./...                   → VET_OK
go test ./... (proxy env cleared) → EXIT:0
  ok  ai-gateway/internal/adapters
  ok  ai-gateway/internal/api
  ok  ai-gateway/internal/proxy
  ok  ai-gateway/internal/storage
```

---

## 上线提示

当前运行的 `./ai-gateway.exe` 仍是上一版二进制。要让以上修复生效，需重新 `go build -o ai-gateway.exe .` 并重启进程（不动 DB/配置/UI）。

## 仍可选（未做，非阻断）
- **Cohere 工具调用（tool_calls）**：v2 支持 `tools`，但当前 OpenAI→Cohere 未做工具映射，带 tools 的请求到 Cohere 会忽略工具。如需可用再开。
- **Gemini 流式非前缀分片兜底**：round 5 已实现文本 diff，极少见的非前缀分片仍会丢弃，可加兜底拼接。
- **Responses 流式未做熔断/限流预检**：与 chat 路径不一致，可在 `HandleResponses` 前加预检。
