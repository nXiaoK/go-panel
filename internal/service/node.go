package service

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/gost"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/result"
	"github.com/nXiaoK/go-panel/internal/ws"
)

const (
	// GOST 1.2.6 起，Agent 在升级时会忽略命令中的可疑地址并优先使用本机持久配置。
	localPanelBaseURLGostMinVersion = "1.2.6"
	// nftables-go 1.3.4 起，Agent 在升级时会忽略命令中的可疑地址并优先使用本机持久配置。
	localPanelBaseURLNftMinVersion = "nftables-go-1.3.4"
	// GOST 1.2.6 会上报本机持久化的面板入口，并在升级时优先复用该历史地址。
	latestGostNodeVersion = "1.2.6"
	// nftables-go 1.3.11 增加首次全量对账同步标记，并由安装器持久启用 IPv4 内核转发。
	latestNftNodeVersion = "nftables-go-1.3.11"
)

var versionNumberPattern = regexp.MustCompile(`\d+`)

type NodeRuntimeConfig struct {
	AllowInsecureDownloads bool
}

var nodeRuntimeConfig NodeRuntimeConfig
var nodeUpgradeSender = gost.UpgradeNode

func ConfigureNodeRuntime(cfg NodeRuntimeConfig) {
	nodeRuntimeConfig = cfg
}

// CreateNode 创建节点（自动生成 secret）
func CreateNode(req dto.NodeDto) result.R {
	if req.PortEnd < req.PortSta {
		return result.Err("结束端口不能小于起始端口")
	}
	mode := normalizeForwardMode(req.ForwardMode)
	serverTarget, err := parseTargetHostPort(req.ServerIP, 1, mode == forwardModeNftables)
	if err != nil {
		return result.Err("节点服务地址格式错误")
	}
	now := time.Now().UnixMilli()
	node := model.Node{
		Name:        req.Name,
		Secret:      strings.ReplaceAll(uuid.NewString(), "-", ""),
		IP:          req.IP,
		ServerIP:    serverTarget.Host,
		PortSta:     req.PortSta,
		PortEnd:     req.PortEnd,
		ForwardMode: mode,
		CreatedTime: now,
		UpdatedTime: &now,
		Status:      0,
	}
	if req.HTTP != nil {
		node.HTTP = *req.HTTP
	}
	if req.TLS != nil {
		node.TLS = *req.TLS
	}
	if req.Socks != nil {
		node.Socks = *req.Socks
	}
	if err := model.DB.Create(&node).Error; err != nil {
		return result.Err("节点创建失败")
	}
	return result.OkMsg("节点创建成功")
}

// GetAllNodes 节点列表（隐藏 secret）
func GetAllNodes() result.R {
	var nodes []model.Node
	model.DB.Find(&nodes)
	for i := range nodes {
		nodes[i].Secret = ""
		enrichNodeVersionInfo(&nodes[i])
	}
	return result.Ok(nodes)
}

func enrichNodeVersionInfo(node *model.Node) {
	if node == nil {
		return
	}
	latest := latestNodeVersion(node.ForwardMode)
	node.LatestVersion = latest
	node.UpgradeAvailable = isVersionNewer(latest, stringValue(node.Version))
}

func latestNodeVersion(mode string) string {
	if normalizeForwardMode(mode) == forwardModeNftables {
		return latestNftNodeVersion
	}
	return latestGostNodeVersion
}

func localPanelBaseURLMinVersion(mode string) string {
	if normalizeForwardMode(mode) == forwardModeNftables {
		return localPanelBaseURLNftMinVersion
	}
	return localPanelBaseURLGostMinVersion
}

// nodeSupportsLocalPanelBaseURL 只信任已具备“本机地址优先”保护的 Agent 使用持久历史地址。
// 旧版或未知版本仍使用系统 ip，防止泄露的节点 secret 把历史 URL 污染转换为节点程序替换权限。
func nodeSupportsLocalPanelBaseURL(node model.Node) bool {
	current := strings.TrimSpace(stringValue(node.Version))
	if len(versionParts(current)) == 0 {
		return false
	}
	return !isVersionNewer(localPanelBaseURLMinVersion(node.ForwardMode), current)
}

func isVersionNewer(latest, current string) bool {
	latestParts := versionParts(latest)
	currentParts := versionParts(current)
	if len(latestParts) == 0 || len(currentParts) == 0 {
		return false
	}
	maxLen := len(latestParts)
	if len(currentParts) > maxLen {
		maxLen = len(currentParts)
	}
	for i := 0; i < maxLen; i++ {
		lv, cv := 0, 0
		if i < len(latestParts) {
			lv = latestParts[i]
		}
		if i < len(currentParts) {
			cv = currentParts[i]
		}
		if lv != cv {
			return lv > cv
		}
	}
	return false
}

func versionParts(version string) []int {
	matches := versionNumberPattern.FindAllString(version, -1)
	parts := make([]int, 0, len(matches))
	for _, match := range matches {
		v, err := strconv.Atoi(match)
		if err != nil {
			continue
		}
		parts = append(parts, v)
	}
	return parts
}

// UpdateNode 更新节点；在线且协议开关变化时下发 SetProtocol，并同步隧道入/出口 IP
func UpdateNode(req dto.NodeUpdateDto) result.R {
	if req.PortEnd < req.PortSta {
		return result.Err("结束端口不能小于起始端口")
	}
	mode := normalizeForwardMode(req.ForwardMode)
	serverTarget, err := parseTargetHostPort(req.ServerIP, 1, mode == forwardModeNftables)
	if err != nil {
		return result.Err("节点服务地址格式错误")
	}

	// UpdateNode participates in the same per-node saga as forward mutations.
	// Read the node and decide whether a mode transition is safe only after the
	// lock is acquired, otherwise a concurrent forward can make that snapshot
	// stale before the node/tunnel writes below.
	unlock := lockNftSagaNodes([]int64{req.ID})
	var node model.Node
	if err := model.DB.First(&node, req.ID).Error; err != nil {
		unlock()
		return result.Err("节点不存在")
	}
	if normalizeForwardMode(node.ForwardMode) != mode {
		var relayTunnelCount int64
		if err := model.DB.Model(&model.Tunnel{}).
			Where("relay_node_id IS NOT NULL AND (in_node_id = ? OR relay_node_id = ? OR out_node_id = ?)", req.ID, req.ID, req.ID).
			Count(&relayTunnelCount).Error; err != nil {
			unlock()
			return result.Err("节点更新失败")
		}
		if relayTunnelCount > 0 {
			unlock()
			return result.Err("该节点正用于三节点 nftables 串联隧道，不能切换转发模式")
		}
		referenced, err := nodeReferencedByForward(req.ID)
		if err != nil {
			unlock()
			return result.Err("节点更新失败")
		}
		if referenced {
			unlock()
			return result.Err("该节点已被转发引用，不能切换转发模式，请先删除或调整相关转发")
		}
	}

	online := node.Status == 1
	httpChanged := req.HTTP != nil && *req.HTTP != node.HTTP
	tlsChanged := req.TLS != nil && *req.TLS != node.TLS
	socksChanged := req.Socks != nil && *req.Socks != node.Socks
	ipChanged := node.IP != req.IP
	serverIPChanged := node.ServerIP != serverTarget.Host
	if online && (httpChanged || tlsChanged || socksChanged) {
		res := ws.SendMsg(node.ID, map[string]interface{}{
			"http":  req.HTTP,
			"tls":   req.TLS,
			"socks": req.Socks,
		}, "SetProtocol")
		if !gost.IsOK(res) {
			unlock()
			return result.Err(res.Msg)
		}
	}

	updates := map[string]interface{}{
		"name":         req.Name,
		"ip":           req.IP,
		"server_ip":    serverTarget.Host,
		"port_sta":     req.PortSta,
		"port_end":     req.PortEnd,
		"forward_mode": mode,
		"updated_time": time.Now().UnixMilli(),
	}
	if req.HTTP != nil {
		updates["http"] = *req.HTTP
	}
	if req.TLS != nil {
		updates["tls"] = *req.TLS
	}
	if req.Socks != nil {
		updates["socks"] = *req.Socks
	}
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.Node{}).Where("id = ?", req.ID).Updates(updates).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.Tunnel{}).Where("in_node_id = ?", req.ID).Update("in_ip", req.IP).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.Tunnel{}).Where("relay_node_id = ?", req.ID).Update("relay_ip", serverTarget.Host).Error; err != nil {
			return err
		}
		return tx.Model(&model.Tunnel{}).Where("out_node_id = ?", req.ID).Update("out_ip", serverTarget.Host).Error
	}); err != nil {
		unlock()
		return result.Err("节点更新失败")
	}
	unlock()

	InvalidateSecretCache(node.Secret)

	// IP 变更后重新下发节点侧配置（gost 链指向出口 ServerIP；nft DNAT 规则同理）
	// This path calls forward/refresh sagas which acquire node locks themselves,
	// so it must run only after releasing the UpdateNode lock.
	if ipChanged || serverIPChanged {
		if err := resyncNodeRelatedForwards(req.ID, serverIPChanged); err != nil {
			return result.Err("节点更新已保存，但运行态同步失败：" + err.Error())
		}
	}

	return result.OkMsg("节点更新成功")
}

// nodeReferencedByForward reports whether a node participates in any stored
// Forward as its tunnel entry, legacy tunnel exit, or explicit exit member.
// Callers changing forward_mode must hold the node saga lock while checking
// and through the subsequent node write.
func nodeReferencedByForward(nodeID int64) (bool, error) {
	var tunnelReferenceCount int64
	if err := model.DB.Table("forward AS f").
		Joins("JOIN tunnel AS t ON t.id = f.tunnel_id").
		Where("t.in_node_id = ? OR t.relay_node_id = ? OR t.out_node_id = ?", nodeID, nodeID, nodeID).
		Count(&tunnelReferenceCount).Error; err != nil {
		return false, err
	}
	if tunnelReferenceCount > 0 {
		return true, nil
	}

	var memberReferenceCount int64
	if err := model.DB.Table("forward_exit_member AS fem").
		Joins("JOIN forward AS f ON f.id = fem.forward_id").
		Where("fem.out_node_id = ?", nodeID).
		Count(&memberReferenceCount).Error; err != nil {
		return false, err
	}
	return memberReferenceCount > 0, nil
}

// resyncNodeRelatedForwards 节点 IP 变更后同步相关转发：
// 重新下发以该节点为出口的隧道转发（gost 链/远端服务地址依赖出口 ServerIP），
// 并刷新本节点及相关入口节点的 nft 规则。
func resyncNodeRelatedForwards(nodeID int64, serverIPChanged bool) error {
	var errs []error
	seenForwards := map[int64]struct{}{}
	if serverIPChanged {
		var dependentTunnels []model.Tunnel
		if err := model.DB.Where("(relay_node_id = ? OR out_node_id = ?) AND type = ?", nodeID, nodeID, tunnelTypeTunnelForward).Find(&dependentTunnels).Error; err != nil {
			return err
		}
		for _, t := range dependentTunnels {
			var forwards []model.Forward
			if err := model.DB.Where("tunnel_id = ?", t.ID).Find(&forwards).Error; err != nil {
				errs = append(errs, err)
				continue
			}
			for i := range forwards {
				if _, duplicate := seenForwards[forwards[i].ID]; duplicate {
					continue
				}
				seenForwards[forwards[i].ID] = struct{}{}
				if err := UpdateForwardA(&forwards[i]); err != nil {
					errs = append(errs, fmt.Errorf("forward %d: %w", forwards[i].ID, err))
				}
			}
		}
		var members []model.ForwardExitMember
		if err := model.DB.Where("out_node_id = ?", nodeID).Find(&members).Error; err != nil {
			return errors.Join(errors.Join(errs...), err)
		}
		for _, member := range members {
			if _, duplicate := seenForwards[member.ForwardID]; duplicate {
				continue
			}
			var forward model.Forward
			if err := model.DB.First(&forward, member.ForwardID).Error; err != nil {
				errs = append(errs, err)
				continue
			}
			var tunnel model.Tunnel
			if err := model.DB.First(&tunnel, forward.TunnelID).Error; err != nil {
				errs = append(errs, err)
				continue
			}
			if tunnel.Type != tunnelTypeTunnelForward {
				continue
			}
			seenForwards[forward.ID] = struct{}{}
			if err := UpdateForwardA(&forward); err != nil {
				errs = append(errs, fmt.Errorf("forward %d: %w", forward.ID, err))
			}
		}
	}
	var refreshedNode model.Node
	if err := model.DB.First(&refreshedNode, nodeID).Error; err != nil {
		errs = append(errs, err)
	} else if refreshedNode.Status == nodeStatusOnline {
		if err := RefreshNodeForwardRulesChecked(nodeID); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// DeleteNode 删除节点（检查隧道占用）
func DeleteNode(id int64) result.R {
	var node model.Node
	if err := model.DB.First(&node, id).Error; err != nil {
		return result.Err("节点不存在")
	}

	var inCount, relayCount, outCount int64
	model.DB.Model(&model.Tunnel{}).Where("in_node_id = ?", id).Count(&inCount)
	if inCount > 0 {
		return result.Err(fmt.Sprintf("该节点还有 %d 个隧道作为入口节点在使用，请先删除相关隧道", inCount))
	}
	model.DB.Model(&model.Tunnel{}).Where("relay_node_id = ?", id).Count(&relayCount)
	if relayCount > 0 {
		return result.Err(fmt.Sprintf("该节点还有 %d 个隧道作为中继节点在使用，请先删除相关隧道", relayCount))
	}
	model.DB.Model(&model.Tunnel{}).Where("out_node_id = ?", id).Count(&outCount)
	if outCount > 0 {
		return result.Err(fmt.Sprintf("该节点还有 %d 个隧道作为出口节点在使用，请先删除相关隧道", outCount))
	}
	var exitMemberCount int64
	model.DB.Model(&model.ForwardExitMember{}).Where("out_node_id = ?", id).Count(&exitMemberCount)
	if exitMemberCount > 0 {
		return result.Err(fmt.Sprintf("该节点还有 %d 个转发出口成员在使用，请先删除或调整相关转发", exitMemberCount))
	}

	if err := model.DB.Delete(&model.Node{}, id).Error; err != nil {
		return result.Err("节点删除失败")
	}
	InvalidateSecretCache(node.Secret)
	return result.OkMsg("节点删除成功")
}

// processServerAddress IPv6 主机加方括号
func processServerAddress(serverAddr string) string {
	if serverAddr == "" || strings.HasPrefix(serverAddr, "[") {
		return serverAddr
	}
	idx := strings.LastIndex(serverAddr, ":")
	if idx == -1 {
		if isIPv6Address(serverAddr) {
			return "[" + serverAddr + "]"
		}
		return serverAddr
	}
	host, port := serverAddr[:idx], serverAddr[idx:]
	if isIPv6Address(host) {
		return "[" + host + "]" + port
	}
	return serverAddr
}

func panelBaseURL(serverAddr string) string {
	addr := strings.TrimSpace(serverAddr)
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return strings.TrimRight(addr, "/")
	}
	return "http://" + processServerAddress(addr)
}

// validatePanelAssetBase 校验安装和升级共用的面板基址。
// 允许保留反向代理子路径，但禁止查询参数、片段及歧义路径；公网 HTTP 是否放行由显式安全开关决定。
func validatePanelAssetBase(raw string, allowInsecure bool) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("面板地址无效")
	}
	if u.ForceQuery || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("面板基址不能包含查询参数或片段")
	}
	normalizedPath := strings.TrimRight(u.Path, "/")
	cleanedPath := path.Clean(normalizedPath)
	if cleanedPath == "." {
		cleanedPath = ""
	}
	if u.RawPath != "" || cleanedPath != normalizedPath {
		return nil, fmt.Errorf("面板基址路径不能包含编码斜杠、重复分隔符或目录跳转")
	}
	u.Path = normalizedPath
	hostIP := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && !allowInsecure && (hostIP == nil || !hostIP.IsLoopback()) {
		return nil, fmt.Errorf("节点安装和升级要求 HTTPS")
	}
	return u, nil
}

// allowInsecureNodeDownloads 返回节点安装/升级当前是否允许明文 HTTP。
// 系统设置默认 false；环境变量 ALLOW_INSECURE_NODE_DOWNLOADS=true 继续作为
// 部署级应急覆盖。任一来源开启都会降低密钥与节点程序下载链路的安全性。
func allowInsecureNodeDownloads() bool {
	configured := strings.EqualFold(
		strings.TrimSpace(GetConfigValue(allowInsecureNodeDownloadsConfigName)),
		"true",
	)
	return nodeRuntimeConfig.AllowInsecureDownloads || configured
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

// isIPv6Address 判断是否为合法的 IPv6 地址（含 IPv4 映射地址）。
// 用 net.ParseIP 精确判断，避免 "a:b:c" 之类的非 IP 字符串被误判。
func isIPv6Address(address string) bool {
	ip := net.ParseIP(address)
	return ip != nil && ip.To4() == nil
}

// GetInstallCommand 生成节点安装命令
func GetInstallCommand(id int64, forwardMode string) result.R {
	var node model.Node
	if err := model.DB.First(&node, id).Error; err != nil {
		return result.Err("节点不存在")
	}

	ipCfg := GetConfigValue("ip")
	if ipCfg == "" {
		return result.Err("请先前往网站配置中设置ip")
	}

	mode := forwardMode
	if strings.TrimSpace(mode) == "" {
		mode = node.ForwardMode
	}
	mode = normalizeForwardMode(mode)
	script := "install.sh"
	if mode == forwardModeNftables {
		script = "install_nftables.sh"
	}

	addr := processServerAddress(ipCfg)
	allowInsecure := allowInsecureNodeDownloads()
	baseURL, err := validatePanelAssetBase(panelBaseURL(ipCfg), allowInsecure)
	if err != nil {
		return result.Err(err.Error())
	}
	scriptURL := strings.TrimRight(baseURL.String(), "/") + "/api/v1/node/install/" + url.PathEscape(script)
	runScript := "./" + script
	curlProtocolPolicy := "--proto '=https' --proto-redir '=https'"
	if baseURL.Scheme == "http" {
		curlProtocolPolicy = "--proto '=http,https' --proto-redir '=https'"
	}
	if baseURL.Scheme == "http" && allowInsecure {
		runScript = "ALLOW_INSECURE_NODE_DOWNLOADS=true " + runScript
	}
	cmd := fmt.Sprintf(
		"curl %s -fsSL %s -o ./%s && chmod +x ./%s && %s -a %s -s %s",
		curlProtocolPolicy, shellQuote(scriptURL), script, script, runScript, shellQuote(addr), shellQuote(node.Secret))
	return result.Ok(cmd)
}

// GetUninstallCommand 生成节点卸载命令
func GetUninstallCommand(id int64, forwardMode string) result.R {
	var node model.Node
	if err := model.DB.First(&node, id).Error; err != nil {
		return result.Err("节点不存在")
	}
	ipCfg := GetConfigValue("ip")
	if ipCfg == "" {
		return result.Err("请先前往网站配置中设置ip")
	}

	mode := forwardMode
	if strings.TrimSpace(mode) == "" {
		mode = node.ForwardMode
	}
	mode = normalizeForwardMode(mode)
	script := "uninstall_gost.sh"
	if mode == forwardModeNftables {
		script = "uninstall_nftables.sh"
	}

	baseURL := panelBaseURL(ipCfg)
	scriptURL := baseURL + "/api/v1/node/install/" + url.PathEscape(script)
	cmd := fmt.Sprintf(
		"curl -fsSL %s -o ./%s && chmod +x ./%s && ./%s -y",
		shellQuote(scriptURL), script, script, script)
	return result.Ok(cmd)
}

// UpgradeNode asks an online node to download the current panel-hosted node asset
// and restart its local service.
func UpgradeNode(id int64) result.R {
	var node model.Node
	if err := model.DB.First(&node, id).Error; err != nil {
		return result.Err("节点不存在")
	}
	if node.Status != 1 {
		return result.Err("节点离线，无法远程升级")
	}

	latest := latestNodeVersion(node.ForwardMode)
	if !isVersionNewer(latest, stringValue(node.Version)) {
		return result.Err("节点已是最新版本")
	}

	allowInsecure := allowInsecureNodeDownloads()
	baseURL, err := nodeUpgradeBaseURL(node, allowInsecure)
	if err != nil {
		return result.Err(err.Error())
	}
	res := nodeUpgradeSender(
		node.ID,
		strings.TrimRight(baseURL.String(), "/"),
		normalizeForwardMode(node.ForwardMode),
		latest,
		allowInsecure,
	)
	if !gost.IsOK(res) {
		if res.OutcomeUnknown {
			return result.Err("升级结果暂时未知：" + res.Msg + "。节点可能仍在执行，请不要立即重试；等待节点重连并检查版本后再决定")
		}
		return result.Err("升级指令发送失败：" + res.Msg)
	}
	return result.Ok(map[string]interface{}{
		"nodeId":        node.ID,
		"latestVersion": latest,
		"message":       "升级已触发，节点将自动重连",
	})
}

// nodeUpgradeBaseURL 为已支持本机地址保护的 Agent 优先使用最近成功连接的历史面板基址。
// 旧版、未知版本或没有历史值的 Agent 回退系统 ip，避免被污染的跨会话历史地址控制旧版下载源。
// 一旦可信历史值被选中便不静默回退；无论来源如何，均重新执行当前 HTTPS/HTTP 安全策略。
func nodeUpgradeBaseURL(node model.Node, allowInsecure bool) (*url.URL, error) {
	raw := strings.TrimSpace(node.LastConnectedBaseURL)
	if raw != "" && nodeSupportsLocalPanelBaseURL(node) {
		baseURL, err := validatePanelAssetBase(raw, allowInsecure)
		if err != nil {
			return nil, fmt.Errorf("节点历史连接地址不可用于升级：%w", err)
		}
		return baseURL, nil
	}

	ipCfg := GetConfigValue("ip")
	if strings.TrimSpace(ipCfg) == "" {
		return nil, fmt.Errorf("请先前往网站配置中设置ip")
	}
	return validatePanelAssetBase(panelBaseURL(ipCfg), allowInsecure)
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// GetNftConfigBySecret nft 节点拉取自身全部规则
func GetNftConfigBySecret(secret string) result.R {
	var node model.Node
	if err := model.DB.Where("secret = ?", secret).First(&node).Error; err != nil {
		return result.Err("节点不存在")
	}
	rules, err := buildNftRules(node.ID)
	if err != nil {
		return result.Err("读取 nft 规则失败")
	}
	return result.Ok(rules)
}

// RefreshNodeForwardRules 刷新节点 nft 规则；隧道转发联动刷新出口节点
func RefreshNodeForwardRules(nodeID int64) {
	if err := RefreshNodeForwardRulesChecked(nodeID); err != nil {
		log.Printf("刷新节点 nft 规则失败(node=%d): %v", nodeID, err)
	}
}

var sendNftRefreshMessage = ws.SendMsgLifecycle

// RefreshNodeForwardRulesChecked refreshes a node and its currently desired
// active tunnel exits, returning every transport/agent failure to the caller.
func RefreshNodeForwardRulesChecked(nodeID int64) error {
	nodeIDs, err := collectNftRefreshNodeIDs(nodeID)
	if err != nil {
		return err
	}
	for {
		unlock := lockNftSagaNodes(nodeIDs)
		current, err := collectNftRefreshNodeIDs(nodeID)
		if err != nil {
			unlock()
			return err
		}
		if nodeIDSetContains(nodeIDs, current) {
			defer unlock()
			return refreshNftNodesCheckedLocked(current)
		}
		unlock()
		nodeIDs = append(nodeIDs, current...)
	}
}

func collectNftRefreshNodeIDs(nodeID int64) ([]int64, error) {
	nodeIDs := []int64{nodeID}
	var tunnels []model.Tunnel
	if err := model.DB.Where("in_node_id = ? AND type = ?", nodeID, tunnelTypeTunnelForward).Find(&tunnels).Error; err != nil {
		return nil, fmt.Errorf("读取 nft 关联隧道: %w", err)
	}
	refreshed := map[int64]bool{nodeID: true}
	for _, t := range tunnels {
		if relayNodeID := tunnelRelayNodeID(&t); relayNodeID > 0 && !refreshed[relayNodeID] {
			refreshed[relayNodeID] = true
			nodeIDs = append(nodeIDs, relayNodeID)
		}
		var forwards []model.Forward
		if err := model.DB.Where("tunnel_id = ? AND status = 1", t.ID).Find(&forwards).Error; err != nil {
			return nil, fmt.Errorf("读取 nft 关联转发: %w", err)
		}
		for i := range forwards {
			members, err := deployForwardExitMembersDB(model.DB, &forwards[i], &t)
			if err != nil {
				return nil, fmt.Errorf("读取 nft 出口成员: %w", err)
			}
			for _, member := range members {
				if member.OutNodeID == 0 || member.OutNodeID == nodeID || refreshed[member.OutNodeID] {
					continue
				}
				refreshed[member.OutNodeID] = true
				nodeIDs = append(nodeIDs, member.OutNodeID)
			}
		}
	}
	return normalizeNodeSagaLockIDs(nodeIDs), nil
}

func nodeIDSetContains(have, want []int64) bool {
	set := make(map[int64]struct{}, len(have))
	for _, nodeID := range have {
		set[nodeID] = struct{}{}
	}
	for _, nodeID := range want {
		if _, ok := set[nodeID]; !ok {
			return false
		}
	}
	return true
}

// refreshNftNodesCheckedLocked refreshes a stable desired-state snapshot. The
// caller must hold the shared saga locks for every supplied node.
func refreshNftNodesCheckedLocked(nodeIDs []int64) error {
	seen := make(map[int64]struct{}, len(nodeIDs))
	var errs []error
	for _, nodeID := range nodeIDs {
		if nodeID <= 0 {
			continue
		}
		if _, duplicate := seen[nodeID]; duplicate {
			continue
		}
		seen[nodeID] = struct{}{}
		if err := doRefreshNodeForwardRulesChecked(nodeID); err != nil {
			errs = append(errs, fmt.Errorf("node %d: %w", nodeID, err))
		}
	}
	return errors.Join(errs...)
}

// refreshNftNodesUntilErrorLocked 用于三节点首次发布：按调用方给定的
// 下游到上游顺序执行，任一下游失败都不会继续启用入口规则。
func refreshNftNodesUntilErrorLocked(nodeIDs []int64) error {
	seen := make(map[int64]struct{}, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		if nodeID <= 0 {
			continue
		}
		if _, duplicate := seen[nodeID]; duplicate {
			continue
		}
		seen[nodeID] = struct{}{}
		// 首次发布不能接受“请求可能已执行但响应丢失”：下游未确认时
		// 必须停止，不能继续启用 B/A。补偿与重连会从错误期望态收敛。
		if err := doRefreshNodeForwardRulesWithPolicy(nodeID, false); err != nil {
			return fmt.Errorf("node %d: %w", nodeID, err)
		}
	}
	return nil
}

func doRefreshNodeForwardRulesChecked(nodeID int64) error {
	return doRefreshNodeForwardRulesWithPolicy(nodeID, true)
}

func doRefreshNodeForwardRulesWithPolicy(nodeID int64, acceptOutcomeUnknown bool) error {
	var node model.Node
	if err := model.DB.First(&node, nodeID).Error; err != nil {
		return fmt.Errorf("读取节点: %w", err)
	}
	if !isNftablesMode(&node) {
		return nil
	}
	rules, err := buildNftRules(nodeID)
	if err != nil {
		return fmt.Errorf("构建 nft 规则快照: %w", err)
	}
	commands := make([]string, 0, len(rules))
	for _, r := range rules {
		commands = append(commands, r.Rule)
	}
	res := sendNftRefreshMessage(nodeID, map[string]interface{}{"rules": commands}, "ApplyNftRules")
	if !gost.IsOK(res) {
		// Once the frame may have reached the agent, disconnect/replacement cannot
		// distinguish "not applied" from "applied but response lost". Keep the
		// committed desired state; the reconnect hook converges it again, and the
		// dirty mark lets the 30s reconciler retry without waiting for reconnect.
		if res.OutcomeUnknown && acceptOutcomeUnknown {
			markNodesDirtyBestEffort(nodeID)
			return nil
		}
		return nftIncrementalCommandError("刷新 nft 规则失败", res.Msg)
	}
	return nil
}

func refreshNftNodesDesiredCheckedLocked(nodeIDs []int64) error {
	return refreshNftNodesCheckedLocked(nodeIDs)
}

func refreshNftNodesDeletingCheckedLocked(nodeIDs []int64) error {
	seen := make(map[int64]struct{}, len(nodeIDs))
	var errs []error
	for _, nodeID := range nodeIDs {
		if nodeID <= 0 {
			continue
		}
		if _, ok := seen[nodeID]; ok {
			continue
		}
		seen[nodeID] = struct{}{}
		if err := doRefreshNodeForwardRulesWithPolicy(nodeID, false); err != nil {
			errs = append(errs, fmt.Errorf("node %d: %w", nodeID, err))
		}
	}
	return errors.Join(errs...)
}

// buildNftRules 构建节点全部 nft 规则（入口 + 出口）
func buildNftRules(nodeID int64) ([]dto.NftRuleDto, error) {
	rules := []dto.NftRuleDto{}
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := appendEntryRules(tx, &rules, nodeID); err != nil {
			return err
		}
		if err := appendRelayRules(tx, &rules, nodeID); err != nil {
			return err
		}
		return appendExitRules(tx, &rules, nodeID)
	})
	if err != nil {
		return nil, err
	}
	return rules, nil
}

// appendEntryRules 入口节点规则（带计数器注释）
func appendEntryRules(db *gorm.DB, out *[]dto.NftRuleDto, nodeID int64) error {
	var entryTunnels []model.Tunnel
	if err := db.Where("in_node_id = ?", nodeID).Find(&entryTunnels).Error; err != nil {
		return err
	}
	if len(entryTunnels) == 0 {
		return nil
	}
	tunnelByID := make(map[int64]*model.Tunnel, len(entryTunnels))
	ids := make([]int64, 0, len(entryTunnels))
	for i := range entryTunnels {
		tunnelByID[entryTunnels[i].ID] = &entryTunnels[i]
		ids = append(ids, entryTunnels[i].ID)
	}

	var forwards []model.Forward
	if err := db.Where("tunnel_id IN ? AND status = 1", ids).Find(&forwards).Error; err != nil {
		return err
	}
	for i := range forwards {
		f := &forwards[i]
		tunnel := tunnelByID[f.TunnelID]
		if tunnel == nil || f.InPort == 0 {
			continue
		}
		allowed, err := forwardPermissionAllowsRuntimeDB(db, f)
		if err != nil {
			return err
		}
		if !allowed {
			continue
		}
		protocols := resolveProtocols(tunnel)
		if len(protocols) == 0 {
			continue
		}
		userTunnelID, err := resolveUserTunnelIDDB(db, f.UserID, tunnel.ID)
		if err != nil {
			return err
		}

		if tunnel.Type == tunnelTypeTunnelForward {
			member, err := nftForwardExitMemberDB(db, f, tunnel)
			if err != nil {
				return err
			}
			if member == nil {
				continue
			}
			nextNodeID := member.OutNodeID
			nextPort := member.OutPort
			if relayNodeID := tunnelRelayNodeID(tunnel); relayNodeID > 0 {
				nextNodeID = relayNodeID
				nextPort = member.RelayPort
			}
			if nextPort <= 0 {
				continue
			}
			var nextNode model.Node
			if err := db.First(&nextNode, nextNodeID).Error; err != nil {
				return err
			}
			if strings.TrimSpace(nextNode.ServerIP) == "" {
				continue
			}
			target, err := parseTargetHostPort(nextNode.ServerIP, nextPort, true)
			if err != nil {
				continue
			}
			for _, protocol := range protocols {
				family := determineFamily(target.IP)
				rules, err := gost.BuildEntryRules(f.ID, f.UserID, userTunnelID, family, protocol, f.InPort, target.IP, target.Port)
				if err != nil {
					continue
				}
				appendRuleDtos(out, f, tunnel, protocol, f.InPort, target.IP, target.Port, rules)
			}
		} else {
			for _, target := range splitRemoteAddresses(effectiveForwardRemoteAddr(f)) {
				parsed, err := ParseTargetAddress(target, true)
				if err != nil {
					continue
				}
				for _, protocol := range protocols {
					family := determineFamily(parsed.IP)
					rules, err := gost.BuildEntryRules(f.ID, f.UserID, userTunnelID, family, protocol, f.InPort, parsed.IP, parsed.Port)
					if err != nil {
						continue
					}
					appendRuleDtos(out, f, tunnel, protocol, f.InPort, parsed.IP, parsed.Port, rules)
				}
			}
		}
	}
	return nil
}

// appendRelayRules 构建三节点链路的中继规则：B:relayPort -> C:outPort。
// 中继不写流量计数器，用户流量只在入口 A 统计一次。
func appendRelayRules(db *gorm.DB, out *[]dto.NftRuleDto, nodeID int64) error {
	var tunnels []model.Tunnel
	if err := db.Where("relay_node_id = ? AND type = ?", nodeID, tunnelTypeTunnelForward).Find(&tunnels).Error; err != nil {
		return err
	}
	for i := range tunnels {
		tunnel := &tunnels[i]
		var forwards []model.Forward
		if err := db.Where("tunnel_id = ? AND status = ?", tunnel.ID, forwardStatusActive).Find(&forwards).Error; err != nil {
			return err
		}
		for j := range forwards {
			forward := &forwards[j]
			allowed, err := forwardPermissionAllowsRuntimeDB(db, forward)
			if err != nil {
				return err
			}
			if !allowed {
				continue
			}
			members, err := deployForwardExitMembersDB(db, forward, tunnel)
			if err != nil {
				return err
			}
			for _, member := range members {
				if member.RelayPort <= 0 || member.OutPort <= 0 {
					continue
				}
				var outNode model.Node
				if err := db.First(&outNode, member.OutNodeID).Error; err != nil {
					return err
				}
				target, err := parseTargetHostPort(outNode.ServerIP, member.OutPort, true)
				if err != nil {
					continue
				}
				if err := appendRelayRulesForMember(db, out, forward, tunnel, member.RelayPort, target.IP, target.Port); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func appendRelayRulesForMember(db *gorm.DB, out *[]dto.NftRuleDto, forward *model.Forward, tunnel *model.Tunnel, listenPort int, targetIP netip.Addr, targetPort int) error {
	userTunnelID, err := resolveUserTunnelIDDB(db, forward.UserID, tunnel.ID)
	if err != nil {
		return err
	}
	for _, protocol := range resolveProtocols(tunnel) {
		rules, err := gost.BuildExitRulesWithComment(forward.ID, forward.UserID, userTunnelID, determineFamily(targetIP), protocol, listenPort, targetIP, targetPort)
		if err != nil {
			continue
		}
		appendRuleDtos(out, forward, tunnel, protocol, listenPort, targetIP, targetPort, rules)
	}
	return nil
}

// appendExitRules 出口节点规则（无计数器注释）
func appendExitRules(db *gorm.DB, out *[]dto.NftRuleDto, nodeID int64) error {
	var members []model.ForwardExitMember
	if err := db.Where("out_node_id = ? AND status = 1", nodeID).Order("id ASC").Find(&members).Error; err != nil {
		return err
	}
	handled := map[int64]bool{}
	for _, member := range members {
		var f model.Forward
		if err := db.First(&f, member.ForwardID).Error; err != nil {
			return err
		}
		if f.Status != forwardStatusActive {
			continue
		}
		allowed, err := forwardPermissionAllowsRuntimeDB(db, &f)
		if err != nil {
			return err
		}
		if !allowed {
			continue
		}
		var tunnel model.Tunnel
		if err := db.First(&tunnel, f.TunnelID).Error; err != nil {
			return err
		}
		if tunnel.Type != tunnelTypeTunnelForward {
			continue
		}
		activeMembers, err := nftForwardExitMembersForNodeDB(db, &f, &tunnel, nodeID)
		if err != nil {
			return err
		}
		for _, activeMember := range activeMembers {
			if activeMember.ID != member.ID {
				continue
			}
			if err := appendExitRulesForMember(db, out, &f, &tunnel, activeMember.OutPort); err != nil {
				return err
			}
			handled[f.ID] = true
			break
		}
	}

	// 兼容旧数据：没有成员表时仍使用 forward.out_port + tunnel.out_node_id。
	var exitTunnels []model.Tunnel
	if err := db.Where("out_node_id = ? AND type = ?", nodeID, tunnelTypeTunnelForward).Find(&exitTunnels).Error; err != nil {
		return err
	}
	for i := range exitTunnels {
		var forwards []model.Forward
		if err := db.Where("tunnel_id = ? AND status = 1", exitTunnels[i].ID).Find(&forwards).Error; err != nil {
			return err
		}
		for j := range forwards {
			if handled[forwards[j].ID] {
				continue
			}
			allowed, err := forwardPermissionAllowsRuntimeDB(db, &forwards[j])
			if err != nil {
				return err
			}
			if !allowed {
				continue
			}
			persisted, err := loadPersistedForwardExitMembersDB(db, forwards[j].ID)
			if err != nil {
				return err
			}
			if len(persisted) > 0 || forwards[j].OutPort == nil {
				continue
			}
			if err := appendExitRulesForMember(db, out, &forwards[j], &exitTunnels[i], *forwards[j].OutPort); err != nil {
				return err
			}
		}
	}
	return nil
}

// forwardPermissionAllowsRuntimeDBFn is swapped in tests to inject gate failures
// without relying on GORM callback timing (Statement.Table is unreliable in Before hooks).
var forwardPermissionAllowsRuntimeDBFn = forwardPermissionAllowsRuntimeDBImpl

func forwardPermissionAllowsRuntimeDB(db *gorm.DB, forward *model.Forward) (bool, error) {
	return forwardPermissionAllowsRuntimeDBFn(db, forward)
}

func forwardPermissionAllowsRuntimeDBImpl(db *gorm.DB, forward *model.Forward) (bool, error) {
	var permission model.UserTunnel
	err := db.Where("user_id = ? AND tunnel_id = ?", forward.UserID, forward.TunnelID).First(&permission).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		var owner model.User
		if err := db.Select("id", "role_id").First(&owner, forward.UserID).Error; err != nil {
			return false, err
		}
		// Only a real administrator may own a forward without UserTunnel.
		return owner.RoleID == adminRoleID, nil
	}
	if err != nil {
		return false, err
	}
	return permission.Status == 1, nil
}

// nftForwardExitMembersForNode returns every desired active exit member owned
// by nodeID. Balance mode deliberately has more than one deploy member, so NFT
// rule generation must not collapse it through nftForwardExitMember.
func nftForwardExitMembersForNode(forward *model.Forward, tunnel *model.Tunnel, nodeID int64) []model.ForwardExitMember {
	members, _ := nftForwardExitMembersForNodeDB(model.DB, forward, tunnel, nodeID)
	return members
}

func nftForwardExitMembersForNodeDB(db *gorm.DB, forward *model.Forward, tunnel *model.Tunnel, nodeID int64) ([]model.ForwardExitMember, error) {
	members, err := deployForwardExitMembersDB(db, forward, tunnel)
	if err != nil {
		return nil, err
	}
	out := make([]model.ForwardExitMember, 0, len(members))
	for _, member := range members {
		if member.OutNodeID == nodeID && member.OutPort > 0 {
			out = append(out, member)
		}
	}
	return out, nil
}

func appendExitRulesForMember(db *gorm.DB, out *[]dto.NftRuleDto, f *model.Forward, tunnel *model.Tunnel, listenPort int) error {
	protocols := resolveProtocols(tunnel)
	if len(protocols) == 0 {
		return nil
	}
	userTunnelID, err := resolveUserTunnelIDDB(db, f.UserID, tunnel.ID)
	if err != nil {
		return err
	}
	for _, target := range splitRemoteAddresses(effectiveForwardRemoteAddr(f)) {
		parsed, err := ParseTargetAddress(target, true)
		if err != nil {
			continue
		}
		for _, protocol := range protocols {
			family := determineFamily(parsed.IP)
			rules, err := gost.BuildExitRulesWithComment(f.ID, f.UserID, userTunnelID, family, protocol, listenPort, parsed.IP, parsed.Port)
			if err != nil {
				continue
			}
			appendRuleDtos(out, f, tunnel, protocol, listenPort, parsed.IP, parsed.Port, rules)
		}
	}
	return nil
}

func appendRuleDtos(out *[]dto.NftRuleDto, f *model.Forward, tunnel *model.Tunnel, protocol string, inPort int, targetIP netip.Addr, targetPort int, rules []string) {
	for _, rule := range rules {
		*out = append(*out, dto.NftRuleDto{
			Rule:        rule,
			ForwardID:   f.ID,
			ForwardName: f.Name,
			TunnelType:  tunnel.Type,
			InPort:      inPort,
			TargetHost:  targetIP.String(),
			TargetPort:  targetPort,
			Protocol:    protocol,
		})
	}
}

// resolveUserTunnelID 获取用户隧道权限 ID，无权限关系返回 0
func resolveUserTunnelID(userID, tunnelID int64) int64 {
	id, _ := resolveUserTunnelIDDB(model.DB, userID, tunnelID)
	return id
}

func resolveUserTunnelIDDB(db *gorm.DB, userID, tunnelID int64) (int64, error) {
	var ut model.UserTunnel
	err := db.Where("user_id = ? AND tunnel_id = ?", userID, tunnelID).First(&ut).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return ut.ID, nil
}

// determineFamily 从已解析的字面量地址决定 nfproto。
func determineFamily(targetIP netip.Addr) string {
	if targetIP.Is6() {
		return "ipv6"
	}
	return "ipv4"
}

// resolveProtocols 隧道启用的协议列表
func resolveProtocols(tunnel *model.Tunnel) []string {
	var protocols []string
	if strings.TrimSpace(tunnel.TCPListenAddr) != "" {
		protocols = append(protocols, "tcp")
	}
	if strings.TrimSpace(tunnel.UDPListenAddr) != "" {
		protocols = append(protocols, "udp")
	}
	if len(protocols) > 0 {
		return protocols
	}
	return []string{defaultProtocol(tunnel.Protocol)}
}

func defaultProtocol(protocol *string) string {
	if protocol == nil {
		return "tcp"
	}
	if strings.EqualFold(strings.TrimSpace(*protocol), "udp") {
		return "udp"
	}
	return "tcp"
}
