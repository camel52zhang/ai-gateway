# API Gateway 第 8 轮收尾报告（fix2 遗留三项增强）

> 纯后端改动，UI / DB / 配置均未触碰。仅改动 `internal/adapters/adapter.go`、`internal/api/api.go`、对应测试。

## 本轮完成的内容

### 1. Cohere v2 tool_calls 双向映射
- `convertToCohere`：OpenAI `tools` → Cohere v2 `parameter_definitions`；assistant 历史 `tool_calls` 透传；tool 结果 → `{role:"tool", tool_call_id, content:[{type:"document", document:{data}}]}`。
- `convertCohereToOpenAI`：`message.tool_calls[]` → OpenAI `tool_calls`（id / type:"function" / function{name, arguments(JSON 字符串)}），并置 `finish_reason="tool_calls"`。
- `translateCohereStreamToOpenAI`：新增 `tool-call-start` / `tool-call-delta` / `tool-call-end` 处理；`tool-call-delta` 增量读取 `delta.message.tool_calls[].function.{name,arguments}` 并逐片 emit，保证工具调用流式可用。

### 2. Gemini 流式非前缀分片兜底
- `translateGeminiStreamToOpenAI`：原严格按"前缀 diff"取增量，遇到服务端发送增量式（非累积）分片会静默丢弃。改为：若当前 `text` 不是 `lastText` 前缀，则直接 emit 整段新 text，避免内容丢失。

### 3. Responses 流式熔断/限流预检
- `internal/api/api.go` 的 `HandleResponses`：在 `ResolveDefinition` 之后新增预检——
  - `proxy.IsBreakerOpen(provider.Type)` → 503（provider circuit open）
  - `proxy.IsLimited(provider.Type)` → 429（rate limited）
- 配套在 `internal/proxy/proxy.go` 新增导出访问器 `IsBreakerOpen(type)` / `IsLimited(type)`，与 chat 路径行为一致，避免对已知不可用 provider 浪费请求。

## 测试中的一个 fixture bug（已修，非实现 bug）
- `stream_test.go` 的 `TestTranslateCohereStreamToolCalls` 第二片 `tool-call-delta` 数据：其 `arguments` 值为 `"\"SF\"}"`，JSON 字符串内含有字面 `}`。原 fixture 少了 1 个结构体闭合 `}`，导致 Go `json.Unmarshal` 报 `unexpected end of JSON input`、该事件被跳过，最终只拿到第一段参数。
- 修正：补 1 个尾部 `}`（共 5 个尾括号）使 JSON 合法。**适配器实现本身正确，无需改代码。**

## 测试覆盖
- 新增/更新：`convert_test.go`（Cohere v2 请求无 v1 `message` 字段 + 响应 `tool_calls` 映射）、`stream_test.go`（Cohere 流式 `tool_calls` 逐片 + Gemini 非前缀兜底）。
- 既有 `auto_test.go` / round6+7 测试均保留。

## 验证结果
- `go vet ./...`：干净。
- `go test ./...`（清除 `HTTP_PROXY` / `HTTPS_PROXY` 死代理环境变量）：全部 `ok`，EXIT:0。
- `go build -o ai-gateway.exe .`：成功（~16MB）。
- 已 `taskkill` 旧进程并重启实例（proxy-free 环境），`GET /health` → `200`，返回合法状态 JSON。

## 状态
三项增强已随新二进制生效。运行时无需任何 DB/配置改动；OpenAI key 仍需用户在仪表盘重新填写（此前为空）。
