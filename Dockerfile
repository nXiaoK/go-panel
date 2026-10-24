# syntax=docker/dockerfile:1.7

# 前端只在构建平台执行；最终静态资源会嵌入 Go 二进制，不要求部署机安装 Node.js。
FROM --platform=$BUILDPLATFORM node:22-alpine AS frontend

WORKDIR /src/vite-frontend
COPY ["vite-frontend/package.json", "vite-frontend/package-lock.json", "./"]
RUN --mount=type=cache,target=/root/.npm npm ci
COPY ["vite-frontend/", "./"]
RUN npm run build


# Go 阶段同时交叉编译面板和两种架构的节点程序；1.26.10 修复网络服务和证书校验漏洞，降级会恢复已知安全风险。
FROM --platform=$BUILDPLATFORM golang:1.26.10-alpine AS backend

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


# 运行镜像只包含面板、节点产物、时区/证书与 tini；3.23 仍处于安全维护周期。
# 修改本阶段（基础镜像、系统包、目录布局、入口命令）且新程序依赖这些修改时，必须递增
# internal/buildinfo.RuntimeABI；否则软更新会把新程序放进不满足要求的旧镜像中运行。
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

# tini 作为 PID 1 回收孤儿进程并转发信号，面板软更新的 supervisor 运行在它之下。
RUN apk add --no-cache ca-certificates tzdata tini \
	&& mkdir -p /app/data /app/node-assets

# LISTEN_ADDR 对容器开放端口；JWT_SECRET 等安全配置必须由 Compose/docker run 显式传入。
# PANEL_RUNTIME_DIR 保存软更新下载的版本与切换状态，必须位于 /app/data 持久卷内，
# 否则重建容器会丢失已切换的版本；改动它会让已有的软更新版本不再被识别。
ENV TZ=Asia/Shanghai \
	LISTEN_ADDR=:6365 \
	DB_PATH=/app/data/flux-panel.db \
	NODE_ASSET_DIR=/app/node-assets \
	PANEL_RUNTIME_DIR=/app/data/runtime

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

# 进程树：tini（PID 1）→ go-panel supervise → go-panel serve。supervisor 是镜像内置版本：
# 选择镜像内置程序或数据卷中经签名校验的软更新版本，新版本先试运行，失败自动回滚；
# tini 把 docker stop 的 SIGTERM 转发给 supervisor，再由它转发给面板进程完成优雅停机。
ENTRYPOINT ["/sbin/tini", "--", "/app/go-panel", "supervise"]
