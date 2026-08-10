#!/bin/bash

YES=0
DELETE_SELF=1
INSTALL_DIR="/etc/gost"
SERVICE_NAME="gost.service"
SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}"
SERVICE_OVERRIDE_DIR="/etc/systemd/system/${SERVICE_NAME}.d"

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
  [[ "$DELETE_SELF" -eq 1 ]] || return 0
  local script_path
  script_path="$(readlink -f "$0" 2>/dev/null || realpath "$0" 2>/dev/null || echo "$0")"
  [[ -f "$script_path" ]] || return 0
  rm -f "$script_path" >/dev/null 2>&1 || true
}
trap delete_self EXIT

confirm_uninstall() {
  [[ "$YES" -eq 1 ]] && return 0
  read -r -p "Uninstall GOST node and remove all Flux Panel GOST files? (y/N): " confirm
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
    "${INSTALL_DIR}/gost"
    "/usr/local/bin/gost"
    "/root/gost/gost"
    "/opt/gost/gost"
  )

  local pattern
  for pattern in "${patterns[@]}"; do
    run_cmd pkill -f "$pattern" >/dev/null 2>&1 || true
  done
}

verify_cleanup() {
  local failed=0

  if [[ -e "$SERVICE_FILE" || -e "$SERVICE_OVERRIDE_DIR" || -e "$INSTALL_DIR" ]]; then
    failed=1
  fi

  if [[ "$failed" -eq 0 ]]; then
    echo "GOST uninstall completed."
  else
    echo "GOST uninstall completed, but some files could not be removed. Please check permissions."
  fi
}

if ! confirm_uninstall; then
  echo "Uninstall cancelled."
  exit 0
fi

echo "Stopping GOST service..."
run_systemctl stop "$SERVICE_NAME" >/dev/null 2>&1 || true
run_systemctl disable "$SERVICE_NAME" >/dev/null 2>&1 || true

echo "Stopping residual GOST processes..."
kill_residual_processes

echo "Removing GOST service files and data..."
remove_path "$SERVICE_FILE"
remove_path "$SERVICE_OVERRIDE_DIR"
remove_path "$INSTALL_DIR"
remove_path "/usr/local/bin/gost"
remove_path "/root/gost"
remove_path "/opt/gost"
remove_path "/run/gost"
remove_path "/var/run/gost"
remove_path "/var/log/gost"
remove_path "/var/log/gost.log"

shopt -s nullglob
for residual_path in /tmp/gost-* /tmp/gost.* /var/log/gost-*.log /var/log/gost_*.log; do
  remove_path "$residual_path"
done
shopt -u nullglob

run_systemctl daemon-reload >/dev/null 2>&1 || true
run_systemctl reset-failed "$SERVICE_NAME" >/dev/null 2>&1 || true

verify_cleanup
