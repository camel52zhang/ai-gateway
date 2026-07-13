#!/usr/bin/env bash
# AI Gateway —— 容器冒烟测试脚本
# 用法：bash smoke-test.sh [BASE_URL]
# 默认 BASE_URL=http://localhost:7000
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

# 4) 首次登录：任意密码都会被哈希保存（admin 用户）
login_resp=$(curl -s -c "$JAR" -o /dev/null -w '%{http_code}' \
  -X POST "$BASE/auth/login" \
  -H 'Content-Type: application/json' \
  -d "{\"username\":\"admin\",\"password\":\"$PASS\"}")
if [ "$login_resp" = "200" ] && [ -s "$JAR" ]; then
  pass "/auth/login -> 200 且写入会话 cookie"
else
  fail "/auth/login -> $login_resp (期望 200 + cookie)"
fi

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

# 6) 静态资源（Vue / Tailwind / Font Awesome）应可访问
for f in /static/vue.global.js /static/tailwind.js /static/all.min.css; do
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
