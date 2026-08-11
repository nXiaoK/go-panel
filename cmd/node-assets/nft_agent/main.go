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
	// 此版本增加首次对账同步标记，并由安装器持久启用 IPv4 内核转发。
	version          = "nftables-go-1.3.11"
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
