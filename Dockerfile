# NOTE: deliberately NO `# syntax=docker/dockerfile:1` directive. This build
# uses only stock Dockerfile features, and the frontend directive would force a
# pull of the dockerfile image from Docker Hub on every build — which hangs
# behind a flaky proxy (auth.docker.io 502). Keep the built-in frontend.

# ============================================================
# 构建阶段：编译 Go 二进制
# ============================================================
FROM golang:1.26-alpine AS build
WORKDIR /src

# 项目使用纯 Go 版 SQLite（modernc.org/sqlite），关闭 CGO 即可静态链接，无需 gcc
ENV CGO_ENABLED=0 GOOS=linux GOFLAGS=-mod=mod

# 仅先拉取依赖，利用 Docker 层缓存加速后续构建
COPY go.mod go.sum ./
RUN go mod download

# 复制全部源码并编译（trimpath + 去除符号表，减小体积）
COPY . .
RUN go build -trimpath -ldflags="-s -w" -o /out/ai-gateway .

# ============================================================
# 运行阶段：精简的 Alpine 镜像
# ============================================================
FROM alpine:3.20
WORKDIR /app

# 网关需对外访问 AI 厂商的 HTTPS 接口，必须携带 CA 证书；tzdata 用于日志时间戳；
# su-exec 用于启动期以 root 修正数据目录属主后降权到 app 用户
RUN apk add --no-cache ca-certificates tzdata su-exec

# 创建非 root 的 app 用户，数据目录初始属主为 app（运行时若 bind 挂载了宿主机
# root 属主的目录，入口脚本会再修正一次）
RUN addgroup -S app \
    && adduser -S app -G app \
    && mkdir -p /app/data \
    && chown -R app:app /app

# 拷贝二进制与本地静态资源（Vue / Tailwind / Font Awesome）
COPY --from=build /out/ai-gateway /app/ai-gateway
COPY static   /app/static
COPY webfonts /app/webfonts
COPY docker-entrypoint.sh /app/docker-entrypoint.sh
# 防御：若构建上下文里的脚本被 CRLF 污染（Windows core.autocrlf=true 检出），
# shebang 会变成 "#!/bin/sh\r" 导致内核找不到解释器、容器直接起不来。
# 这里强制转成 LF，保证镜像在任何宿主机换行符下都能正常启动。
RUN sed -i 's/\r$//' /app/docker-entrypoint.sh && chmod +x /app/docker-entrypoint.sh

EXPOSE 7000

# 清掉可能干扰上游直连的代理变量（与 start.sh 行为一致：纯直连模式）
ENV PORT=7000 \
    ALLOWED_ORIGIN= \
    HTTP_PROXY= \
    HTTPS_PROXY= \
    http_proxy= \
    https_proxy= \
    ALL_PROXY= \
    all_proxy=

# 容器自带的健康检查（docker run 场景或 compose 未覆盖时也生效）。
# /health 是存活探针：进程活着即回 200，DB 读失败会带 dbError 标记但仍 200，
# 避免瞬时 DB 抖动把容器误判为 unhealthy。
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -q -O /dev/null http://localhost:7000/health || exit 1

ENTRYPOINT ["/app/docker-entrypoint.sh"]
