# AI Gateway Go - Project Memory

## Port
- Default: **7000** (`main.go:31`, changed from original 3000)
- Controlled via `PORT` env var

## Static Files (local, no CDN)
- `static/vue.global.js` - Vue 3 (downloaded from unpkg)
- `static/tailwind.js` - Tailwind Play CDN (downloaded)
- `static/all.min.css` - Font Awesome 6 CSS
- `webfonts/` - Font Awesome .woff2/.ttf font files
- Routes: `/static/` and `/webfonts/` in `main.go`

## SIMPLE_MODE
- **Completely removed** from all Go files
- Login always requires credentials
- Default username: `admin` (in `internal/config/config.go`)
- First run: any password accepted, then hashed and saved

## Login Page
- `internal/web/web.go:RenderLogin()` - pure HTML+CSS+Vanilla JS, zero external dependencies
- No Vue, no Tailwind, no CDN

## Dashboard
- `internal/web/web.go:renderDashboardTemplate()` - Vue 3 app with 6 tabs
- All resources served locally from `/static/` and `/webfonts/`

## Navigation (order)
1. 概览 (Overview) - API key, stats, health summary, provider health, failure stats
2. 提供商 (Providers) - provider config + custom provider (inline, not separate tab)
3. 模型 (Models)
4. 测试 (Test / Playground)
5. 日志 (Logs)
6. 设置 (Settings)

## Build & Run
```powershell
cd D:\tools\opencode\api-gateway
go build -o ai-gateway.exe .
Start-Process -FilePath ".\ai-gateway.exe" -WindowStyle Minimized
# Visit http://localhost:7000/login
```

## Key Files Modified
- `main.go` - port 7000 default, static/webfonts routes, removed SIMPLE_MODE
- `internal/web/web.go` - vanilla JS login, local assets, no SIMPLE_MODE pages
- `internal/db/db.go` - removed SIMPLE_MODE from Env struct
- `internal/auth/auth.go` - removed SIMPLE_MODE login shortcut
- `internal/storage/storage.go` - removed SIMPLE_MODE auth bypass
