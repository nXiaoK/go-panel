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
  # README 使用相对路径引用运维指南和界面截图，发布包必须一并携带，保证离线阅读不缺图。
  cp "$ROOT_DIR/README.md" "$ROOT_DIR/OPERATIONS.md" "$ROOT_DIR/LICENSE" "$ROOT_DIR/THIRD_PARTY_NOTICES.md" "$bundle_dir/"
  mkdir -p "$bundle_dir/assets"
  cp -R "$ROOT_DIR/assets/screenshots" "$bundle_dir/assets/"
  # macOS 自带的 bsdtar 会为扩展属性写入 ._ 开头的 AppleDouble 条目，它们位于包根目录之外，
  # 会被面板的更新包校验拒绝；COPYFILE_DISABLE=1 禁止生成这类条目，对 GNU tar 无影响。
  COPYFILE_DISABLE=1 tar -C "$OUTPUT_DIR" -czf "$OUTPUT_DIR/$bundle_name.tar.gz" "$bundle_name"
done

# 生成更新清单：记录版本、兼容声明以及各架构压缩包和其中面板、节点程序的 SHA-256，
# 面板软更新据此逐文件校验。清单不在本脚本中签名（本地没有私钥），由 CI 的 release-assets
# 作业用受保护环境中的私钥签名。仓库名默认取 CI 提供的 GITHUB_REPOSITORY，便于在 fork 中演练。
(
  cd "$ROOT_DIR"
  go run ./tools/releasetool manifest \
    --release-dir "$OUTPUT_DIR" \
    --version "$VERSION" \
    --commit "$COMMIT" \
    --build-time "$BUILD_TIME" \
    --repository "${GITHUB_REPOSITORY:-nXiaoK/go-panel}" \
    --out "$OUTPUT_DIR/update-manifest.json"
)

# SHA256SUMS 供人工核对下载内容，同时覆盖压缩包与更新清单；软更新只信任经签名的清单。
(
  cd "$OUTPUT_DIR"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum ./*.tar.gz ./update-manifest.json > SHA256SUMS
  else
    shasum -a 256 ./*.tar.gz ./update-manifest.json > SHA256SUMS
  fi
)

echo "Release 产物已写入 $OUTPUT_DIR"
