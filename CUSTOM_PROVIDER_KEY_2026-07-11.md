# Custom Provider API Key — 实现与验证 (2026-07-11)

## 目标
自定义 Provider 需要密钥字段，用于拉取模型列表与代理请求（像内置 provider 一样工作）。

## 实现
- config.go: `CustomProvider` 新增 `Key` 字段（仅服务端持有，绝不下发客户端）。
- api.go `HandleCustomProvidersPost`:
  - upsert 语义：已存在则合并非空字段（支持只改 key）。
  - key 镜像到 `cfg.Providers[]`（Type=cp.ID, Key=cp.Key），使现有模型拉取/代理逻辑复用。
  - key 清空（空串）则移除镜像条目，避免陈旧密钥滞留。
  - 响应体返回 `merged` 但 `resp.Key=""`，**绝不回显明文密钥**。
- api.go `HandleConfigPost`: 新增 re-merge 循环，每次保存配置时把 `cfg.CustomProviders` 中有 key 的条目重新合并进 `cfg.Providers`，避免仪表盘 `saveConfig()` 把自定义 provider 的镜像 key 冲掉（关键 bug 修复）。
- api.go `HandleCustomProvidersGet`: 返回 `keyMask`（仅后 4 位，如 `****1234`）。
- api.go `HandleCustomProvidersDelete`: 删除自定义 provider 时同步删除 `cfg.Providers` 镜像条目。
- web.go: 仪表盘自定义 provider 表单新增密钥输入框；列表每项有「更新密钥/清除」；显示已配置密钥掩码徽章。

## 发现的真实 Bug（已在本次修复）
1. **POST 响应回显明文 key** —— 已改为返回空 key。
2. **saveConfig 冲掉自定义 provider key** —— `HandleConfigPost` 原逻辑 `cfg.Providers = body.Providers` 会用客户端（不含自定义 key）的列表覆盖服务端镜像，导致自定义 provider 加完后再触发任何配置保存即丢失 key、无法拉模型/代理。已用 re-merge 解决。

## 验证（本地 Windows 冒烟）
- go build / go vet / go test ./... 全绿。
- 复现并验证 clobber 场景：添加自定义 provider(key=sk-secret-E2E) → 发空 providers 的 /api/config POST → 重启服务 → `providers` 仍含 `{"type":"e2e","key":"sk-secret-E2E"}`。
- GET /api/providers/custom 返回 `keyMask:"****1234"`，POST 响应无 key 明文。
- 交叉编译 `ai-gateway-linux-amd64`（15.8MB）供 Debian VM 部署。

## 提交
- ea78d23 feat(custom): add secret API key to custom providers with server-side mirroring
