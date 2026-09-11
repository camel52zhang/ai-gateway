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
- First run: no longer accepts an arbitrary password. Set `ADMIN_PASSWORD` to
  seed the account at startup; otherwise sign-in is refused until a password is
  set. `ALLOW_FIRST_RUN_ANY_PASSWORD=1` restores the old behaviour (not
  recommended on a network-reachable host).

## Authentication & Recovery
Three recovery paths, all local — no SMS / email / OAuth. This is a single-user
self-hosted gateway with no second identity source to bind to, so the failure
mode that actually happens is "locked out of my own service", not impersonation.

| Mechanism | Env / flag | Behaviour |
|---|---|---|
| Seed password | `ADMIN_PASSWORD` | At startup, hashes and stores it if the DB has no password yet |
| Reset, one-shot | `--reset-password` | Resets then **exits**; prints a generated password unless `ADMIN_PASSWORD` is set |
| Reset, in-place | `RESET_PASSWORD=1` | Resets during startup and keeps serving; logs a warning until the var is removed |
| Offline recovery codes | Settings page | 10 one-time codes, consumed from the login page |

- A local reset is **not** a privilege escalation: anyone who can run the binary
  or edit the compose file can already rewrite the SQLite database by hand.
- Recovery codes are persisted only as SHA-256 digests (`Config.RecoveryCodes`),
  are single-use, and are normalised (dashes / case optional). Plaintext is
  returned exactly once by `POST /api/recovery/generate` (authenticated).
- A successful reset calls `storage.DeleteAllSessions()`: all old sessions die.
- Endpoints: `POST /auth/login`, `POST /auth/recovery`, `POST /api/recovery/generate`.
- Rate limiting keys on `auth.clientIP()`. Set `TRUST_PROXY=1` behind nginx,
  otherwise every request appears to come from the proxy and the per-IP limiter
  degenerates into a global one (an attacker could lock the operator out).

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
cd D:\tools\qlcaw\api-gateway
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
