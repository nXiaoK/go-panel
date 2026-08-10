#!/bin/sh
set -eu

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
ASSET_DIR="$ROOT_DIR/node-assets"
GO_LDFLAGS="-s -w"
GOCACHE="${GOCACHE:-${TMPDIR:-/tmp}/go-panel-go-build}"
GOMODCACHE="${GOMODCACHE:-${TMPDIR:-/tmp}/go-panel-go-mod}"

mkdir -p "$ASSET_DIR"
mkdir -p "$GOCACHE"
mkdir -p "$GOMODCACHE"
export GOCACHE GOMODCACHE

build_root_tool() {
  name="$1"
  arch="$2"
  out="$3"
  echo "building $out"
  (
    cd "$ROOT_DIR"
    CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -ldflags "$GO_LDFLAGS" -o "$ASSET_DIR/$out" "./cmd/node-assets/$name"
  )
}

build_gost() {
  arch="$1"
  out="$2"
  echo "building $out"
  (
    cd "$ROOT_DIR/go-gost"
    CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -ldflags "$GO_LDFLAGS" -o "$ASSET_DIR/$out" .
  )
}

for arch in amd64 arm64; do
  build_gost "$arch" "gost-$arch"
  build_root_tool nft_agent "$arch" "nft_agent_$arch"
  build_root_tool nft_rule_payload "$arch" "nft_rule_payload_$arch"
  build_root_tool nft_flow_reporter "$arch" "nft_flow_reporter_$arch"
done

chmod +x "$ASSET_DIR"/gost-* "$ASSET_DIR"/nft_* || true

echo "node assets written to $ASSET_DIR"
