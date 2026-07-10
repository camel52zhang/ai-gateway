# API Gateway — 第 6 轮优化（收尾未完成任务，纯后端）

> 时间：2026-07-10 · 角色：Senior Developer · UI 未改动

## 背景
第 5 轮（tool_calls 流式 + Gemini/Cohere 原生逐 token 流式）已完成且 `go build`/`go vet`/`go test` 全绿。
本轮继续完成第 4 轮复查中记录的 **非阻断遗留项**（均为后端，未碰 UI）：

1. `HandleConfigPost` 不清理已移除 key 的模型缓存 → 仪表盘显示「幽灵模型」。
2. `GetConfig` 每次代理请求都读 SQLite + 解析整段配置 → 高并发下有性能/锁竞争空间。
3. Gemini/Cohere 多模态图片被 `extractMessageText` 静默丢弃 → 视觉请求图片丢失。

## 改动清单

### 1. `GetConfig` 加内存缓存（`internal/storage/storage.go`）
- 新增 `configCache` / `configCacheMu` / `configCached`：热路径直接返回缓存快照，跳过 SQLite 读 + 整段 JSON 解析。
- `SaveConfig` 写入 DB 的同时刷新缓存，保证 DB 与缓存一致。
- 新增 `cloneConfig`：用 JSON 往返做深拷贝，使调用方（如 `HandleConfigPost`、`HandleKeyRegenerate`、`HandleCustomProvidersPost/Delete`）对自己拿到的 config 做原地修改**不会污染共享缓存或其他调用方**。
- 首次启动生成 unified key 的逻辑保持不变（仍由 `configGenerateMu` 串行化）。

### 2. 清理过期模型缓存（`internal/api/api.go` `HandleConfigPost`）
- 保存配置前统计「有 key 的 provider 类型集合 `keyedTypes`」。
- 合并拉取到的模型后，遍历 `cfg.Models`：凡属于「无 key / 已删除类型」的条目，从 `cfg.Models` 删除，并调用新增的 `storage.DeleteCachedModels(t)` 清掉 `gw_models` 里对应缓存。
- 刻意保留「有 key 但本次拉取为空（如上中下游抖动）」的类型，避免误删可用缓存。

### 3. Gemini 多模态图片转发（`internal/adapters/adapter.go`）
- 重写 `convertToGemini`：不再只抽文本，而是 `convertContentToGeminiParts` 按 OpenAI content 数组逐 part 转换——
  - `type:"text"` → Gemini `{"text": ...}`。
  - `type:"image_url"` 的 **base64 `data:` URL** → Gemini `{"inline_data":{"mime_type","data"}}`，视觉请求真正转发。
- 新增 `geminiInlineData`：仅接受 `data:image/...;base64,` 形式（常见视觉请求形态）；远程 `http(s)` URL 不在代理路径内联抓取（避免引入网络耦合与延迟），跳过而非当文本误发。
- Cohere v2 `/v2/chat` 的 `message` 为纯字符串、API 不接收内联图片，故保持文本抽取（与官方能力一致），未作伪转发。
- 空 content 仍补一个（可能为空）文本 part，保证 Gemini content 非空。

### 4. 测试（`internal/adapters/convert_test.go` 新增 / `internal/storage/storage_clone_test.go` 新增）
- `TestConvertToGeminiMultimodal`：文本 + base64 图片 → 生成 2 个 part，图片解析为 `inline_data` 且 `mime_type`/`data` 正确。
- `TestConvertToGeminiTextOnlyRegression`：单字符串 + assistant→model 角色映射回归，产物可 JSON 序列化。
- `TestConvertToGeminiSkipsRemoteImageURL`：远程图片 URL 被跳过，文本 part 保留。
- `TestCloneConfigIsolation` / `TestCloneConfigNilSafe`：验证 `cloneConfig` 的深拷贝隔离性（原始/克隆互不污染）。

## 验证
- `go build ./...` ✅
- `go vet ./...` ✅
- `go test ./...`（已清除 `HTTP_PROXY` 等代理环境变量，避免误报 ECONNREFUSED）✅ 全绿，含本轮新增测试。

## 上线提示
- 当前运行的 `./ai-gateway.exe` 仍是旧二进制；**需重新 `go build` 并重启进程**才能生效（重启前无需动 DB/配置）。
- 未改动任何前端文件、未改动已配置 provider / key。

## 仍可选（未做，非阻断）
- Cohere 内联图片转发：受限于 Cohere v2 chat API（message 为纯文本），如需可改为走 RAG `documents` 或文件上传通道，按需再开。
- `GetConfig` 缓存为进程内单实例；若将来多实例部署需改为共享缓存/DB 通知。
