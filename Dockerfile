# syntax=docker/dockerfile:1

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

# 网关需对外访问 AI 厂商的 HTTPS 接口，必须携带 CA 证书；tzdata 用于日志时间戳
RUN apk add --no-cache ca-certificates tzdata

# 以非 root 用户运行，降低容器内提权风险
RUN addgroup -S app \
    && adduser -S app -G app \
    && mkdir -p /app/data \
    && chown -R app:app /app

# 拷贝二进制与本地静态资源（Vue / Tailwind / Font Awesome）
COPY --from=build /out/ai-gateway /app/ai-gateway
COPY static   /app/static
COPY webfonts /app/webfonts

USER app

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

ENTRYPOINT ["/app/ai-gateway"]
