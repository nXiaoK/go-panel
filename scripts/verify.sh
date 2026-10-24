#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

go test ./...
go vet ./...
go build ./...
go build ./cmd/node-assets/nft_agent
go build ./cmd/node-assets/nft_flow_reporter
go build ./cmd/node-assets/nft_rule_payload
# selfupdate_e2e 只用于本地容器端到端测试；确认该标签下的代码仍可编译。
go vet -tags selfupdate_e2e ./internal/selfupdate/...

if [[ "${RUN_RACE:-0}" == "1" ]]; then
  go test -race ./internal/ws ./internal/task ./internal/service ./internal/selfupdate/... ./cmd/node-assets/nft_agent ./cmd/node-assets/nft_flow_reporter ./internal/nftgeneration
fi

(
  cd go-gost
  go test ./...
  go vet ./...
  go build ./...
)

# go-gost/x 是独立 Go 模块，父模块的 ./... 不会覆盖节点连接、升级和上报代码。
(
  cd go-gost/x
  go test ./...
  go vet ./...
  go build ./...
)

# 三个模块分别解析依赖图，不能只扫描仓库根目录。
go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...
(
  cd go-gost
  go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...
)
(
  cd go-gost/x
  go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...
)

(
  cd vite-frontend
  if [[ -f package-lock.json ]]; then
    npm ci
  else
    npm install
  fi
  npm run check
  npm audit --omit=dev --audit-level=moderate
)

# web/dist is gitignored and only needed for go:embed packaging / local binary runs.
if [[ "${SYNC_WEB_DIST:-0}" == "1" || "${RUN_PACKAGING:-0}" == "1" ]]; then
  bash "$root/scripts/sync-web-dist.sh"
  # 同步后强制校验真实入口及默认 CSP 的响应范围；缺少构建产物会失败。
  go test -tags webdist ./web -count=1
fi

if [[ "${RUN_PACKAGING:-0}" == "1" ]]; then
  docker build -t go-panel:verify .
fi

if [[ "${SKIP_GIT_DIFF:-0}" != "1" ]]; then
  git diff --exit-code
fi
