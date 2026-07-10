@echo off
REM ============================================================
REM  AI Gateway 启动脚本（部署用）
REM  关键：先清掉可能残留的代理环境变量，避免上游请求被路由到
REM  死代理而报 ECONNREFUSED（历史踩坑点）。
REM ============================================================
set HTTP_PROXY=
set HTTPS_PROXY=
set http_proxy=
set https_proxy=

REM 可选：若需自定义监听地址/端口，取消下一行注释修改
REM set GATEWAY_ADDR=0.0.0.0:7000

echo Starting AI Gateway ...
ai-gateway.exe
