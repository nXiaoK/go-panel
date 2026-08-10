#!/bin/sh
set -eu

# GitHub Release 产物构建入口。调用前必须已把前端同步到 web/dist。
VERSION=${1:?需要版本号，例如 v0.1.0}
COMMIT=${2:?需要 Git Commit}
BUILD_TIME=${3:?需要 UTC 构建时间}

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
OUTPUT_DIR="$ROOT_DIR/release"
LDFLAGS="-s -w -X github.com/nXiaoK/go-panel/internal/buildinfo.Version=$VERSION -X github.com/nXiaoK/go-panel/internal/buildinfo.Commit=$COMMIT -X github.com/nXiaoK/go-panel/internal/buildinfo.BuildTime=$BUILD_TIME"

mkdir -p "$OUTPUT_DIR"
"$ROOT_DIR/scripts/build-node-assets.sh"

for arch in amd64 arm64; do
  bundle_name="go-panel-${VERSION}-linux-${arch}"
  bundle_dir="$OUTPUT_DIR/$bundle_name"
  if [ -e "$bundle_dir" ] || [ -e "$OUTPUT_DIR/$bundle_name.tar.gz" ]; then
    echo "release 目标已存在：$bundle_name" >&2
    exit 1
  fi
  mkdir -p "$bundle_dir/node-assets"

  (
    cd "$ROOT_DIR"
    CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -ldflags "$LDFLAGS" -o "$bundle_dir/go-panel" .
  )
  cp "$ROOT_DIR"/node-assets/gost-* "$bundle_dir/node-assets/"
  cp "$ROOT_DIR"/node-assets/nft_* "$bundle_dir/node-assets/"
  cp "$ROOT_DIR/compose.yml" "$ROOT_DIR/compose.update.yml" "$ROOT_DIR/.env.example" "$bundle_dir/"
  cp "$ROOT_DIR/README.md" "$ROOT_DIR/LICENSE" "$ROOT_DIR/THIRD_PARTY_NOTICES.md" "$bundle_dir/"
  tar -C "$OUTPUT_DIR" -czf "$OUTPUT_DIR/$bundle_name.tar.gz" "$bundle_name"
done

(
  cd "$OUTPUT_DIR"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum ./*.tar.gz > SHA256SUMS
  else
    shasum -a 256 ./*.tar.gz > SHA256SUMS
  fi
)

echo "Release 产物已写入 $OUTPUT_DIR"
