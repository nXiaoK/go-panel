package service

import (
	"strings"

	"github.com/nXiaoK/go-panel/internal/model"
)

const (
	targetModeBalance = "balance"
	targetModeManual  = "manual"
)

type normalizedForwardTarget struct {
	RemoteAddr string
	Mode       string
	ActiveAddr string
}

func normalizeTargetMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case targetModeManual:
		return targetModeManual
	default:
		return targetModeBalance
	}
}

func normalizeForwardTargetConfig(mode, remoteAddr, activeAddr string, requireLiteralIP ...bool) (normalizedForwardTarget, string) {
	rawTargets := splitRemoteAddresses(remoteAddr)
	if len(rawTargets) == 0 {
		return normalizedForwardTarget{}, "请输入目标地址"
	}
	requireIP := len(requireLiteralIP) > 0 && requireLiteralIP[0]
	targets := make([]string, 0, len(rawTargets))
	for _, rawTarget := range rawTargets {
		parsed, err := ParseTargetAddress(rawTarget, requireIP)
		if err != nil {
			return normalizedForwardTarget{}, "目标地址格式错误"
		}
		targets = append(targets, parsed.Normalized)
	}

	normalized := normalizedForwardTarget{
		RemoteAddr: strings.Join(targets, ","),
		Mode:       normalizeTargetMode(mode),
	}
	if strings.TrimSpace(activeAddr) != "" {
		parsedActive, err := ParseTargetAddress(strings.TrimSpace(activeAddr), requireIP)
		if err != nil {
			return normalizedForwardTarget{}, "目标地址格式错误"
		}
		normalized.ActiveAddr = parsedActive.Normalized
	}

	if normalized.Mode != targetModeManual {
		if normalized.ActiveAddr != "" && !targetAddressExists(targets, normalized.ActiveAddr) {
			normalized.ActiveAddr = ""
		}
		return normalized, ""
	}

	if normalized.ActiveAddr == "" {
		if len(targets) == 1 {
			normalized.ActiveAddr = targets[0]
		} else {
			return normalizedForwardTarget{}, "手动目标需要选择当前目标地址"
		}
	}
	if !targetAddressExists(targets, normalized.ActiveAddr) {
		return normalizedForwardTarget{}, "当前目标地址不在目标地址列表中"
	}
	return normalized, ""
}

func effectiveForwardRemoteAddr(forward *model.Forward) string {
	if forward == nil {
		return ""
	}
	if normalizeTargetMode(forward.TargetMode) == targetModeManual {
		active := strings.TrimSpace(forward.ActiveRemoteAddr)
		if active != "" {
			return active
		}
	}
	return forward.RemoteAddr
}

func targetAddressExists(targets []string, target string) bool {
	target = strings.TrimSpace(target)
	for _, item := range targets {
		if item == target {
			return true
		}
	}
	return false
}
