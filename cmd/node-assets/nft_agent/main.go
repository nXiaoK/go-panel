package main

import (
	"encoding/json"
)

const (
	configPath       = "/etc/flux-nftables/config.env"
	applyScriptPath  = "/etc/flux-nftables/apply_rules.sh"
	flowReporterPath = "/etc/flux-nftables/nft_flow_reporter"
	// 此版本把严格的异常 dormant flag 兼容扩展到 Debian 11 nftables 0.9.8。
	version          = "nftables-go-1.3.10"
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
