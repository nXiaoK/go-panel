# syntax=docker/dockerfile:1.7

# 前端只在构建平台执行；最终静态资源会嵌入 Go 二进制，不要求部署机安装 Node.js。
FROM --platform=$BUILDPLATFORM node:22-alpine AS frontend

WORKDIR /src/vite-frontend
COPY ["vite-frontend/package.json", "vite-frontend/package-lock.json", "./"]
RUN --mount=type=cache,target=/root/.npm npm ci
COPY ["vite-frontend/", "./"]
RUN npm run build


# Go 阶段同时交叉编译面板和两种架构的节点程序；1.26.7 包含当前标准库安全修复，不能降级。
FROM --platform=$BUILDPLATFORM golang:1.26.7-alpine AS backend

ARG TARGETOS=linux
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIME=unknown

WORKDIR /src
ENV CGO_ENABLED=0 \
    GOCACHE=/root/.cache/go-build \
    GOMODCACHE=/go/pkg/mod

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
COPY --from=frontend /src/vite-frontend/dist/ ./web/dist/

# 镜像固定携带 amd64/arm64 的 gost 与 nft 工具，节点升级不再依赖宿主机本地文件。
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    sh ./scripts/build-node-assets.sh

RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    GOOS="$TARGETOS" GOARCH="$TARGETARCH" go build \
      -trimpath \
      -ldflags="-s -w \
        -X github.com/nXiaoK/go-panel/internal/buildinfo.Version=$VERSION \
        -X github.com/nXiaoK/go-panel/internal/buildinfo.Commit=$COMMIT \
        -X github.com/nXiaoK/go-panel/internal/buildinfo.BuildTime=$BUILD_TIME" \
      -o /out/go-panel .


# 运行镜像只包含面板、节点产物和时区/证书；3.23 仍处于安全维护周期。
FROM alpine:3.23

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIME=unknown

LABEL org.opencontainers.image.title="go-panel" \
      org.opencontainers.image.description="Go + SQLite network forwarding panel" \
      org.opencontainers.image.source="https://github.com/nXiaoK/go-panel" \
      org.opencontainers.image.version="$VERSION" \
      org.opencontainers.image.revision="$COMMIT" \
      org.opencontainers.image.created="$BUILD_TIME"

RUN apk add --no-cache ca-certificates tzdata \
	&& mkdir -p /app/data /app/node-assets

# LISTEN_ADDR 对容器开放端口；JWT_SECRET 等安全配置必须由 Compose/docker run 显式传入。
ENV TZ=Asia/Shanghai \
	LISTEN_ADDR=:6365 \
	DB_PATH=/app/data/flux-panel.db \
	NODE_ASSET_DIR=/app/node-assets

WORKDIR /app

COPY --from=backend /out/go-panel /app/go-panel
COPY --from=backend /src/node-assets/ /app/node-assets/
COPY subscription-assets/vless-server.sh /app/vless-server.sh
COPY subscription-assets/clash.yml /app/clash.yml
COPY subscription-assets/surge.config /app/surge.config
RUN chmod 755 /app/go-panel /app/vless-server.sh /app/node-assets/*

# 数据库卷必须持久化；节点程序属于镜像内容，不能再被空宿主机目录覆盖。
VOLUME ["/app/data"]
EXPOSE 6365

# 健康检查仅访问无敏感信息的本机端点，连续失败才让编排器标记为 unhealthy。
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -q -T 3 -O /dev/null http://127.0.0.1:6365/healthz || exit 1

ENTRYPOINT ["/app/go-panel"]
