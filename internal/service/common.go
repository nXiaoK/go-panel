package service

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/nXiaoK/go-panel/internal/model"
)

// 共享常量与工具

const (
	tunnelTypePortForward   = 1
	tunnelTypeTunnelForward = 2
	forwardModeGost         = "gost"
	forwardModeNftables     = "nftables"
)

// getUserTunnel 查询用户隧道权限，无则返回 nil
func getUserTunnel(userID, tunnelID int64) *model.UserTunnel {
	var ut model.UserTunnel
	if err := model.DB.Where("user_id = ? AND tunnel_id = ?", userID, tunnelID).First(&ut).Error; err != nil {
		return nil
	}
	return &ut
}

// normalizeForwardMode 规范化转发模式，默认 gost
func normalizeForwardMode(mode string) string {
	if strings.EqualFold(strings.TrimSpace(mode), forwardModeNftables) {
		return forwardModeNftables
	}
	return forwardModeGost
}

// isNftablesMode 节点是否为 nftables 模式
func isNftablesMode(node *model.Node) bool {
	return node != nil && normalizeForwardMode(node.ForwardMode) == forwardModeNftables
}

// buildServiceName 构建 gost 服务名 forwardId_userId_userTunnelId
func buildServiceName(forwardID, userID int64, ut *model.UserTunnel) string {
	utID := int64(0)
	if ut != nil {
		utID = ut.ID
	}
	return formatServiceName(forwardID, userID, utID)
}

func formatServiceName(forwardID, userID, userTunnelID int64) string {
	return strconv.FormatInt(forwardID, 10) + "_" + strconv.FormatInt(userID, 10) + "_" + strconv.FormatInt(userTunnelID, 10)
}

// extractHost 从 host:port / [v6]:port 提取主机
func extractHost(address string) string {
	v := strings.TrimSpace(address)
	if v == "" {
		return ""
	}
	if strings.HasPrefix(v, "[") {
		if end := strings.Index(v, "]"); end > 1 {
			return v[1:end]
		}
		return ""
	}
	if idx := strings.LastIndex(v, ":"); idx > 0 {
		return v[:idx]
	}
	return v
}

// extractPort 从 host:port / [v6]:port 提取端口，失败返回 -1
func extractPort(address string) int {
	v := strings.TrimSpace(address)
	if v == "" {
		return -1
	}
	if strings.HasPrefix(v, "[") {
		end := strings.Index(v, "]")
		// 要求 ] 后紧跟 : 且端口串至少 1 字符
		if end > 1 && end+1 < len(v) && v[end+1] == ':' {
			return parsePortStr(v[end+2:])
		}
		return -1
	}
	idx := strings.LastIndex(v, ":")
	if idx <= 0 || idx+1 >= len(v) {
		return -1
	}
	return parsePortStr(v[idx+1:])
}

func joinHostPort(host string, port int) string {
	host = strings.TrimSpace(host)
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		return fmt.Sprintf("%s:%d", host, port)
	}
	if strings.Contains(host, ":") {
		return fmt.Sprintf("[%s]:%d", host, port)
	}
	return fmt.Sprintf("%s:%d", host, port)
}

func parsePortStr(s string) int {
	p, err := strconv.Atoi(s)
	if err != nil || p < 0 || p > 65535 {
		return -1
	}
	return p
}

// splitRemoteAddresses 拆分逗号分隔的目标地址
func splitRemoteAddresses(remoteAddr string) []string {
	var res []string
	for _, part := range strings.Split(remoteAddr, ",") {
		if t := strings.TrimSpace(part); t != "" {
			res = append(res, t)
		}
	}
	return res
}
