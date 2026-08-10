#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source_dir="$root/vite-frontend/dist/"
target_dir="$root/web/dist/"

test -f "${source_dir}index.html"
if command -v rsync >/dev/null 2>&1; then
  rsync -a --delete --exclude .gitkeep "$source_dir" "$target_dir"
else
  rm -rf "${target_dir:?}/"*
  mkdir -p "$target_dir"
  cp -a "$source_dir". "$target_dir"
fi
touch "${target_dir}.gitkeep"
echo "synced $source_dir -> $target_dir"
