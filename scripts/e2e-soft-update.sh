#!/usr/bin/env bash
# 软更新容器端到端测试：在本机 Docker 中用真实镜像、真实 supervisor 与面板管理 API 验证完整链路。
#
# 测试版本用 selfupdate_e2e 构建标签编译，信任一把只存在于临时目录的测试公钥，并从容器网络内的
# 假 Release 服务器下载更新；正式构建不带此标签，CI 会检查构建脚本中不出现它。脚本不会推送镜像，
# 也不会访问真实的 GitHub。依赖：docker、go、python3、curl、openssl。
#
# 可选环境变量：
#   E2E_BASE_IMAGE  已构建的官方镜像（例如 go-panel:soft-update-test），留空时先执行 docker build
#   E2E_PORT        面板映射到 127.0.0.1 的端口，默认 16480
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"
for tool in docker go python3 curl openssl; do
  command -v "$tool" >/dev/null 2>&1 || { echo "缺少依赖：$tool" >&2; exit 1; }
done

work="$(mktemp -d "${TMPDIR:-/tmp}/go-panel-e2e.XXXXXX")"
tag="gp-e2e-$$"
network="$tag-net"
volume="$tag-data"
panel="$tag-panel"
releases="$tag-releases"
image_a="$tag-image-a"
image_e="$tag-image-e"
port="${E2E_PORT:-16480}"
base_image="${E2E_BASE_IMAGE:-}"
built_base=""
admin_user="e2e_admin"
admin_password="E2e-Only-$(openssl rand -hex 8)"
jwt_secret="$(openssl rand -hex 32)"
base_url="http://127.0.0.1:$port"
token=""
completed=""

cleanup() {
  local status=$?
  # 未执行到脚本末尾一律视为失败：部分 bash 版本在 set -u 报错时传给 EXIT trap 的状态不可靠。
  if [ -z "$completed" ] && [ "$status" -eq 0 ]; then
    status=1
  fi
  if [ "$status" -ne 0 ] && docker ps -a --format '{{.Names}}' | grep -qx "$panel"; then
    echo "---- 面板容器日志（最后 80 行）----" >&2
    docker logs --tail 80 "$panel" >&2 || true
  fi
  docker rm -f "$panel" "$releases" >/dev/null 2>&1 || true
  docker volume rm "$volume" >/dev/null 2>&1 || true
  docker network rm "$network" >/dev/null 2>&1 || true
  docker image rm "$image_a" "$image_e" >/dev/null 2>&1 || true
  if [ -n "$built_base" ]; then
    docker image rm "$base_image" >/dev/null 2>&1 || true
  fi
  rm -rf "$work"
  exit "$status"
}
trap cleanup EXIT

step() { printf '\n==> %s\n' "$*"; }
fail() { echo "失败：$*" >&2; exit 1; }

case "$(docker info --format '{{.Architecture}}')" in
  x86_64 | amd64) goarch=amd64 ;;
  aarch64 | arm64) goarch=arm64 ;;
  *) fail "不支持的 Docker 架构" ;;
esac
commit="$(git rev-parse HEAD)"
build_time="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

step "准备基础镜像"
if [ -z "$base_image" ]; then
  base_image="$tag-base"
  built_base=1
  docker build -q -t "$base_image" . >/dev/null
fi

step "构建测试版本（selfupdate_e2e 标签，linux/${goarch}）"
build_panel() { # 版本 行为 输出路径
  CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" GOFLAGS=-buildvcs=false go build -trimpath -tags selfupdate_e2e \
    -ldflags "-s -w -X github.com/nXiaoK/go-panel/internal/buildinfo.Version=$1 -X github.com/nXiaoK/go-panel/internal/buildinfo.Commit=$commit -X github.com/nXiaoK/go-panel/internal/buildinfo.BuildTime=$build_time -X main.e2eBehavior=$2" \
    -o "$3" .
}
mkdir -p "$work/bin"
build_panel v9.0.0 "" "$work/bin/v9.0.0"
build_panel v9.1.0 "" "$work/bin/v9.1.0"
build_panel v9.2.0 crash-on-start "$work/bin/v9.2.0"
build_panel v9.3.0 never-ready "$work/bin/v9.3.0"
build_panel v9.5.0 "" "$work/bin/v9.5.0"

make_image() { # 镜像名 面板程序
  mkdir -p "$work/image-$1"
  cp "$2" "$work/image-$1/go-panel"
  printf 'FROM %s\nCOPY go-panel /app/go-panel\n' "$base_image" >"$work/image-$1/Dockerfile"
  docker build -q -t "$1" "$work/image-$1" >/dev/null
}
make_image "$image_a" "$work/bin/v9.0.0"
make_image "$image_e" "$work/bin/v9.5.0"

step "生成测试签名密钥（只保存在临时目录）"
keygen_output="$(GOFLAGS=-buildvcs=false go run ./tools/releasetool keygen --out "$work/release.key")"
pubkey="$(printf '%s\n' "$keygen_output" | sed -n 's/^公钥（base64）：//p')"
[ -n "$pubkey" ] || fail "无法解析测试公钥"

step "打包并签名测试 Release"
www="$work/www"
make_release() { # 版本
  local version="$1"
  local root_name="go-panel-$version-linux-$goarch"
  local src="$work/src/$version"
  local out="$www/nXiaoK/go-panel/releases/download/$version"
  mkdir -p "$src/$root_name/node-assets" "$out"
  cp "$work/bin/$version" "$src/$root_name/go-panel"
  printf 'e2e-gost-%s\n' "$version" >"$src/$root_name/node-assets/gost-amd64"
  chmod 755 "$src/$root_name/go-panel" "$src/$root_name/node-assets/gost-amd64"
  (cd "$src" && COPYFILE_DISABLE=1 tar -czf "$out/$root_name.tar.gz" "$root_name")
  GOFLAGS=-buildvcs=false go run ./tools/releasetool manifest --release-dir "$out" --version "$version" \
    --commit "$commit" --build-time "$build_time" --repository nXiaoK/go-panel --platforms "linux/$goarch" \
    --out "$out/update-manifest.json" >/dev/null
  GO_PANEL_E2E_SIGNING_KEY="$(cat "$work/release.key")" GOFLAGS=-buildvcs=false go run ./tools/releasetool sign \
    --manifest "$out/update-manifest.json" --key-env GO_PANEL_E2E_SIGNING_KEY --out "$out/update-manifest.json.sig" >/dev/null
}
for version in v9.1.0 v9.2.0 v9.3.0; do
  make_release "$version"
done

set_latest() { # 版本：改写假 GitHub API 的 releases/latest
  python3 - "$www" "$1" <<'PY'
import json, os, sys
www, version = sys.argv[1:]
base = f"{www}/nXiaoK/go-panel/releases/download/{version}"
assets = [{"name": name, "size": os.path.getsize(os.path.join(base, name)),
           "browser_download_url": f"https://github.com/nXiaoK/go-panel/releases/download/{version}/{name}"}
          for name in sorted(os.listdir(base))]
os.makedirs(f"{www}/repos/nXiaoK/go-panel/releases", exist_ok=True)
with open(f"{www}/repos/nXiaoK/go-panel/releases/latest", "w") as handle:
    json.dump({"tag_name": version, "name": f"go-panel {version}", "body": "e2e",
               "html_url": f"https://github.com/nXiaoK/go-panel/releases/tag/{version}",
               "published_at": "2026-11-01T00:00:00Z", "assets": assets}, handle)
PY
}

json_get() { # Python 表达式，d 为解析后的 JSON
  python3 -c 'import json, sys; d = json.load(sys.stdin); print(eval(sys.argv[1], {"d": d}))' "$1"
}

api() { # 路径 [JSON 请求体]
  local body="${2:-}"
  [ -n "$body" ] || body='{}'
  curl -fsS -m 30 -X POST -H "Content-Type: application/json" -H "Authorization: $token" -d "$body" "$base_url/api/v1$1"
}

start_panel() { # 镜像 [docker run 额外参数...]
  local image="$1"
  shift
  docker rm -f "$panel" >/dev/null 2>&1 || true
  docker run -d --name "$panel" --network "$network" -p "127.0.0.1:$port:6365" -v "$volume:/app/data" \
    -e ENVIRONMENT=production -e JWT_SECRET="$jwt_secret" -e ADMIN_USERNAME="$admin_user" \
    -e ADMIN_PASSWORD="$admin_password" -e CORS_ALLOW_ORIGIN="$base_url" \
    -e GO_PANEL_E2E_TRUSTED_PUBKEY="$pubkey" -e GO_PANEL_E2E_RELEASE_BASE_URL="http://$releases:8080" \
    -e GO_PANEL_E2E_READY_TIMEOUT=15s -e GO_PANEL_E2E_STABLE_PERIOD=5s \
    "$@" "$image" >/dev/null
  local i
  for i in $(seq 1 120); do
    curl -fsS -m 2 "$base_url/healthz" >/dev/null 2>&1 && return 0
    sleep 1
  done
  fail "面板未在 120 秒内就绪"
}

login() {
  token="$(curl -fsS -m 10 -X POST -H 'Content-Type: application/json' \
    -d "{\"username\":\"$admin_user\",\"password\":\"$admin_password\"}" "$base_url/api/v1/user/login" | json_get "d['data']['token']")"
  [ -n "$token" ] || fail "管理员登录失败"
}

current_version() { api /system/version 2>/dev/null | json_get "d['data']['version']" 2>/dev/null || true; }
status_value() { api /system/update/status 2>/dev/null | json_get "$1" 2>/dev/null || true; }

wait_for() { # 描述 超时秒数 条件函数 [参数...]
  local description="$1" timeout="$2"
  shift 2
  local deadline=$(($(date +%s) + timeout))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    if "$@"; then
      echo "    ✓ $description"
      return 0
    fi
    sleep 2
  done
  fail "${description}（等待 ${timeout} 秒超时；最新状态：$(api /system/update/status 2>/dev/null || echo 不可用)）"
}

running_committed() { # 版本 运行方式
  [ "$(current_version)" = "$1" ] &&
    [ "$(status_value "d['data']['runtime']['mode']")" = "$2" ] &&
    [ "$(status_value "d['data']['runtime']['lastResult']['outcome']")" = "committed" ]
}

rolled_back_from() { # 失败版本 阶段 当前版本
  [ "$(current_version)" = "$3" ] &&
    [ "$(status_value "d['data']['runtime']['lastResult']['outcome']")" = "rolled_back" ] &&
    [ "$(status_value "d['data']['runtime']['lastResult']['version']")" = "$1" ] &&
    [ "$(status_value "d['data']['runtime']['lastResult']['stage']")" = "$2" ]
}

latest_is() { # 版本：模拟管理员手动检查；距上次检查不足 30 秒时后端返回缓存，因此轮询等待
  [ "$(api /system/update/check '{"force":true}' 2>/dev/null | json_get "d['data']['latestVersion']" 2>/dev/null || true)" = "$1" ]
}

apply_latest() { # 期望版本
  local apply
  wait_for "检查到最新版本 $1" 90 latest_is "$1"
  apply="$(api /system/update/apply "{\"targetVersion\":\"$1\",\"force\":true}")"
  [ "$(printf '%s' "$apply" | json_get "d['code']")" = "0" ] || fail "启动软更新失败：$apply"
}

step "启动假 Release 服务器与镜像内置 v9.0.0"
docker network create "$network" >/dev/null
docker volume create "$volume" >/dev/null
# Alpine 的 busybox 不含 httpd；官方 busybox 镜像自带，固定精确版本保证测试可重复。
docker run -d --name "$releases" --network "$network" -v "$www:/www:ro" busybox:1.37.0 httpd -f -p 8080 -h /www >/dev/null
start_panel "$image_a"
login
[ "$(current_version)" = "v9.0.0" ] || fail "初始版本应为 v9.0.0"

step "场景 1：软更新到 v9.1.0，通过试运行后正式启用，节点程序随之切换"
set_latest v9.1.0
wait_for "检查到最新版本 v9.1.0" 90 latest_is v9.1.0
check="$(api /system/update/status)"
[ "$(printf '%s' "$check" | json_get "d['data']['softUpdate']['available']")" = "True" ] || fail "v9.1.0 应可软更新：$check"
apply_latest v9.1.0
wait_for "运行 v9.1.0 并完成试运行" 180 running_committed v9.1.0 release
asset="$(curl -fsS -m 10 "$base_url/api/v1/node/assets/gost-amd64")"
[ "$asset" = "e2e-gost-v9.1.0" ] || fail "节点程序未随面板切换：$asset"
echo "    ✓ /api/v1/node/assets/gost-amd64 已是 v9.1.0 附带的版本"

step "场景 2：重建容器保留软更新版本；应急开关临时运行镜像内置版本"
start_panel "$image_a" -e PANEL_USE_BUILTIN=true
[ "$(current_version)" = "v9.0.0" ] || fail "PANEL_USE_BUILTIN=true 时应运行镜像内置 v9.0.0"
echo "    ✓ 应急开关生效"
start_panel "$image_a"
[ "$(current_version)" = "v9.1.0" ] || fail "关闭应急开关并重建容器后应继续运行 v9.1.0"
echo "    ✓ 重建容器后继续运行 v9.1.0"

step "场景 3：启动即失败的 v9.2.0 自动回滚并被隔离"
set_latest v9.2.0
apply_latest v9.2.0
wait_for "回滚到 v9.1.0" 180 rolled_back_from v9.2.0 start v9.1.0
wait_for "回滚后重新检查到 v9.2.0" 90 latest_is v9.2.0
reason="$(api /system/update/status | json_get "d['data']['softUpdate']['reason']")"
[ "$reason" = "quarantined" ] || fail "失败版本应被隔离，实际原因：$reason"
echo "    ✓ v9.2.0 已隔离"

step "场景 4：永不就绪的 v9.3.0 在就绪超时后回滚"
set_latest v9.3.0
apply_latest v9.3.0
wait_for "就绪超时后回滚到 v9.1.0" 180 rolled_back_from v9.3.0 ready_timeout v9.1.0

step "场景 5：在面板中回滚到上一版本（镜像内置 v9.0.0）"
rollback="$(api /system/update/rollback '{"target":"previous"}')"
[ "$(printf '%s' "$rollback" | json_get "d['code']")" = "0" ] || fail "回滚请求失败：$rollback"
wait_for "回滚到镜像内置 v9.0.0" 120 running_committed v9.0.0 builtin

step "场景 6：再次软更新后改用更新的镜像 v9.5.0，以镜像为准"
set_latest v9.1.0
apply_latest v9.1.0
wait_for "再次运行 v9.1.0" 180 running_committed v9.1.0 release
start_panel "$image_e"
[ "$(current_version)" = "v9.5.0" ] || fail "更换镜像后应运行镜像内置 v9.5.0"
[ "$(status_value "d['data']['runtime']['mode']")" = "builtin" ] || fail "更换镜像后应以镜像版本运行"
releases_left="$(docker exec "$panel" sh -c 'ls /app/data/runtime/releases | wc -l' | tr -d ' ')"
[ "$releases_left" = "0" ] || fail "更换镜像后软更新版本应被清理，剩余 $releases_left 个"
echo "    ✓ 软更新版本已退役并清理"

completed=1
printf '\n全部场景通过。\n'
