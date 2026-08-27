package service

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/model"
	subscriptionassets "github.com/nXiaoK/go-panel/subscription-assets"
)

const (
	subAPIKeyConfigName          = "subscription_api_key"
	subDefaultBackfillKey        = "subscription_default_backfilled"
	subV2rayNDefaultBackfillKey  = "subscription_v2rayn_default_backfilled"
	subSingboxDefaultBackfillKey = "subscription_singbox_default_backfilled"
	// Snell v3+ 已被 Mihomo 与 Shadowrocket 支持；该迁移键只执行一次兼容订阅回填。
	subSnellClashBackfillKey = "subscription_snell_clash_udp_backfilled"
	defaultSubFormat         = "surge"
	proxyRelayModeReplace    = "replace"
	proxyRelayModeAppend     = "append"
	defaultSurgeSubName      = "Surge 默认订阅"
	defaultClashSubName      = "Clash 默认订阅"
	defaultV2rayNSubName     = "v2rayN 默认订阅"
	defaultSingboxSubName    = "sing-box 默认订阅"
	defaultSurgeSubDesc      = "Snell 节点默认自动加入此配置"
	defaultClashSubDesc      = "Clash / Shadowrocket 兼容节点（含 Snell v3+）默认自动加入此配置"
	defaultV2rayNSubDesc     = "非 Snell 节点默认自动加入此配置"
	defaultSingboxSubDesc    = "非 Snell 节点默认自动加入此配置"
	vlessServerScriptName    = "vless-server.sh"
)

const fallbackSurgeTemplate = `[General]
skip-proxy = 127.0.0.1, localhost, *.local
ipv6 = false

[Proxy]
# {{PROXIES}}

[Proxy Group]
Proxy = select, policy-regex-filter=.*, include-all-proxies=1
FINAL = select, DIRECT, Proxy

[Rule]
FINAL,FINAL,dns-failed
`

const fallbackClashTemplate = `mixed-port: 7890
allow-lan: false
mode: rule
log-level: info
proxies:
  # {{PROXIES}}
proxy-groups:
  - name: Proxy
    type: select
    proxies:
      - DIRECT
rules:
  - MATCH,Proxy
`

// fallbackSingboxTemplate 是内嵌模板不可用时的安全回退配置。
// 严格 JSON 不允许内联注释，各模块的中文说明见 subscription-assets/README.md。
const fallbackSingboxTemplate = `{
  "log": { "level": "info", "timestamp": true },
  "dns": {
    "servers": [
      { "type": "local", "tag": "dns-local" },
      {
        "type": "https",
        "tag": "dns-proxy",
        "server": "1.1.1.1",
        "tls": { "enabled": true, "server_name": "cloudflare-dns.com" },
        "detour": "proxy"
      }
    ],
    "rules": [
      { "domain_suffix": [".lan", ".local", ".cn"], "action": "route", "server": "dns-local" }
    ],
    "final": "dns-proxy",
    "strategy": "ipv4_only",
    "reverse_mapping": true
  },
  "inbounds": [
    {
      "type": "tun",
      "tag": "tun-in",
      "address": ["172.19.0.1/30", "fdfe:dcba:9876::1/126"],
      "mtu": 1500,
      "auto_route": true,
      "strict_route": true,
      "stack": "mixed"
    }
  ],
  "outbounds": [
    { "type": "selector", "tag": "proxy", "outbounds": ["direct"], "default": "direct" },
    { "type": "direct", "tag": "direct", "domain_resolver": "dns-local" }
  ],
  "route": {
    "rules": [
      { "action": "sniff" },
      { "port": 53, "action": "hijack-dns" },
      { "protocol": "dns", "action": "hijack-dns" },
      { "ip_version": 6, "action": "reject" },
      { "ip_is_private": true, "action": "route", "outbound": "direct" },
      { "domain_suffix": ".cn", "action": "route", "outbound": "direct" }
    ],
    "final": "proxy"
  },
  "experimental": { "cache_file": { "enabled": true, "store_rdrc": true } }
}`

const fallbackVlessServerBootstrap = `#!/bin/bash
set -euo pipefail

CFG="/etc/vless-reality"
SUB_PANEL_FILE="$CFG/sub-panel.env"

quote_value() {
    printf "'%s'" "$(printf '%s' "$1" | sed "s/'/'\"'\"'/g")"
}

save_flux_panel_config() {
    local panel_url="$1" api_key="$2" server_ip="${3:-}"
    mkdir -p "$CFG"
    umask 077
    {
        echo "SUB_PANEL_URL=$(quote_value "$panel_url")"
        echo "SUB_PANEL_API_KEY=$(quote_value "$api_key")"
        if [[ -n "$server_ip" ]]; then
            echo "SUB_PANEL_SERVER=$(quote_value "$server_ip")"
        fi
    } > "$SUB_PANEL_FILE"
    echo "Flux Panel 上报配置已保存: $SUB_PANEL_FILE"
}

try_full_script_report() {
    local panel_url="$1" api_key="$2" server_ip="${3:-}"
    local self_path full_script
    self_path="$(readlink -f "$0" 2>/dev/null || realpath "$0" 2>/dev/null || printf '%s' "$0")"
    for full_script in /usr/local/bin/vless-server.sh /usr/local/bin/vless /root/vless-server.sh; do
        if [[ -x "$full_script" && "$full_script" != "$self_path" ]]; then
            "$full_script" --flux-panel-bind "$panel_url" "$api_key" "$server_ip" || true
            return 0
        fi
    done
    return 1
}

case "${1:-}" in
    --flux-panel-bind)
        panel_url="${2:-}"
        api_key="${3:-}"
        server_ip="${4:-}"
        if [[ -z "$panel_url" || -z "$api_key" ]]; then
            echo "用法: $0 --flux-panel-bind <panel_url> <api_key> [server_ip]" >&2
            exit 1
        fi
        save_flux_panel_config "$panel_url" "$api_key" "$server_ip"
        try_full_script_report "$panel_url" "$api_key" "$server_ip" || {
            echo "未找到完整 vless-server.sh，仅完成 Flux Panel 配置绑定。"
            echo "安装/管理协议后，完整脚本会按此配置自动上报。"
        }
        ;;
    *)
        echo "这是 Flux Panel 订阅绑定 bootstrap。"
        echo "用法: $0 --flux-panel-bind <panel_url> <api_key> [server_ip]"
        ;;
esac
`

type renderNode struct {
	model.ProxyNode
	Address string
	Port    int
}

type proxyNodeView struct {
	model.ProxyNode
	ResolvedServer    string  `json:"resolvedServer"`
	ResolvedPort      int     `json:"resolvedPort"`
	ResolvedAddress   string  `json:"resolvedAddress"`
	ProfileIDs        []int64 `json:"profileIds"`
	Provider          string  `json:"provider,omitempty"`
	Region            string  `json:"region,omitempty"`
	ProtocolLabel     string  `json:"protocolLabel,omitempty"`
	RelayMode         string  `json:"relayMode,omitempty"`
	SourceNodeName    string  `json:"sourceNodeName,omitempty"`
	RelayChildCount   int     `json:"relayChildCount"`
	ForwardName       string  `json:"forwardName,omitempty"`
	ForwardTunnelID   int64   `json:"forwardTunnelId,omitempty"`
	ForwardTunnel     string  `json:"forwardTunnel,omitempty"`
	ForwardTunnelType int     `json:"forwardTunnelType,omitempty"`
	ForwardInIP       string  `json:"forwardInIp,omitempty"`
	ForwardInPort     int     `json:"forwardInPort,omitempty"`
	ForwardOutIP      string  `json:"forwardOutIp,omitempty"`
	ForwardOutPort    int     `json:"forwardOutPort,omitempty"`
	ForwardTarget     string  `json:"forwardTarget,omitempty"`
}

type proxyRelayEndpointView struct {
	NodeID   int64  `json:"nodeId,omitempty"`
	NodeName string `json:"nodeName,omitempty"`
	IP       string `json:"ip,omitempty"`
	Port     int    `json:"port,omitempty"`
	PortText string `json:"portText,omitempty"`
	Address  string `json:"address,omitempty"`
}

type proxyRelayPreviewView struct {
	NodeID              int64                   `json:"nodeId"`
	NodeName            string                  `json:"nodeName"`
	RelayMode           string                  `json:"relayMode,omitempty"`
	TunnelID            int64                   `json:"tunnelId"`
	TunnelName          string                  `json:"tunnelName"`
	TunnelType          int                     `json:"tunnelType"`
	TunnelTypeName      string                  `json:"tunnelTypeName"`
	Protocol            string                  `json:"protocol,omitempty"`
	Entry               proxyRelayEndpointView  `json:"entry"`
	Relay               *proxyRelayEndpointView `json:"relay,omitempty"`
	Exit                proxyRelayEndpointView  `json:"exit"`
	Target              proxyRelayEndpointView  `json:"target"`
	SubscriptionAddress string                  `json:"subscriptionAddress,omitempty"`
	ForwardID           int64                   `json:"forwardId,omitempty"`
	ForwardName         string                  `json:"forwardName,omitempty"`
}

type subscriptionTunnelView struct {
	ID       int64   `json:"id"`
	Name     string  `json:"name"`
	InIP     string  `json:"inIp"`
	Type     int     `json:"type"`
	Protocol *string `json:"protocol"`
	Status   int     `json:"status"`
}

var createForwardForProxyNodeRelay = createForwardInternal

// EnsureSubscriptionDefaults 写入订阅系统默认配置。
func EnsureSubscriptionDefaults() {
	if GetConfigValue(subAPIKeyConfigName) == "" {
		updateOrCreateConfig(subAPIKeyConfigName, randomToken(32))
	}

	ensureDefaultSubscriptionProfile(defaultSurgeSubName, "surge", defaultSurgeSubDesc)
	ensureDefaultSubscriptionProfile(defaultClashSubName, "clash", defaultClashSubDesc)
	ensureDefaultSubscriptionProfile(defaultV2rayNSubName, "v2rayn", defaultV2rayNSubDesc)
	ensureDefaultSubscriptionProfile(defaultSingboxSubName, "singbox", defaultSingboxSubDesc)
	repairInvalidClashTemplates()
	backfillEmptySingboxTemplates()
	if GetConfigValue(subDefaultBackfillKey) == "" {
		backfillDefaultProfileNodes()
		updateOrCreateConfig(subDefaultBackfillKey, "1")
	}
	if GetConfigValue(subV2rayNDefaultBackfillKey) == "" {
		backfillDefaultProfileNodes()
		updateOrCreateConfig(subV2rayNDefaultBackfillKey, "1")
	}
	if GetConfigValue(subSingboxDefaultBackfillKey) == "" {
		backfillDefaultProfileNodes()
		updateOrCreateConfig(subSingboxDefaultBackfillKey, "1")
	}
	if GetConfigValue(subSnellClashBackfillKey) == "" {
		// 历史版本把 Snell 排除在 Clash 外；重新执行幂等绑定即可补入现有默认订阅。
		backfillDefaultProfileNodes()
		updateOrCreateConfig(subSnellClashBackfillKey, "1")
	}
}

func ensureDefaultSubscriptionProfile(name, format, description string) {
	var profile model.SubscriptionProfile
	err := model.DB.Where("name = ?", name).First(&profile).Error
	if err == nil {
		updates := map[string]interface{}{}
		if normalizeFormat(profile.DefaultFormat) == "" {
			updates["default_format"] = format
		}
		if strings.TrimSpace(profile.SurgeTemplate) == "" {
			updates["surge_template"] = loadTemplateFile("surge.config", fallbackSurgeTemplate)
		}
		if strings.TrimSpace(profile.ClashTemplate) == "" || isInvalidClashTemplate(profile.ClashTemplate) {
			updates["clash_template"] = defaultClashTemplate()
		}
		if strings.TrimSpace(profile.SingboxTemplate) == "" {
			updates["singbox_template"] = defaultSingboxTemplate()
		}
		if len(updates) > 0 {
			updates["updated_time"] = time.Now().UnixMilli()
			model.DB.Model(&model.SubscriptionProfile{}).Where("id = ?", profile.ID).Updates(updates)
		}
		return
	}
	if err != gorm.ErrRecordNotFound {
		fmt.Printf("查询默认订阅失败: %v\n", err)
		return
	}
	now := time.Now().UnixMilli()
	profile = model.SubscriptionProfile{
		Name:            name,
		Token:           randomToken(24),
		DefaultFormat:   format,
		Description:     description,
		SurgeTemplate:   loadTemplateFile("surge.config", fallbackSurgeTemplate),
		ClashTemplate:   defaultClashTemplate(),
		SingboxTemplate: defaultSingboxTemplate(),
		Status:          1,
		CreatedTime:     now,
		UpdatedTime:     now,
	}
	if err := model.DB.Create(&profile).Error; err != nil {
		fmt.Printf("初始化默认订阅失败: %v\n", err)
	}
}

func backfillDefaultProfileNodes() {
	var nodes []model.ProxyNode
	model.DB.Where("status = ?", 1).Find(&nodes)
	for _, node := range nodes {
		attachNodeToDefaultProfilesByProtocol(node.ID, node.Protocol)
	}
}

func defaultClashTemplate() string {
	return loadTemplateFile("clash.yml", fallbackClashTemplate)
}

func defaultSingboxTemplate() string {
	return loadTemplateFile("sing-box-android.json", fallbackSingboxTemplate)
}

func backfillEmptySingboxTemplates() {
	now := time.Now().UnixMilli()
	model.DB.Model(&model.SubscriptionProfile{}).
		Where("singbox_template IS NULL OR trim(singbox_template) = ''").
		Updates(map[string]interface{}{
			"singbox_template": defaultSingboxTemplate(),
			"updated_time":     now,
		})
}

func repairInvalidClashTemplates() {
	var profiles []model.SubscriptionProfile
	model.DB.Find(&profiles)
	var template string
	now := time.Now().UnixMilli()
	for _, profile := range profiles {
		if !isInvalidClashTemplate(profile.ClashTemplate) {
			continue
		}
		if template == "" {
			template = defaultClashTemplate()
		}
		model.DB.Model(&model.SubscriptionProfile{}).Where("id = ?", profile.ID).Updates(map[string]interface{}{
			"clash_template": template,
			"updated_time":   now,
		})
	}
}

func isInvalidClashTemplate(template string) bool {
	trimmed := strings.TrimSpace(template)
	if trimmed == "" {
		return true
	}
	if strings.Contains(trimmed, "[Proxy]") || strings.Contains(trimmed, "[Proxy Group]") || strings.Contains(trimmed, "#!MANAGED-CONFIG") {
		return true
	}
	var root map[string]interface{}
	if err := yaml.Unmarshal([]byte(template), &root); err != nil {
		return true
	}
	if root == nil {
		return true
	}
	_, hasProxies := root["proxies"]
	_, hasGroups := root["proxy-groups"]
	_, hasRules := root["rules"]
	return !hasProxies || !hasGroups || !hasRules
}

func loadTemplateFile(name, fallback string) string {
	// Embedded assets are authoritative: tests and production binaries must not
	// depend on process cwd or accidental sibling clash.yml files outside the repo.
	if b, err := subscriptionassets.Files.ReadFile(name); err == nil && len(strings.TrimSpace(string(b))) > 0 {
		return string(b)
	}
	candidates := []string{
		filepath.Join("subscription-assets", name),
		filepath.Join("..", "subscription-assets", name),
		filepath.Join("..", "..", "subscription-assets", name),
		filepath.Join(".", name),
	}
	if wd, err := os.Getwd(); err == nil {
		for dir, depth := wd, 0; depth < 8; depth++ {
			candidates = append(candidates,
				filepath.Join(dir, "subscription-assets", name),
				filepath.Join(dir, name),
			)
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	for _, p := range candidates {
		if b, err := os.ReadFile(p); err == nil && len(strings.TrimSpace(string(b))) > 0 {
			return string(b)
		}
	}
	return fallback
}

func randomToken(bytesLen int) string {
	b := make([]byte, bytesLen)
	if _, err := rand.Read(b); err != nil {
		return strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	return hex.EncodeToString(b)
}

func normalizeProtocol(protocol string) string {
	p := strings.ToLower(strings.TrimSpace(protocol))
	switch p {
	case "shadowsocks":
		return "ss"
	case "ss2022", "ss-legacy":
		return "ss"
	case "socks":
		return "socks5"
	case "snell-v5":
		return "snell"
	case "vless-vision", "vless-reality", "vless-ws", "vless-xhttp":
		return "vless"
	case "vmess-ws":
		return "vmess"
	default:
		return p
	}
}

func normalizeFormat(format string) string {
	f := strings.ToLower(strings.TrimSpace(format))
	switch f {
	case "yaml", "yml":
		return "clash"
	case "v2ray", "v2rayn", "v2rayng", "v2ray-n":
		return "v2rayn"
	case "sing-box", "sing_box", "sfa":
		return "singbox"
	case "surge", "clash", "singbox":
		return f
	default:
		return ""
	}
}

func trimJSON(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return "{}"
	}
	var v interface{}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return "{}"
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func optionsMap(raw string) map[string]interface{} {
	out := map[string]interface{}{}
	if strings.TrimSpace(raw) == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

func stringOption(opts map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if v, ok := opts[key]; ok {
			switch x := v.(type) {
			case string:
				if x != "" {
					return x
				}
			case float64:
				if x != 0 {
					return strconv.Itoa(int(x))
				}
			case int:
				if x != 0 {
					return strconv.Itoa(x)
				}
			case bool:
				return strconv.FormatBool(x)
			}
		}
	}
	return ""
}

func boolOption(opts map[string]interface{}, key string, def bool) bool {
	v, ok := opts[key]
	if !ok {
		return def
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x == "true" || x == "1" || x == "yes"
	case float64:
		return x != 0
	}
	return def
}

func intOption(opts map[string]interface{}, key string, def int) int {
	v, ok := opts[key]
	if !ok {
		return def
	}
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case string:
		i, err := strconv.Atoi(x)
		if err == nil {
			return i
		}
	}
	return def
}

func defaultNodeName(req dto.ProxyNodeReportDto) string {
	if name := subscriptionReportedNodeName(req); name != "" {
		return name
	}
	if strings.TrimSpace(req.Protocol) != "" {
		return strings.ToUpper(req.Protocol) + "-" + strings.TrimSpace(req.Server)
	}
	return "proxy-" + strings.TrimSpace(req.Server)
}

func subscriptionReportedNodeName(req dto.ProxyNodeReportDto) string {
	opts := optionsMap(req.Options)
	provider := normalizeNodeNamePart(stringOption(opts, "provider", "serviceProvider", "cloudProvider", "isp"))
	region := normalizeRegionCode(firstNonEmpty(stringOption(opts, "region", "country", "countryCode"), regionFromReportedName(req.Name)))
	protocol := normalizeProtocolLabel(firstNonEmpty(stringOption(opts, "protocolLabel"), protocolFromReportedName(req.Name), req.Protocol, stringOption(opts, "originalProtocol")))
	if provider == "" {
		provider = normalizeNodeNamePart(providerFromReportedName(req.Name))
	}
	if provider != "" && region != "" && protocol != "" {
		return provider + "-" + region + "-" + protocol
	}
	return strings.TrimSpace(req.Name)
}

func proxyNodeClassification(node model.ProxyNode) (string, string, string) {
	opts := optionsMap(node.Options)
	provider := normalizeNodeNamePart(firstNonEmpty(
		stringOption(opts, "provider", "serviceProvider", "cloudProvider", "isp"),
		providerFromReportedName(node.Name),
	))
	region := normalizeRegionCode(firstNonEmpty(
		stringOption(opts, "region", "country", "countryCode"),
		regionFromReportedName(node.Name),
	))
	protocolLabel := normalizeProtocolLabel(firstNonEmpty(
		stringOption(opts, "protocolLabel"),
		node.Protocol,
		stringOption(opts, "originalProtocol"),
		protocolFromReportedName(node.Name),
	))
	return provider, region, protocolLabel
}

func normalizeNodeNamePart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	switch {
	case strings.Contains(lower, "greencloud"):
		return "GreenCloud"
	case strings.Contains(lower, "alibaba") || strings.Contains(lower, "aliyun") || strings.Contains(lower, "ali cloud"):
		return "Aliyun"
	case strings.Contains(lower, "tencent"):
		return "TencentCloud"
	case strings.Contains(lower, "amazon") || strings.Contains(lower, "aws"):
		return "AWS"
	case strings.Contains(lower, "google"):
		return "GoogleCloud"
	case strings.Contains(lower, "oracle"):
		return "OracleCloud"
	case strings.Contains(lower, "microsoft") || strings.Contains(lower, "azure"):
		return "Azure"
	case strings.Contains(lower, "digitalocean"):
		return "DigitalOcean"
	case strings.Contains(lower, "vultr") || strings.Contains(lower, "constant company"):
		return "Vultr"
	case strings.Contains(lower, "linode") || strings.Contains(lower, "akamai"):
		return "Linode"
	case strings.Contains(lower, "hetzner"):
		return "Hetzner"
	case strings.Contains(lower, "cloudflare"):
		return "Cloudflare"
	}
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'))
	})
	out := strings.Join(parts, "")
	if out == "" {
		return ""
	}
	return out
}

func normalizeRegionCode(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	if len(value) < 2 {
		return ""
	}
	var b strings.Builder
	for _, r := range value {
		if r >= 'A' && r <= 'Z' {
			b.WriteRune(r)
		}
		if b.Len() == 2 {
			break
		}
	}
	return b.String()
}

func normalizeProtocolLabel(value string) string {
	switch normalizeProtocol(value) {
	case "ss":
		return "SS"
	case "snell":
		return "Snell"
	case "vless":
		return "VLESS"
	case "vmess":
		return "VMess"
	case "trojan":
		return "Trojan"
	case "socks5":
		return "SOCKS5"
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	switch {
	case strings.Contains(lower, "shadow"):
		return "SS"
	case strings.Contains(lower, "snell"):
		return "Snell"
	case strings.Contains(lower, "vless"):
		return "VLESS"
	case strings.Contains(lower, "vmess"):
		return "VMess"
	case strings.Contains(lower, "trojan"):
		return "Trojan"
	case strings.Contains(lower, "socks"):
		return "SOCKS5"
	}
	return normalizeNodeNamePart(value)
}

func providerFromReportedName(name string) string {
	parts := strings.Split(strings.TrimSpace(name), "-")
	if len(parts) >= 3 && normalizeRegionCode(parts[1]) != "" {
		return parts[0]
	}
	return ""
}

func regionFromReportedName(name string) string {
	parts := strings.Split(strings.TrimSpace(name), "-")
	if len(parts) >= 3 {
		return parts[1]
	}
	if len(parts) >= 2 {
		if isRegionToken(parts[0]) {
			return parts[0]
		}
		if isRegionToken(parts[1]) {
			return parts[1]
		}
	}
	return ""
}

func isRegionToken(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 2 {
		return false
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			return false
		}
	}
	return true
}

func protocolFromReportedName(name string) string {
	parts := strings.Split(strings.TrimSpace(name), "-")
	if len(parts) >= 3 {
		return strings.Join(parts[2:], "-")
	}
	if len(parts) >= 2 {
		return strings.Join(parts[1:], "-")
	}
	return ""
}

func externalNodeID(req dto.ProxyNodeReportDto) string {
	if strings.TrimSpace(req.ExternalID) != "" {
		return strings.TrimSpace(req.ExternalID)
	}
	sourceHost := strings.TrimSpace(req.SourceHost)
	if sourceHost != "" && strings.TrimSpace(req.Protocol) != "" {
		return sourceHost + "-" + strings.TrimSpace(req.Protocol)
	}
	key := fmt.Sprintf("%s|%s|%d|%s|%s", normalizeProtocol(req.Protocol), req.Server, req.Port, req.UUID, req.Password)
	return strings.ReplaceAll(uuid.NewSHA1(uuid.NameSpaceURL, []byte(key)).String(), "-", "")
}

func legacyExternalNodeIDs(req dto.ProxyNodeReportDto) []string {
	candidates := []string{
		strings.TrimSpace(req.LegacyExternalID),
	}
	if host := strings.TrimSpace(req.LegacySourceHost); host != "" && strings.TrimSpace(req.Protocol) != "" {
		candidates = append(candidates, host+"-"+strings.TrimSpace(req.Protocol))
	}
	return uniqueStrings(candidates)
}

func applyNodeReport(node *model.ProxyNode, req dto.ProxyNodeReportDto, now int64) {
	opts := trimJSON(req.Options)
	node.ExternalID = externalNodeID(req)
	node.Name = defaultNodeName(req)
	node.Protocol = normalizeProtocol(req.Protocol)
	node.Server = strings.TrimSpace(req.Server)
	node.Port = req.Port
	node.UUID = strings.TrimSpace(req.UUID)
	node.Username = strings.TrimSpace(req.Username)
	node.Password = strings.TrimSpace(req.Password)
	node.Method = strings.TrimSpace(req.Method)
	node.SNI = strings.TrimSpace(req.SNI)
	node.Network = strings.TrimSpace(req.Network)
	node.Security = strings.TrimSpace(req.Security)
	node.Path = strings.TrimSpace(req.Path)
	node.Flow = strings.TrimSpace(req.Flow)
	node.PublicKey = strings.TrimSpace(req.PublicKey)
	node.ShortID = strings.TrimSpace(req.ShortID)
	node.Fingerprint = strings.TrimSpace(req.Fingerprint)
	node.SnellVersion = req.SnellVersion
	node.AllowInsecure = boolToInt(req.AllowInsecure)
	node.UDP = 1
	if req.UDP != nil {
		node.UDP = boolToInt(*req.UDP)
	}
	node.Link = strings.TrimSpace(req.Link)
	node.Options = opts
	node.LastReportTime = now
	node.UpdatedTime = now
	if node.CreatedTime == 0 {
		node.CreatedTime = now
	}
	if req.Status == nil {
		if node.ID == 0 {
			node.Status = 1
		}
	} else {
		node.Status = *req.Status
	}
	if req.Sort != nil {
		node.Sort = *req.Sort
	}
	if req.ForwardID != nil {
		node.ForwardID = req.ForwardID
	}
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// isValidSubscriptionAPIKey 常量时间比较 API Key，避免时序侧信道泄露。
// 空 key 直接返回 false；长度不同时用真实 key 做一次比较以均衡耗时。
func isValidSubscriptionAPIKey(apiKey string) bool {
	apiKey = strings.TrimSpace(apiKey)
	expected := GetConfigValue(subAPIKeyConfigName)
	if apiKey == "" || expected == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(apiKey), []byte(expected)) == 1
}

// ReportProxyNode upsert 节点上报。
