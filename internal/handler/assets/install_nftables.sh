#!/bin/bash

LOCAL_MODE=0
UPDATE_MODE=0
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

get_sudo_cmd() {
    if [[ $EUID -ne 0 ]]; then
        echo "sudo"
    else
        echo ""
    fi
}

get_config_params() {
  if [[ -z "$SERVER_ADDR" || -z "$SECRET" ]]; then
    echo "请输入配置参数："

    if [[ -z "$SERVER_ADDR" ]]; then
      read -p "服务器地址: " SERVER_ADDR
    fi

    if [[ -z "$SECRET" ]]; then
      read -p "密钥: " SECRET
    fi

    if [[ -z "$SERVER_ADDR" || -z "$SECRET" ]]; then
      echo "❌ 参数不完整，操作取消。"
      exit 1
    fi
  fi
}

show_help() {
  echo "用法: $0 [选项]"
  echo ""
  echo "选项:"
  echo "  -a <地址>    服务器地址"
  echo "  -s <密钥>    节点密钥"
  echo "  -l           本地安装模式（从同级 scripts 目录读取二进制文件）"
  echo "  -u           更新模式（重新下载二进制并重启服务，复用已有配置）"
  echo "  -h           显示帮助信息"
  echo ""
  echo "示例:"
  echo "  $0 -a 192.168.1.1:8080 -s your_secret"
  echo "  $0 -l -a 192.168.1.1:8080 -s your_secret"
  echo "  $0 -u"
  exit 0
}

while getopts "a:s:luh" opt; do
  case $opt in
    a) SERVER_ADDR="$OPTARG" ;;
    s) SECRET="$OPTARG" ;;
    l) LOCAL_MODE=1 ;;
    u) UPDATE_MODE=1 ;;
    h) show_help ;;
    *) echo "❌ 无效参数"; exit 1 ;;
  esac
done

INSTALL_DIR="/etc/flux-nftables"
# 此目录保存流量 reporter 的持久化 journal 和 active marker；完整卸载会删除它，
# 因此尚未确认上报的流量将无法恢复。组件更新不得清理此目录。
STATE_DIR="/var/lib/flux-nftables"
# 活动表标记由 reporter 在完整同步面板规则并完成原子切换后写入；实际表通常是
# flux_panel_g_<32位hex>，运维检查不得固定假设仍使用历史表 flux_panel。
ACTIVE_TABLE_MARKER="$STATE_DIR/active-table"
# Agent 每次建立 WebSocket 会话都会先清除此运行时标记，首次全量规则对账成功后
# 再写入当时代际表；安装输出只有在它与 active-table 一致时才可信。
AGENT_SYNC_MARKER="/run/flux-nftables/agent-synced"
SERVICE_FILE="/etc/systemd/system/flux-nftables.service"
AGENT_SERVICE_FILE="/etc/systemd/system/flux-nftables-agent.service"
SCRIPT_FILE="$INSTALL_DIR/apply_rules.sh"
AGENT_FILE="$INSTALL_DIR/nft_agent"
RULE_HELPER_FILE="$INSTALL_DIR/nft_rule_payload"
FLOW_REPORTER_FILE="$INSTALL_DIR/nft_flow_reporter"
ENV_FILE="$INSTALL_DIR/config.env"
# 此 sysctl 配置持久启用 IPv4 路由；没有它时 DNAT 可出现在 prerouting，
# 但内核不会把数据包送入 forward 链，表现为规则存在、计数始终为 0。
SYSCTL_FILE="/etc/sysctl.d/99-flux-nftables-forwarding.conf"
# 固定历史表名限定项目所有权；卸载只额外接受严格的 32 位小写十六进制代际表，
# 不从可编辑配置扩大删除范围，避免误删第三方 nftables 表。
NFT_TABLE_NAME="flux_panel"

run_privileged() {
  local sudo_cmd
  sudo_cmd=$(get_sudo_cmd)
  if [[ -n "$sudo_cmd" ]]; then
    "$sudo_cmd" "$@"
  else
    "$@"
  fi
}

verified_active_nft_table() {
  local table_name synced_table final_table final_synced_table
  run_privileged systemctl is-active --quiet flux-nftables.service || return 1
  run_privileged systemctl is-active --quiet flux-nftables-agent.service || return 1
  if ! table_name="$(run_privileged cat "$ACTIVE_TABLE_MARKER" 2>/dev/null)"; then
    return 1
  fi
  if [[ "$table_name" != "$NFT_TABLE_NAME" ]] && [[ ! "$table_name" =~ ^flux_panel_g_[0-9a-f]{32}$ ]]; then
    return 1
  fi
  if ! synced_table="$(run_privileged cat "$AGENT_SYNC_MARKER" 2>/dev/null)"; then
    return 1
  fi
  [[ "$synced_table" == "$table_name" ]] || return 1
  run_privileged nft list table inet "$table_name" >/dev/null 2>&1 || return 1
  # nft 表验证期间仍可能发生下一次代际切换；复读两个标记可避免打印刚被替换的表名。
  final_table="$(run_privileged cat "$ACTIVE_TABLE_MARKER" 2>/dev/null)" || return 1
  final_synced_table="$(run_privileged cat "$AGENT_SYNC_MARKER" 2>/dev/null)" || return 1
  [[ "$final_table" == "$table_name" && "$final_synced_table" == "$table_name" ]] || return 1
  printf '%s\n' "$table_name"
}

wait_for_verified_active_nft_table() {
  local attempt active_table
  # 最长等待 60 秒，覆盖节点首次 WebSocket 握手和面板完整对账；超时后明确失败，
  # 不回退打印仅由启动服务写入、随后可能马上失效的旧代际表。
  for ((attempt = 1; attempt <= 60; attempt++)); do
    if active_table="$(verified_active_nft_table)"; then
      printf '%s\n' "$active_table"
      return 0
    fi
    sleep 1
  done
  return 1
}

list_managed_nft_tables() {
  command -v nft >/dev/null 2>&1 || return 1

  local inventory keyword family table_name remainder
  if ! inventory="$(run_privileged nft list tables 2>/dev/null)"; then
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
    run_privileged nft delete table inet "$table_name" >/dev/null 2>&1 || true
  done <<< "$tables"
  if ! remaining="$(list_managed_nft_tables)"; then
    return 1
  fi
  [[ -z "$remaining" ]]
}

verify_uninstall_cleanup() {
  local failed=0
  local path remaining

  for path in "$SERVICE_FILE" "$AGENT_SERVICE_FILE" \
    "/etc/systemd/system/flux-nftables.service.d" "/etc/systemd/system/flux-nftables-agent.service.d" \
    "$INSTALL_DIR" "$STATE_DIR" "$SYSCTL_FILE" "/run/flux-nftables" "/var/run/flux-nftables" \
    "/var/log/flux-nftables" "/var/log/flux-nftables.log"; do
    if [[ -e "$path" || -L "$path" ]]; then
      echo "❌ 卸载后仍有残留: $path" >&2
      failed=1
    fi
  done

  if ! remaining="$(list_managed_nft_tables)"; then
    echo "❌ 无法确认 nftables 表是否清理完成" >&2
    failed=1
  elif [[ -n "$remaining" ]]; then
    echo "❌ 卸载后仍有 Flux Panel nftables 表: $remaining" >&2
    failed=1
  fi
  [[ "$failed" -eq 0 ]]
}

build_panel_base_url() {
  local addr="$SERVER_ADDR"
  if [[ "$addr" == http://* || "$addr" == https://* ]]; then
    echo "${addr%/}"
  else
    echo "http://${addr}"
  fi
}

node_ipv6_loopback_host() {
  local host="$1"
  local left right part
  local -a left_parts right_parts parts
  left_parts=()
  right_parts=()
  parts=()

  [[ "$host" != *:::* ]] || return 1
  if [[ "$host" == *::* ]]; then
    left="${host%%::*}"
    right="${host#*::}"
    [[ "$right" != *::* ]] || return 1
    [[ -z "$left" ]] || IFS=: read -r -a left_parts <<< "$left"
    [[ -z "$right" ]] || IFS=: read -r -a right_parts <<< "$right"
    (( ${#left_parts[@]} + ${#right_parts[@]} < 8 )) || return 1
    parts=("${left_parts[@]}" "${right_parts[@]}")
  else
    [[ "$host" != :* && "$host" != *: ]] || return 1
    IFS=: read -r -a parts <<< "$host"
    (( ${#parts[@]} == 8 )) || return 1
  fi

  (( ${#parts[@]} > 0 )) || return 1
  for part in "${parts[@]:0:${#parts[@]}-1}"; do
    [[ "$part" =~ ^0{1,4}$ ]] || return 1
  done
  part="${parts[${#parts[@]}-1]}"
  [[ "$part" =~ ^0{0,3}1$ ]]
}

node_download_url_allowed() {
  local url="$1"
  local rest authority host a b c d
  case "$url" in
    https://*) rest="${url#https://}" ;;
    http://*) rest="${url#http://}" ;;
    *) return 1 ;;
  esac
  authority="${rest%%/*}"
  authority="${authority%%\?*}"
  authority="${authority%%\#*}"
  [[ -n "$authority" && "$authority" != *@* ]] || return 1

  [[ "$url" == https://* ]] && return 0
  [[ "${ALLOW_INSECURE_NODE_DOWNLOADS:-}" == "true" ]] && return 0

  if [[ "$authority" == \[* ]]; then
    [[ "$authority" =~ ^\[[^]]+\](:[0-9]+)?$ ]] || return 1
    host="${authority#\[}"
    host="${host%%\]*}"
    node_ipv6_loopback_host "$host"
    return
  fi
  host="${authority%%:*}"
  [[ "$host" =~ ^127\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || return 1
  IFS=. read -r a b c d <<< "$host"
  (( 10#$b <= 255 && 10#$c <= 255 && 10#$d <= 255 ))
}

download_node_file() {
  local url="$1"
  local output="$2"
  local initial_protocols="=https"
  if ! node_download_url_allowed "$url"; then
    echo "❌ 节点下载要求 HTTPS；仅 loopback 或显式 ALLOW_INSECURE_NODE_DOWNLOADS=true 可使用 HTTP。" >&2
    return 1
  fi
  [[ "$url" == http://* ]] && initial_protocols="=http,https"
  curl --proto "$initial_protocols" --proto-redir '=https' -fsSL "$url" -o "$output"
}

get_arch_suffix() {
  local arch
  arch=$(uname -m)
  case "$arch" in
    x86_64|amd64)
      echo "amd64"
      ;;
    aarch64|arm64)
      echo "arm64"
      ;;
    *)
      echo "amd64"
      ;;
  esac
}

install_staged_file() {
  local source_path="$1"
  local target_path="$2"
  local mode="$3"
  local SUDO_CMD
  SUDO_CMD=$(get_sudo_cmd)

  if ! $SUDO_CMD install -m "$mode" "$source_path" "$target_path"; then
    return 1
  fi
}

stage_support_script() {
  local source_name="$1"
  local target_path="$2"
  local mode="$3"
  local local_source
  local tmp_file

  tmp_file=$(mktemp)
  local_source="$(dirname "$0")/$source_name"
  if [ -f "$local_source" ]; then
    if ! cp "$local_source" "$tmp_file"; then
      rm -f "$tmp_file"
      return 1
    fi
  elif [ -f "./$source_name" ]; then
    if ! cp "./$source_name" "$tmp_file"; then
      rm -f "$tmp_file"
      return 1
    fi
  elif ! download_node_file "$(build_panel_base_url)/api/v1/node/install/$source_name" "$tmp_file"; then
    rm -f "$tmp_file"
    echo "❌ 无法获取 $source_name"
    return 1
  fi

  if ! install_staged_file "$tmp_file" "$target_path" "$mode"; then
    rm -f "$tmp_file"
    return 1
  fi

  rm -f "$tmp_file"
}

install_binary() {
  local binary_name="$1"
  local target_path="$2"
  local arch_suffix
  arch_suffix=$(get_arch_suffix)
  local tmp_file
  tmp_file=$(mktemp)

  if [[ $LOCAL_MODE -eq 1 ]]; then
    local local_binary="$SCRIPT_DIR/scripts/${binary_name}_${arch_suffix}"
    if [[ ! -f "$local_binary" ]]; then
      echo "❌ 本地模式: 未找到 $local_binary"
      echo "   请确保 scripts 目录下存在 ${binary_name}_${arch_suffix} 文件"
      rm -f "$tmp_file"
      return 1
    fi
    echo "📦 本地安装 ${binary_name}_${arch_suffix}..."
    if ! cp "$local_binary" "$tmp_file"; then
      rm -f "$tmp_file"
      return 1
    fi
  else
    local release_url="$(build_panel_base_url)/api/v1/node/assets/${binary_name}_${arch_suffix}"
    echo "📥 下载 ${binary_name}_${arch_suffix}..."
    if ! download_node_file "$release_url" "$tmp_file"; then
      rm -f "$tmp_file"
      echo "❌ 无法下载 ${binary_name}_${arch_suffix}"
      echo "   请确认面板服务端 node-assets 目录存在 ${binary_name}_${arch_suffix}"
      return 1
    fi
  fi

  if ! install_staged_file "$tmp_file" "$target_path" 0755; then
    rm -f "$tmp_file"
    return 1
  fi

  rm -f "$tmp_file"
  echo "✅ ${binary_name} 安装成功"
}

ensure_dependencies() {
  local SUDO_CMD
  SUDO_CMD=$(get_sudo_cmd)

  local need_install=0
  command -v nft >/dev/null 2>&1 || need_install=1
  command -v curl >/dev/null 2>&1 || need_install=1
  command -v iperf3 >/dev/null 2>&1 || need_install=1
  command -v conntrack >/dev/null 2>&1 || need_install=1
  command -v sysctl >/dev/null 2>&1 || need_install=1

  if [[ $need_install -eq 0 ]]; then
    return 0
  fi

  if [ -f /etc/os-release ]; then
    . /etc/os-release
    case "$ID" in
      ubuntu|debian)
        $SUDO_CMD apt update && $SUDO_CMD apt install -y nftables curl iperf3 conntrack procps
        ;;
      centos|rhel|rocky|almalinux|fedora)
        if command -v dnf >/dev/null 2>&1; then
          $SUDO_CMD dnf install -y nftables curl iperf3 conntrack-tools procps-ng
        else
          $SUDO_CMD yum install -y nftables curl iperf3 conntrack-tools procps-ng
        fi
        ;;
      alpine)
        $SUDO_CMD apk add --no-cache nftables curl iperf3 conntrack-tools procps
        ;;
      arch|manjaro)
        $SUDO_CMD pacman -Sy --noconfirm nftables curl iperf3 conntrack-tools procps-ng
        ;;
      *)
        echo "❌ 当前发行版暂未自动适配，请先手动安装 nftables/curl/iperf3/conntrack/sysctl"
        exit 1
        ;;
    esac
  else
    echo "❌ 无法识别系统，请先手动安装 nftables/curl/iperf3/conntrack/sysctl"
    exit 1
  fi
}

install_forwarding_sysctl() {
  local sysctl_tmp current
  sysctl_tmp=$(mktemp) || return 1
  cat > "$sysctl_tmp" <<'EOF'
# Flux Panel 的 IPv4 DNAT 转发依赖内核路由；默认必须为 1，否则数据包不会进入 forward 链。
# 这是主机级网络开关，可能影响同机其他防火墙或路由服务；卸载时仅删除本文件，不强制关闭运行时值。
net.ipv4.ip_forward = 1
EOF
  if ! install_staged_file "$sysctl_tmp" "$SYSCTL_FILE" 0644; then
    rm -f "$sysctl_tmp"
    return 1
  fi
  rm -f "$sysctl_tmp"

  if ! run_privileged sysctl -w net.ipv4.ip_forward=1 >/dev/null; then
    echo "❌ 无法启用 net.ipv4.ip_forward，nftables DNAT 不能转发流量" >&2
    return 1
  fi
  current="$(run_privileged sysctl -n net.ipv4.ip_forward 2>/dev/null)" || return 1
  if [[ "$current" != "1" ]]; then
    echo "❌ net.ipv4.ip_forward 当前值为 $current，期望为 1" >&2
    return 1
  fi
  echo "✅ IPv4 内核转发已启用并持久化"
}

write_rule_script() {
  stage_support_script "apply_nft_rules.sh" "$SCRIPT_FILE" 0755
}

install_service() {
  local service_tmp
  local agent_tmp

  service_tmp=$(mktemp)
  cat > "$service_tmp" <<EOF
[Unit]
Description=Flux Panel nftables Forward Service
# 必须等待系统 sysctl 配置加载完成，确保本服务恢复 DNAT 规则前 IPv4 转发已经启用。
After=network-online.target systemd-sysctl.service
Wants=network-online.target

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=$SCRIPT_FILE
ExecReload=$SCRIPT_FILE

[Install]
WantedBy=multi-user.target
EOF

  if ! install_staged_file "$service_tmp" "$SERVICE_FILE" 0644; then
    rm -f "$service_tmp"
    return 1
  fi
  rm -f "$service_tmp"

  agent_tmp=$(mktemp)
  cat > "$agent_tmp" <<EOF
[Unit]
Description=Flux Panel nftables WebSocket Agent
After=network-online.target flux-nftables.service
Wants=network-online.target

[Service]
Type=simple
ExecStart=$AGENT_FILE
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

  if ! install_staged_file "$agent_tmp" "$AGENT_SERVICE_FILE" 0644; then
    rm -f "$agent_tmp"
    return 1
  fi
  rm -f "$agent_tmp"
}

install_nftables_mode() {
  if [[ $LOCAL_MODE -eq 1 ]]; then
    echo "🔄 本地安装模式"
    if [[ ! -d "$SCRIPT_DIR/scripts" ]]; then
      echo "❌ 本地模式需要 scripts 目录"
      echo "   请确保 $SCRIPT_DIR/scripts 目录存在且包含二进制文件"
      exit 1
    fi
    local arch_suffix
    arch_suffix=$(get_arch_suffix)
    local missing=0
    for bin in nft_agent nft_rule_payload nft_flow_reporter; do
      if [[ ! -f "$SCRIPT_DIR/scripts/${bin}_${arch_suffix}" ]]; then
        echo "❌ 缺少: scripts/${bin}_${arch_suffix}"
        missing=1
      fi
    done
    if [[ $missing -eq 1 ]]; then
      exit 1
    fi
  fi

  echo "🚀 开始安装 nftables 转发模式..."
  get_config_params
  ensure_dependencies
  if ! install_forwarding_sysctl; then
    exit 1
  fi

  local SUDO_CMD
  SUDO_CMD=$(get_sudo_cmd)

  $SUDO_CMD mkdir -p "$INSTALL_DIR"
  echo "SERVER_ADDR=$SERVER_ADDR" | $SUDO_CMD tee "$ENV_FILE" >/dev/null
  echo "SECRET=$SECRET" | $SUDO_CMD tee -a "$ENV_FILE" >/dev/null
  echo "NFT_TABLE_NAME=$NFT_TABLE_NAME" | $SUDO_CMD tee -a "$ENV_FILE" >/dev/null

  if ! install_binary "nft_flow_reporter" "$FLOW_REPORTER_FILE"; then
    exit 1
  fi
  if ! install_binary "nft_rule_payload" "$RULE_HELPER_FILE"; then
    exit 1
  fi
  if ! write_rule_script; then
    echo "❌ 无法写入规则脚本"
    exit 1
  fi
  if ! install_binary "nft_agent" "$AGENT_FILE"; then
    exit 1
  fi
  if ! install_service; then
    echo "❌ 无法写入 systemd 服务文件"
    exit 1
  fi

  $SUDO_CMD systemctl daemon-reload
  $SUDO_CMD systemctl enable nftables >/dev/null 2>&1 || true
  $SUDO_CMD systemctl enable flux-nftables.service
  $SUDO_CMD systemctl enable flux-nftables-agent.service
  $SUDO_CMD rm -f "$AGENT_SYNC_MARKER"
  $SUDO_CMD systemctl restart flux-nftables.service
  $SUDO_CMD systemctl restart flux-nftables-agent.service

  local active_table
  if active_table="$(wait_for_verified_active_nft_table)"; then
    echo "✅ nftables 转发模式安装完成"
    echo "🔄 已从面板同步此节点当前启用的转发规则"
    echo "📌 当前活动规则表: inet $active_table"
    echo "🔍 查看规则: nft list table inet $active_table"
    echo "📁 配置目录: $INSTALL_DIR"
  else
    echo "❌ 服务启动或活动规则表验证失败，请查看日志：journalctl -u flux-nftables.service -f 或 journalctl -u flux-nftables-agent.service -f"
    exit 1
  fi
}

update_nftables_mode() {
  local SUDO_CMD
  SUDO_CMD=$(get_sudo_cmd)
  if [[ ! -f "$ENV_FILE" ]]; then
    echo "❌ 未安装 nftables 转发模式，请先安装"
    exit 1
  fi
  ensure_dependencies
  if ! install_forwarding_sysctl; then
    exit 1
  fi
  $SUDO_CMD rm -f "$AGENT_SYNC_MARKER"
  $SUDO_CMD systemctl restart flux-nftables.service
  $SUDO_CMD systemctl restart flux-nftables-agent.service
  local active_table
  if ! active_table="$(wait_for_verified_active_nft_table)"; then
    echo "❌ 规则刷新后活动规则表验证失败，请查看：journalctl -u flux-nftables.service -n 50 --no-pager" >&2
    exit 1
  fi
  echo "✅ nftables 规则已刷新"
  echo "📌 当前活动规则表: inet $active_table"
}

# 从已有 config.env 读取配置（更新模式下无需重新输入）
load_existing_config() {
  if [[ ! -f "$ENV_FILE" ]]; then
    return 1
  fi
  local key value
  while IFS='=' read -r key value; do
    case "$key" in
      SERVER_ADDR) [[ -z "$SERVER_ADDR" ]] && SERVER_ADDR="$value" ;;
      SECRET) [[ -z "$SECRET" ]] && SECRET="$value" ;;
    esac
  done < "$ENV_FILE"
  [[ -n "$SERVER_ADDR" && -n "$SECRET" ]]
}

upgrade_nftables_mode() {
  local SUDO_CMD
  SUDO_CMD=$(get_sudo_cmd)

  if [[ ! -f "$ENV_FILE" ]]; then
    echo "❌ 未安装 nftables 转发模式，请先安装"
    exit 1
  fi
  if ! load_existing_config; then
    echo "❌ 无法从 $ENV_FILE 读取 SERVER_ADDR/SECRET，请使用 -a/-s 指定后重试"
    exit 1
  fi

  echo "🚀 开始更新节点组件 (面板: $SERVER_ADDR)..."
  ensure_dependencies
  if ! install_forwarding_sysctl; then
    exit 1
  fi

  if ! install_binary "nft_flow_reporter" "$FLOW_REPORTER_FILE"; then
    exit 1
  fi
  if ! install_binary "nft_rule_payload" "$RULE_HELPER_FILE"; then
    exit 1
  fi
  if ! write_rule_script; then
    echo "❌ 无法写入规则脚本"
    exit 1
  fi
  if ! install_binary "nft_agent" "$AGENT_FILE"; then
    exit 1
  fi

  $SUDO_CMD rm -f "$AGENT_SYNC_MARKER"
  $SUDO_CMD systemctl restart flux-nftables.service
  $SUDO_CMD systemctl restart flux-nftables-agent.service

  local active_table
  if active_table="$(wait_for_verified_active_nft_table)"; then
    echo "✅ 节点组件更新完成"
    echo "🔄 已从面板重新同步此节点当前启用的转发规则"
    echo "📌 当前活动规则表: inet $active_table"
  else
    echo "❌ 服务启动或活动规则表验证失败，请查看日志：journalctl -u flux-nftables.service -f 或 journalctl -u flux-nftables-agent.service -f"
    exit 1
  fi
}

uninstall_nftables_mode() {
  local SUDO_CMD
  SUDO_CMD=$(get_sudo_cmd)
  read -p "确认卸载 nftables 转发模式吗？未确认上报的流量 journal 也会被删除。(y/N): " confirm
  if [[ "$confirm" != "y" && "$confirm" != "Y" ]]; then
    echo "❌ 取消卸载"
    exit 0
  fi

  remove_uninstall_path() {
    local path="$1"
    if [[ -e "$path" || -L "$path" ]]; then
      $SUDO_CMD rm -rf "$path"
      echo "🧹 删除: $path"
    fi
  }

  echo "🛑 停止并禁用服务..."
  $SUDO_CMD systemctl stop flux-nftables-agent.service >/dev/null 2>&1 || true
  $SUDO_CMD systemctl disable flux-nftables-agent.service >/dev/null 2>&1 || true
  $SUDO_CMD systemctl stop flux-nftables.service >/dev/null 2>&1 || true
  $SUDO_CMD systemctl disable flux-nftables.service >/dev/null 2>&1 || true

  echo "🛑 清理残留进程..."
  if command -v pkill >/dev/null 2>&1; then
    for pattern in "$AGENT_FILE" "$FLOW_REPORTER_FILE" "$RULE_HELPER_FILE" "$SCRIPT_FILE"; do
      $SUDO_CMD pkill -f "$pattern" >/dev/null 2>&1 || true
    done
  fi

  echo "🧹 清理 Flux Panel nftables 表..."
  if ! delete_managed_nft_tables; then
    echo "❌ 无法删除并确认全部 Flux Panel nftables 表；已保留持久化 journal，请修复 nft 权限后重新卸载。" >&2
    exit 1
  fi

  remove_uninstall_path "$SERVICE_FILE"
  remove_uninstall_path "$AGENT_SERVICE_FILE"
  remove_uninstall_path "/etc/systemd/system/flux-nftables.service.d"
  remove_uninstall_path "/etc/systemd/system/flux-nftables-agent.service.d"
  remove_uninstall_path "$INSTALL_DIR"
  remove_uninstall_path "$STATE_DIR"
  remove_uninstall_path "$SYSCTL_FILE"
  echo "ℹ️ 已删除 Flux Panel 的 IPv4 转发持久配置；为避免中断同机其他网络服务，当前运行时 ip_forward 值未自动关闭。"
  remove_uninstall_path "/run/flux-nftables"
  remove_uninstall_path "/var/run/flux-nftables"
  remove_uninstall_path "/var/log/flux-nftables"
  remove_uninstall_path "/var/log/flux-nftables.log"

  shopt -s nullglob
  for residual_path in /tmp/flux-nftables-* /tmp/flux_nftables_* /tmp/flux-panel-nftables-* /var/log/flux-nftables-*.log; do
    remove_uninstall_path "$residual_path"
  done
  shopt -u nullglob

  if command -v systemctl >/dev/null 2>&1; then
    $SUDO_CMD systemctl daemon-reload >/dev/null 2>&1 || true
    $SUDO_CMD systemctl reset-failed flux-nftables.service flux-nftables-agent.service >/dev/null 2>&1 || true
  fi

  if ! verify_uninstall_cleanup; then
    echo "❌ 卸载未完整完成，请根据上方残留项修复后重试。" >&2
    exit 1
  fi
  echo "✅ 卸载完成"
}

show_menu() {
  echo "==============================================="
  echo "        nftables 节点管理脚本"
  echo "==============================================="
  echo "1. 安装"
  echo "2. 刷新规则"
  echo "3. 更新组件"
  echo "4. 卸载"
  echo "5. 退出"
  echo "==============================================="
}

main() {
  if [[ $UPDATE_MODE -eq 1 ]]; then
    upgrade_nftables_mode
    exit 0
  fi

  if [[ -n "$SERVER_ADDR" && -n "$SECRET" ]]; then
    install_nftables_mode
    exit 0
  fi

  while true; do
    show_menu
    read -p "请输入选项 (1-5): " choice
    case $choice in
      1) install_nftables_mode; exit 0 ;;
      2) update_nftables_mode; exit 0 ;;
      3) upgrade_nftables_mode; exit 0 ;;
      4) uninstall_nftables_mode; exit 0 ;;
      5) exit 0 ;;
      *) echo "❌ 无效选项" ;;
    esac
  done
}

main
