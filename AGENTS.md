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
| Master recovery key | Settings page | One permanent high-entropy key (digest-only storage, NOT consumed on use); accepted by the same login-page form as the codes |

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

## Build & Run — Docker Compose only
Local and VPS run the exact same way. There is deliberately **no** "run the
binary directly" path in the repo anymore: `start.sh`, `start.bat` and the
systemd unit `ai-gateway.service` were removed, and the `ai-gateway-linux-amd64`
build artifact is no longer tracked.

```bash
cp .env.example .env        # optional: host port / CORS origin / ADMIN_PASSWORD
docker compose up -d --build
# dashboard http://localhost:7000  ·  login: admin + $ADMIN_PASSWORD
```

- Data lives in the named volume `gateway-data` → container `/app/data`.
  The compose file also documents the bind-mount alternative.
- Repo directory: `D:\tools\WorkBuddy\ai-gateway_v5`
- VPS: same compose file, fronted by nginx (`nginx-ai-gateway.conf`,
  remember `TRUST_PROXY=1` so login rate limiting sees the real client IP).

## Testing
- Unit tests still run on the host (and in CI): `CGO_ENABLED=0 go test ./...`
- Container smoke: `bash smoke-test.sh http://localhost:7000` — see `TESTING.md`.

## Frontend assets — build-time generated, do not hand-edit
| File | Source | Notes |
|---|---|---|
| `static/tailwind.css` | Tailwind CLI v3.4.17 scanning `internal/web/web.go` | 14 KB. Replaced the 407 KB Play CDN, which generated styles in the browser at runtime (FOUC, not for production). |
| `static/vue.global.prod.js` | Vue 3.5.39 production build | The dev build (`vue.global.js`, 605 KB) was shipped here by mistake; the prod build is 165 KB. |

- Both are **generated**. After changing any class name in `internal/web/web.go`,
  regenerate `tailwind.css` — a stale file means purged classes and missing
  styles. Tooling + a coverage checker live in `.workbuddy/tailwind-build/`.
- The `<link href="/static/tailwind.css">` sits **after** the page's own
  `<style>` on purpose: the Play CDN appended its rules to the end of `<head>`
  at runtime, so this preserves the original cascade order.
- Dynamic class concatenation in the template (`'health-' + status`,
  `'toast-' + type`) targets **custom CSS** in the page's own `<style>` block,
  not Tailwind utilities — hence no safelist is needed. `preflight` must stay on.
- The login page uses neither Tailwind nor Vue; it is plain HTML/CSS/JS.

## Client identity — one helper, two consumers
`utils.ClientIP(r)` is the single source of truth for "who is calling". Both the
login rate limiter and the request logger use it, so they never disagree.
- Without `TRUST_PROXY=1` it returns the socket peer (port stripped). Behind
  nginx that is *always the proxy*: the limiter degenerates into a global one and
  every Logs entry names the proxy. Set `TRUST_PROXY=1` behind a reverse proxy.
- When trusted it prefers `X-Real-IP`, else the **last** hop of
  `X-Forwarded-For` (nginx appends what it saw; a client-supplied prefix is
  attacker-controlled).
- The limiter map is swept on a throttled schedule — `/auth/login` and
  `/auth/recovery` are unauthenticated, so without pruning a scanner cycling
  through addresses would grow it without bound.

## CORS — deliberately credential-free
`Access-Control-Allow-Credentials` is **not** sent. The session cookie is
`SameSite=Strict`, so it never travels on a cross-site request and credentialed
cross-origin access could not work anyway. Advertising it would only invite
someone to "fix" the mismatch by relaxing `SameSite` — and none of the mutating
endpoints carry a CSRF token, so that would open a real hole. Cross-origin API
clients should use the Bearer unified key.

## Build / deploy notes
- `Dockerfile` — multi-stage (`golang:1.26-alpine` → `alpine:3.20`), CGO off.
  It intentionally has **no** `# syntax=docker/dockerfile:1` directive: that
  forces a dockerfile-frontend pull which hangs behind a flaky proxy.
- `VERSION` build arg → `-X main.version`, printed in the startup banner. Needed
  because `.dockerignore` excludes `.git`, so the binary cannot pick up
  `vcs.revision` itself and would otherwise fall back to `dev`. CI passes
  `github.sha`; locally pass `git rev-parse --short HEAD`.
- `docker-entrypoint.sh` — starts as root only long enough to `chown /app/data`,
  then drops to the non-root `app` user via `su-exec`.
- `.gitattributes` pins `*.sh` to LF. CRLF in the entrypoint shebang makes the
  container fail to start at all (Alpine looks for `#!/bin/sh\r`).
- `.dockerignore` keeps the build context to source + `static/` + `webfonts/`.
- `smoke-test.sh` needs an initialised instance: pass the admin password via
  `GW_TEST_PASSWORD=...`. A 403 is reported as "instance not initialised"
  (with the fix), not as a test failure — the gateway no longer accepts an
  arbitrary first-run password.
