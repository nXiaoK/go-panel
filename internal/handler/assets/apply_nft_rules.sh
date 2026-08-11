#!/bin/bash
set -euo pipefail

unset LD_LIBRARY_PATH

CONFIG_FILE="/etc/flux-nftables/config.env"
[ -f "$CONFIG_FILE" ] || exit 1
# shellcheck disable=SC1090
source "$CONFIG_FILE"

FORWARD_SYSCTL_TMP=""
TMP_RULES=""
TMP_JSON=""
cleanup() {
  [[ -z "$FORWARD_SYSCTL_TMP" ]] || rm -f "$FORWARD_SYSCTL_TMP"
  [[ -z "$TMP_RULES" ]] || rm -f "$TMP_RULES"
  [[ -z "$TMP_JSON" ]] || rm -f "$TMP_JSON"
}
trap cleanup EXIT

# 此配置持久启用 Flux Panel IPv4 DNAT 所需的内核路由；默认必须为 1，
# 否则规则虽可写入 prerouting，数据包也不会进入 forward 链。
FORWARD_SYSCTL_FILE="/etc/sysctl.d/99-flux-nftables-forwarding.conf"
# 远程组件升级会直接替换本脚本而不重跑安装器，因此每次应用规则都重新收敛
# 持久配置和运行时值。该主机级开关可能影响其他防火墙或路由服务。
FORWARD_SYSCTL_TMP=$(mktemp)
cat > "$FORWARD_SYSCTL_TMP" <<'EOF'
# Flux Panel 的 IPv4 DNAT 转发依赖内核路由；默认必须为 1，否则数据包不会进入 forward 链。
# 这是主机级网络开关，可能影响同机其他防火墙或路由服务；卸载时仅删除本文件，不强制关闭运行时值。
net.ipv4.ip_forward = 1
EOF
install -d -m 0755 "$(dirname "$FORWARD_SYSCTL_FILE")"
install -m 0644 "$FORWARD_SYSCTL_TMP" "$FORWARD_SYSCTL_FILE"
rm -f "$FORWARD_SYSCTL_TMP"
FORWARD_SYSCTL_TMP=""
if ! printf '1\n' > /proc/sys/net/ipv4/ip_forward; then
  echo "无法启用 net.ipv4.ip_forward，nftables DNAT 不能转发流量" >&2
  exit 1
fi
if [[ "$(< /proc/sys/net/ipv4/ip_forward)" != "1" ]]; then
  echo "net.ipv4.ip_forward 未保持为 1，nftables DNAT 不能转发流量" >&2
  exit 1
fi

PANEL_BASE_URL="${SERVER_ADDR%/}"
if [[ "$PANEL_BASE_URL" != http://* && "$PANEL_BASE_URL" != https://* ]]; then
  PANEL_BASE_URL="http://${PANEL_BASE_URL}"
fi
RULE_URL="${PANEL_BASE_URL}/api/v1/node/nft-config?secret=${SECRET}"
CURL_INITIAL_PROTOCOLS="=https"
[[ "$RULE_URL" == http://* ]] && CURL_INITIAL_PROTOCOLS="=http,https"

TMP_RULES=$(mktemp)
TMP_JSON=$(mktemp)

curl --proto "$CURL_INITIAL_PROTOCOLS" --proto-redir '=https' -fsSL "$RULE_URL" -o "$TMP_JSON"
/etc/flux-nftables/nft_rule_payload "$TMP_JSON" "$TMP_RULES"

exec /etc/flux-nftables/nft_flow_reporter \
  --refresh "$TMP_RULES" "$SERVER_ADDR" "$SECRET" "$NFT_TABLE_NAME"
