#!/usr/bin/env bash
# AI Gateway 启动脚本（Linux）
# 作用：清掉可能干扰上游调用的代理环境变量，避免请求被路由到无效地址；
#       然后以前台进程方式运行二进制（交给 systemd 管理生命周期）。
set -e

cd "$(dirname "$0")"

# 清掉代理环境变量（关键：残留的 HTTP_PROXY 会导致上游 API 调用 ECONNREFUSED）
unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy ALL_PROXY all_proxy

# 可选：自定义监听端口（默认 7000，main.go 读 PORT 环境变量）
# export PORT=7000

exec ./ai-gateway-linux-amd64
