package main

import (
	"encoding/json"
)

const (
	configPath       = "/etc/flux-nftables/config.env"
	applyScriptPath  = "/etc/flux-nftables/apply_rules.sh"
	flowReporterPath = "/etc/flux-nftables/nft_flow_reporter"
	// 首次 WebSocket 全量对账成功后写入此运行时标记；安装脚本只在它与活动表标记一致时报告成功。
	agentSyncMarkerPath = "/run/flux-nftables/agent-synced"
	// 此版本用 Go 1.26.10 和 x/net v0.60.0 重建，修复网络和证书校验漏洞；与面板 latestNftNodeVersion 同步，
	// 使已有 1.3.13 节点可升级并替换整套 nft 工具，避免继续运行含已知漏洞的旧二进制。
	version          = "nftables-go-1.3.14"
	maxNodeAssetSize = int64(128 << 20)
)

type config struct {
	ServerAddr string
	Secret     string
}

type commandMessage struct {
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data"`
	RequestID string          `json:"requestId,omitempty"`
}

type commandResponse struct {
	Type      string `json:"type"`
	Success   bool   `json:"success"`
	Message   string `json:"message"`
	Data      any    `json:"data,omitempty"`
	RequestID string `json:"requestId,omitempty"`
}

type stagedNodeAsset struct {
	name   string
	target string
	tmp    string
}
