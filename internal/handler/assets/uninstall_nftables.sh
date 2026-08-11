#!/bin/bash

YES=0
DELETE_SELF=1
INSTALL_DIR="/etc/flux-nftables"
# 此目录保存流量 reporter 的持久化 journal 和 active marker；完整卸载会删除它，
# 因此尚未确认上报的流量将无法恢复。组件更新不得清理此目录。
STATE_DIR="/var/lib/flux-nftables"
SERVICE_NAME="flux-nftables.service"
AGENT_SERVICE_NAME="flux-nftables-agent.service"
SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}"
AGENT_SERVICE_FILE="/etc/systemd/system/${AGENT_SERVICE_NAME}"
SERVICE_OVERRIDE_DIR="/etc/systemd/system/${SERVICE_NAME}.d"
AGENT_SERVICE_OVERRIDE_DIR="/etc/systemd/system/${AGENT_SERVICE_NAME}.d"
# 安装器用此配置持久启用 IPv4 DNAT 转发；卸载只删除文件，不强制把运行时值改回 0，
# 避免中断同机 Docker、VPN、路由器等其他依赖内核转发的服务。
SYSCTL_FILE="/etc/sysctl.d/99-flux-nftables-forwarding.conf"
# 固定历史表名限定项目所有权；卸载只额外接受严格的 32 位小写十六进制代际表，
# 不从可编辑配置扩大删除范围，避免误删第三方 nftables 表。
NFT_TABLE_NAME="flux_panel"

show_help() {
  echo "Usage: $0 [options]"
  echo ""
  echo "Options:"
  echo "  -y, --yes        Run without confirmation"
  echo "  --keep-script    Do not delete this script after completion"
  echo "  -h, --help       Show this help"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    -y|--yes)
      YES=1
      ;;
    --keep-script)
      DELETE_SELF=0
      ;;
    -h|--help)
      show_help
      exit 0
      ;;
    *)
      echo "Unknown option: $1"
      show_help
      exit 1
      ;;
  esac
  shift
done

get_sudo_cmd() {
  if [[ $EUID -eq 0 ]]; then
    echo ""
    return 0
  fi
  if command -v sudo >/dev/null 2>&1; then
    echo "sudo"
    return 0
  fi
  echo "This script must be run as root or on a system with sudo." >&2
  return 1
}

SUDO_CMD=$(get_sudo_cmd) || exit 1

run_cmd() {
  if [[ -n "$SUDO_CMD" ]]; then
    "$SUDO_CMD" "$@"
  else
    "$@"
  fi
}

run_systemctl() {
  command -v systemctl >/dev/null 2>&1 || return 0
  run_cmd systemctl "$@"
}

delete_self() {
  local exit_status="$?"
  [[ "$exit_status" -eq 0 ]] || return 0
  [[ "$DELETE_SELF" -eq 1 ]] || return 0
  local script_path
  script_path="$(readlink -f "$0" 2>/dev/null || realpath "$0" 2>/dev/null || echo "$0")"
  [[ -f "$script_path" ]] || return 0
  rm -f "$script_path" >/dev/null 2>&1 || true
}
trap delete_self EXIT

confirm_uninstall() {
  [[ "$YES" -eq 1 ]] && return 0
  read -r -p "Uninstall nftables node and remove all Flux Panel nftables files, including unreported traffic journal data? (y/N): " confirm
  [[ "$confirm" == "y" || "$confirm" == "Y" ]]
}

remove_path() {
  local path="$1"
  if [[ -e "$path" || -L "$path" ]]; then
    run_cmd rm -rf "$path"
    echo "Removed: $path"
  fi
}

kill_residual_processes() {
  command -v pkill >/dev/null 2>&1 || return 0

  local patterns=(
    "${INSTALL_DIR}/nft_agent"
    "${INSTALL_DIR}/nft_flow_reporter"
    "${INSTALL_DIR}/nft_rule_payload"
    "${INSTALL_DIR}/apply_rules.sh"
  )

  local pattern
  for pattern in "${patterns[@]}"; do
    run_cmd pkill -f "$pattern" >/dev/null 2>&1 || true
  done
}

list_managed_nft_tables() {
  command -v nft >/dev/null 2>&1 || return 1

  local inventory keyword family table_name remainder
  if ! inventory="$(run_cmd nft list tables 2>/dev/null)"; then
    return 1
  fi
  while read -r keyword family table_name remainder; do
    [[ "$keyword" == "table" && "$family" == "inet" ]] || continue
    if [[ "$table_name" == "$NFT_TABLE_NAME" || "$table_name" =~ ^flux_panel_g_[0-9a-f]{32}$ ]]; then
      printf '%s\n' "$table_name"
    fi
  done <<< "$inventory"
}

delete_managed_nft_tables() {
  local tables table_name remaining
  if ! tables="$(list_managed_nft_tables)"; then
    return 1
  fi
  while IFS= read -r table_name; do
    [[ -n "$table_name" ]] || continue
    run_cmd nft delete table inet "$table_name" >/dev/null 2>&1 || true
  done <<< "$tables"
  if ! remaining="$(list_managed_nft_tables)"; then
    return 1
  fi
  [[ -z "$remaining" ]]
}

verify_cleanup() {
  local failed=0
  local path remaining

  for path in "$SERVICE_FILE" "$AGENT_SERVICE_FILE" "$SERVICE_OVERRIDE_DIR" "$AGENT_SERVICE_OVERRIDE_DIR" \
    "$INSTALL_DIR" "$STATE_DIR" "$SYSCTL_FILE" "/run/flux-nftables" "/var/run/flux-nftables" \
    "/var/log/flux-nftables" "/var/log/flux-nftables.log"; do
    if [[ -e "$path" || -L "$path" ]]; then
      echo "Cleanup incomplete: $path" >&2
      failed=1
    fi
  done

  if ! remaining="$(list_managed_nft_tables)"; then
    echo "Cleanup incomplete: unable to verify nftables inventory." >&2
    failed=1
  elif [[ -n "$remaining" ]]; then
    echo "Cleanup incomplete: managed nftables tables remain: $remaining" >&2
    failed=1
  fi

  if [[ "$failed" -eq 0 ]]; then
    echo "nftables node uninstall completed."
    return 0
  else
    echo "nftables node uninstall is incomplete because some files or nftables state could not be removed. Please check permissions." >&2
    return 1
  fi
}

if ! confirm_uninstall; then
  echo "Uninstall cancelled."
  exit 0
fi

echo "Stopping Flux Panel nftables services..."
run_systemctl stop "$AGENT_SERVICE_NAME" >/dev/null 2>&1 || true
run_systemctl disable "$AGENT_SERVICE_NAME" >/dev/null 2>&1 || true
run_systemctl stop "$SERVICE_NAME" >/dev/null 2>&1 || true
run_systemctl disable "$SERVICE_NAME" >/dev/null 2>&1 || true

echo "Stopping residual nftables node processes..."
kill_residual_processes

echo "Removing Flux Panel nftables tables..."
if ! delete_managed_nft_tables; then
  echo "Unable to remove and verify all Flux Panel nftables tables; persistent journal data was retained. Fix nft permissions and rerun uninstall." >&2
  exit 1
fi

echo "Removing nftables service files and data..."
remove_path "$SERVICE_FILE"
remove_path "$AGENT_SERVICE_FILE"
remove_path "$SERVICE_OVERRIDE_DIR"
remove_path "$AGENT_SERVICE_OVERRIDE_DIR"
remove_path "$INSTALL_DIR"
remove_path "$STATE_DIR"
remove_path "$SYSCTL_FILE"
echo "Removed the persistent Flux Panel IPv4 forwarding setting; the current runtime ip_forward value was left unchanged to protect other network services."
remove_path "/run/flux-nftables"
remove_path "/var/run/flux-nftables"
remove_path "/var/log/flux-nftables"
remove_path "/var/log/flux-nftables.log"

shopt -s nullglob
for residual_path in /tmp/flux-nftables-* /tmp/flux_nftables_* /tmp/flux-panel-nftables-* /var/log/flux-nftables-*.log; do
  remove_path "$residual_path"
done
shopt -u nullglob

run_systemctl daemon-reload >/dev/null 2>&1 || true
run_systemctl reset-failed "$SERVICE_NAME" "$AGENT_SERVICE_NAME" >/dev/null 2>&1 || true

verify_cleanup
