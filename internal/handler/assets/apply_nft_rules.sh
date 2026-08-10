#!/bin/bash
set -euo pipefail

unset LD_LIBRARY_PATH

CONFIG_FILE="/etc/flux-nftables/config.env"
[ -f "$CONFIG_FILE" ] || exit 1
# shellcheck disable=SC1090
source "$CONFIG_FILE"

PANEL_BASE_URL="${SERVER_ADDR%/}"
if [[ "$PANEL_BASE_URL" != http://* && "$PANEL_BASE_URL" != https://* ]]; then
  PANEL_BASE_URL="http://${PANEL_BASE_URL}"
fi
RULE_URL="${PANEL_BASE_URL}/api/v1/node/nft-config?secret=${SECRET}"
CURL_INITIAL_PROTOCOLS="=https"
[[ "$RULE_URL" == http://* ]] && CURL_INITIAL_PROTOCOLS="=http,https"

TMP_RULES=$(mktemp)
TMP_JSON=$(mktemp)
cleanup() {
  rm -f "$TMP_RULES" "$TMP_JSON"
}
trap cleanup EXIT

curl --proto "$CURL_INITIAL_PROTOCOLS" --proto-redir '=https' -fsSL "$RULE_URL" -o "$TMP_JSON"
/etc/flux-nftables/nft_rule_payload "$TMP_JSON" "$TMP_RULES"

exec /etc/flux-nftables/nft_flow_reporter \
  --refresh "$TMP_RULES" "$SERVER_ADDR" "$SECRET" "$NFT_TABLE_NAME"
