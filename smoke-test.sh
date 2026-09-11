#!/usr/bin/env bash
# AI Gateway —— 容器冒烟测试脚本
# 用法：GW_TEST_PASSWORD=<admin密码> bash smoke-test.sh [BASE_URL]
# 默认 BASE_URL=http://localhost:7000
#
# 关于密码：网关不再接受「首次运行任意密码」。未设置 ADMIN_PASSWORD 的实例会
# 直接拒绝登录（403），因此本脚本需要一个已初始化的实例：
#   1) 在 .env 里设 ADMIN_PASSWORD=... 后 docker compose up -d
#   2) 或 docker compose run --rm ai-gateway --reset-password 生成随机密码
# 然后用 GW_TEST_PASSWORD 把该密码传给本脚本。
set -u

BASE="${1:-http://localhost:7000}"
JAR=$(mktemp)
LOGINHTML=$(mktemp)
HDR=$(mktemp)
PASS="${GW_TEST_PASSWORD:-password123}"
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[0;33m'; NC='\033[0m'
FAIL=0

pass(){ echo -e "${GREEN}✓${NC} $1"; }
fail(){ echo -e "${RED}✗${NC} $1"; FAIL=1; }
warn(){ echo -e "${YELLOW}!${NC} $1"; }

echo "==> 冒烟测试目标: $BASE"
echo

# 1) 健康检查（容器内 busybox wget 也探这个）
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/health")
if [ "$code" = "200" ]; then pass "/health -> 200"; else fail "/health -> $code (期望 200)"; fi

# 2) 登录页（直接管道 grep，避免写 /tmp 在某些 Git Bash 下失败）
code=$(curl -s -w '\n%{http_code}' "$BASE/login" | tee "$LOGINHTML" | tail -1)
body=$(cat "$LOGINHTML")
if [ "$code" = "200" ] && echo "$body" | grep -qi "password"; then
  pass "/login -> 200 且含登录表单"
else
  fail "/login -> $code (期望 200 且含表单)"
fi

# 3) 未登录访问受保护根路径应 302 重定向到 /login（抓响应头判断）
curl -s -D "$HDR" -o /dev/null "$BASE/"
code=$(grep -i '^HTTP' "$HDR" | tail -1 | awk '{print $2}')
loc=$(grep -i '^location' "$HDR" | tail -1 | awk '{print $2}' | tr -d '\r')
if [ "$code" = "302" ] && echo "$loc" | grep -q "/login"; then
  pass "/ 未登录 -> 302 重定向到 $loc"
else
  fail "/ 未登录 -> $code (Location=$loc, 期望 302 -> /login)"
fi

# 4) 登录
#    注意：未设 ADMIN_PASSWORD 的实例会返回 403（有意为之的安全默认，不再是
#    「任意密码即可进入」）。403 不算失败，但要明确告诉使用者实例尚未初始化。
login_code=$(curl -s -c "$JAR" -o /dev/null -w '%{http_code}' \
  -X POST "$BASE/auth/login" \
  -H 'Content-Type: application/json' \
  -d "{\"username\":\"admin\",\"password\":\"$PASS\"}")
case "$login_code" in
  200)
    if [ -s "$JAR" ]; then pass "/auth/login -> 200 且写入会话 cookie"
    else fail "/auth/login -> 200 但未写入会话 cookie"; fi
    ;;
  403)
    warn "/auth/login -> 403：该实例尚未设置管理员密码（这是预期的拒绝，不是缺陷）"
    warn "  先设 ADMIN_PASSWORD 重启，或 docker compose run --rm ai-gateway --reset-password"
    warn "  拿到密码后：GW_TEST_PASSWORD=<密码> bash smoke-test.sh $BASE"
    echo
    echo -e "${YELLOW}结果: 实例未初始化，依赖登录的检查已跳过${NC}"
    rm -f "$JAR" "$LOGINHTML" "$HDR"
    exit 0
    ;;
  429)
    warn "/auth/login -> 429：触发登录限流，等 5 分钟窗口过期后重试"
    ;;
  *)
    fail "/auth/login -> $login_code (期望 200 + cookie)"
    ;;
esac

# 5) 登录后访问受保护 API
for ep in /api/stats /api/providers /api/config; do
  code=$(curl -s -b "$JAR" -o /dev/null -w '%{http_code}' "$BASE$ep")
  if [ "$code" = "200" ]; then pass "$ep -> 200"; else warn "$ep -> $code"; fi
done

# /api/models 需要 ?type= 参数（缺参返回 400 是正常校验）
# 带 type 调用时，因容器刚启动无提供商配置，应到达 provider 查找逻辑（返回 404 而非 400）
mcode=$(curl -s -b "$JAR" -o /dev/null -w '%{http_code}' "$BASE/api/models?type=openai")
if [ "$mcode" = "404" ] || [ "$mcode" = "200" ]; then
  pass "/api/models?type=openai -> $mcode (通过 type 必填校验，进入查找逻辑)"
elif [ "$mcode" = "400" ]; then
  fail "/api/models?type=openai -> 400 (不该在已带 type 时返回 400)"
else
  warn "/api/models?type=openai -> $mcode"
fi

# 6) 静态资源（Vue 生产版 / 构建期生成的 Tailwind CSS / Font Awesome）应可访问
for f in /static/vue.global.prod.js /static/tailwind.css /static/all.min.css; do
  code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE$f")
  if [ "$code" = "200" ]; then pass "$f -> 200"; else fail "$f -> $code (期望 200)"; fi
done

# 7) 注销
code=$(curl -s -b "$JAR" -c "$JAR" -o /dev/null -w '%{http_code}' -X POST "$BASE/auth/logout")
if [ "$code" = "200" ]; then pass "/auth/logout -> 200"; else warn "/auth/logout -> $code"; fi

rm -f "$JAR" "$LOGINHTML" "$HDR"

echo
if [ "$FAIL" = "1" ]; then
  echo -e "${RED}结果: 存在失败项，请检查上方 ✗${NC}"
  exit 1
else
  echo -e "${GREEN}结果: 冒烟测试通过${NC}"
fi
