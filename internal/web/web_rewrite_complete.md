# Web Dashboard Rewrite - Completion Report

## Objective
Rewrite `E:\tools\qclaw\go\internal\web\web.go` to match the full Node.js dashboard from `E:\tools\qclaw\vps\html\dashboard.js` and `E:\tools\qclaw\vps\src\simpleHtml.js`.

## What Was Done

### web.go — Full Rewrite (57,853 bytes)

Complete feature-parity dashboard with **9 tabs** matching the Node.js version:

| Tab | Key | Description |
|-----|-----|-------------|
| 概览 (Overview) | `overview` | Unified key display, stats grid (requests/tokens/promptTokens/completionTokens), Base URL info, health summary from `/api/stats` |
| 提供商 (Providers) | `providers` | Search, category filter, select provider, save/update API key with visual key indicator |
| 模型 (Models) | `models` | Table of models fetched from `/api/models?type=xxx` per provider |
| 测试 (Test) | `test` | Full playground: model selector (grouped by provider type), system/user prompt, temperature/maxTokens/topP controls, streaming toggle, response display with token stats and latency |
| 日志 (Logs) | `logs` | Recent request logs + error logs with time/provider/category/status filters from config data |
| 故障 (Failures) | `failures` | Failure metrics per provider from `config.failureMetrics`, categorized by type |
| 健康 (Health) | `health` | Provider health states (healthy/degraded/circuit_open) with colored dots, latency display |
| 自定义 (Custom) | `custom` | Full CRUD for custom providers via `/api/providers/custom` |
| 设置 (Settings) | `settings` | Change password form (POST `/auth/reset-password`), session info display |

### Functions Added

- **`RenderLogin()`** — Standard login page (Vue 3 + Tailwind CDN, toast notifications) — already existed, kept as-is
- **`RenderDashboard(providerDataJSON string)`** — Full dashboard with `{{PROVIDER_DATA}}` injection
- **`GetSimpleLoginHtml()`** — SIMPLE_MODE login page (matching Node.js `getSimpleLoginHtml`)
- **`GetSimpleDashboardHtml()`** — SIMPLE_MODE dashboard page (matching Node.js `getSimpleDashboardHtml`)
- **`escapeJSON(s string) string`** — JSON string escaper (pre-existing, kept)

### main.go Updates

Updated SIMPLE_MODE routes to use the new functions:
- `/login` → `web.GetSimpleLoginHtml()` (was inline `<h1>Simple</h1>`)
- `/` → `web.GetSimpleDashboardHtml()` (was inline `<h1>Dashboard</h1>`)

### Compilation Status

- ✅ `internal/web/` compiles cleanly
- ✅ All other packages (`api`, `auth`, `config`, `db`, `providers`, `storage`, `utils`) compile cleanly
- ⚠️ `internal/proxy/` has **pre-existing** compilation errors (missing `CustomProvider` arg in `GetFallbackProvider` calls) — not introduced by this change

### Key Design Decisions

1. **Vue 3 data model** mirrors Node.js exactly: `config`, `allProviders`, `providerModels`, log filter state, playground state, etc.
2. **`{{PROVIDER_DATA}}` injection** replaces the `PM = {{PROVIDER_DATA}}` pattern from Node.js
3. **All API endpoints** (`/api/config`, `/api/stats`, `/api/providers`, `/api/providers/custom`, `/api/models`, `/api/key/regenerate`, `/auth/reset-password`, `/auth/logout`) used via fetch()
4. **Streaming test** sends to `/v1/chat/completions` with unified key auth header, handles SSE stream
5. **Simple HTML pages** match Node.js versions exactly (no Vue/Tailwind CDN dependencies)
