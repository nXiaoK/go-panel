package service

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/result"
	subscriptionassets "github.com/nXiaoK/go-panel/subscription-assets"
)

func ReportProxyNode(apiKey string, req dto.ProxyNodeReportDto) result.R {
	if !isValidSubscriptionAPIKey(apiKey) {
		return result.ErrCode(401, "API Key 无效")
	}
	if strings.TrimSpace(req.Protocol) == "" {
		return result.Err("协议不能为空")
	}
	if strings.TrimSpace(req.Server) == "" {
		return result.Err("节点地址不能为空")
	}
	if req.Port <= 0 || req.Port > 65535 {
		return result.Err("端口范围无效")
	}

	now := time.Now().UnixMilli()
	externalID := externalNodeID(req)
	var node model.ProxyNode
	err := model.DB.Where("external_id = ?", externalID).First(&node).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return result.Err("查询节点失败")
	}
	if err == gorm.ErrRecordNotFound {
		for _, legacyID := range legacyExternalNodeIDs(req) {
			if legacyID == "" || legacyID == externalID {
				continue
			}
			err = model.DB.Where("external_id = ?", legacyID).First(&node).Error
			if err == nil {
				break
			}
			if err != gorm.ErrRecordNotFound {
				return result.Err("查询节点失败")
			}
		}
	}
	applyNodeReport(&node, req, now)
	if node.ID == 0 {
		if err := model.DB.Create(&node).Error; err != nil {
			return result.Err("节点上报失败")
		}
		attachNodeToDefaultProfilesByProtocol(node.ID, node.Protocol)
		return result.Ok(node)
	}
	if err := model.DB.Save(&node).Error; err != nil {
		return result.Err("节点上报失败")
	}
	return result.Ok(node)
}

// DeleteReportedProxyNode 清理节点侧卸载后上报的订阅协议。
func DeleteReportedProxyNode(apiKey string, req dto.ProxyNodeDeleteReportDto) result.R {
	if !isValidSubscriptionAPIKey(apiKey) {
		return result.ErrCode(401, "API Key 无效")
	}
	mode := strings.ToLower(strings.TrimSpace(req.CleanupMode))
	if mode == "" {
		mode = "protocol"
	}
	sourceHost := strings.TrimSpace(req.SourceHost)
	if sourceHost == "" {
		sourceHost = strings.TrimSpace(req.Server)
	}
	legacySourceHost := strings.TrimSpace(req.LegacySourceHost)
	if mode == "server" || mode == "all" || mode == "node" {
		return deleteReportedProxyNodesBySource(uniqueStrings([]string{sourceHost, legacySourceHost}), strings.TrimSpace(req.Server))
	}
	externalIDs := uniqueStrings([]string{
		strings.TrimSpace(req.ExternalID),
		strings.TrimSpace(req.LegacyExternalID),
	})
	if len(externalIDs) == 0 {
		if sourceHost != "" && strings.TrimSpace(req.Protocol) != "" {
			externalIDs = append(externalIDs, sourceHost+"-"+strings.TrimSpace(req.Protocol))
			if legacySourceHost != "" {
				externalIDs = append(externalIDs, legacySourceHost+"-"+strings.TrimSpace(req.Protocol))
			}
		} else if strings.TrimSpace(req.Protocol) != "" && strings.TrimSpace(req.Server) != "" {
			externalIDs = append(externalIDs, externalNodeID(dto.ProxyNodeReportDto{
				Protocol: req.Protocol,
				Server:   req.Server,
			}))
		} else {
			return result.Err("缺少 externalId 或协议/节点来源")
		}
	}
	return deleteProxyNodeByExternalIDs(uniqueStrings(externalIDs))
}

func deleteProxyNodeByExternalIDs(externalIDs []string) result.R {
	var node model.ProxyNode
	err := model.DB.Where("external_id IN ?", uniqueStrings(externalIDs)).First(&node).Error
	if err == gorm.ErrRecordNotFound {
		return result.OkMsg("节点协议已不存在")
	}
	if err != nil {
		return result.Err("查询节点协议失败")
	}
	return deleteReportedProxyNodeRecord(node)
}

func deleteReportedProxyNodesBySource(sourceHosts []string, server string) result.R {
	sourceHosts = uniqueStrings(sourceHosts)
	server = strings.TrimSpace(server)
	if len(sourceHosts) == 0 && server == "" {
		return result.Err("缺少节点来源标识")
	}
	var nodes []model.ProxyNode
	query := model.DB.Where("1 = 1")
	if len(sourceHosts) > 0 {
		query = query.Where("external_id LIKE ?", sourceHosts[0]+"-%")
		for _, sourceHost := range sourceHosts[1:] {
			query = query.Or("external_id LIKE ?", sourceHost+"-%")
		}
	} else {
		query = query.Where("server = ?", server)
	}
	if err := query.Find(&nodes).Error; err != nil {
		return result.Err("查询节点协议失败")
	}
	if len(nodes) == 0 {
		return result.OkMsg("节点协议已不存在")
	}
	ids := make([]int64, 0, len(nodes))
	for _, node := range nodes {
		if r := CloseProxyNodeRelay(systemAdminUser(), dto.ProxyNodeRelayCloseDto{NodeID: node.ID}); r.Code != 0 {
			return result.Err("中转规则删除失败：" + r.Msg)
		}
		ids = append(ids, node.ID)
	}
	if err := model.DB.Delete(&model.SubscriptionProfileNode{}, "proxy_node_id IN ?", ids).Error; err != nil {
		return result.Err("删除节点关联失败")
	}
	if err := model.DB.Delete(&model.ProxyNode{}, "id IN ?", ids).Error; err != nil {
		return result.Err("节点删除失败")
	}
	return result.OkMsg("节点协议已清理")
}

func uniqueStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func attachNodeToDefaultProfilesByProtocol(nodeID int64, protocol string) {
	targetFormats := []string{"clash", "singbox", "v2rayn"}
	if normalizeProtocol(protocol) == "snell" {
		// Surge 原生支持 Snell；Mihomo/Shadowrocket 的 Clash 结构也支持 v3+ 及 UDP 标记。
		// sing-box 与 v2rayN 仍不接收 Snell，因此不能把它们加入目标格式。
		targetFormats = []string{"surge", "clash"}
	}
	var profiles []model.SubscriptionProfile
	model.DB.Where("status = ? AND default_format IN ?", 1, targetFormats).Find(&profiles)
	now := time.Now().UnixMilli()
	for _, p := range profiles {
		var count int64
		model.DB.Model(&model.SubscriptionProfileNode{}).
			Where("subscription_id = ? AND proxy_node_id = ?", p.ID, nodeID).
			Count(&count)
		if count == 0 {
			model.DB.Create(&model.SubscriptionProfileNode{
				SubscriptionID: p.ID,
				ProxyNodeID:    nodeID,
				Sort:           0,
				CreatedTime:    now,
			})
		}
	}
}

func GetProxyNodes() result.R {
	var nodes []model.ProxyNode
	model.DB.Order("sort asc, created_time desc").Find(&nodes)
	return result.Ok(buildProxyNodeViews(nodes))
}

func UpdateProxyNode(req dto.ProxyNodeUpdateDto) result.R {
	var node model.ProxyNode
	if err := model.DB.First(&node, req.ID).Error; err != nil {
		return result.Err("节点不存在")
	}
	report := dto.ProxyNodeReportDto{
		ExternalID:    node.ExternalID,
		Name:          req.Name,
		Protocol:      req.Protocol,
		Server:        req.Server,
		Port:          req.Port,
		UUID:          req.UUID,
		Username:      req.Username,
		Password:      req.Password,
		Method:        req.Method,
		SNI:           req.SNI,
		Network:       req.Network,
		Security:      req.Security,
		Path:          req.Path,
		Flow:          req.Flow,
		PublicKey:     req.PublicKey,
		ShortID:       req.ShortID,
		Fingerprint:   req.Fingerprint,
		SnellVersion:  req.SnellVersion,
		AllowInsecure: req.AllowInsecure,
		Link:          req.Link,
		Options:       req.Options,
		ForwardID:     req.ForwardID,
		Sort:          req.Sort,
		Status:        req.Status,
	}
	if req.UDP != nil {
		report.UDP = req.UDP
	} else {
		udp := node.UDP != 0
		report.UDP = &udp
	}
	applyNodeReport(&node, report, time.Now().UnixMilli())
	node.ID = req.ID
	node.ExternalID = strings.TrimSpace(req.ExternalID)
	if node.ExternalID == "" {
		node.ExternalID = externalNodeID(report)
	}
	if err := model.DB.Save(&node).Error; err != nil {
		return result.Err("节点更新失败")
	}
	return result.OkMsg("节点更新成功")
}

func AssignProxyNodeProfiles(req dto.ProxyNodeProfileAssignDto) result.R {
	var node model.ProxyNode
	if err := model.DB.First(&node, req.NodeID).Error; err != nil {
		return result.Err("节点不存在")
	}
	if err := model.DB.Delete(&model.SubscriptionProfileNode{}, "proxy_node_id = ?", req.NodeID).Error; err != nil {
		return result.Err("清理节点绑定失败")
	}
	now := time.Now().UnixMilli()
	for i, profileID := range uniqueInt64(req.ProfileIDs) {
		var profile model.SubscriptionProfile
		if err := model.DB.First(&profile, profileID).Error; err != nil {
			continue
		}
		model.DB.Create(&model.SubscriptionProfileNode{
			SubscriptionID: profileID,
			ProxyNodeID:    req.NodeID,
			Sort:           i,
			CreatedTime:    now,
		})
	}
	return result.OkMsg("节点配置绑定已更新")
}

func CreateProxyNodeRelay(cu CurrentUser, req dto.ProxyNodeRelayDto) result.R {
	mode := normalizeProxyRelayMode(req.Mode)
	var node model.ProxyNode
	if err := model.DB.First(&node, req.NodeID).Error; err != nil {
		return result.Err("节点不存在")
	}
	if node.Status != 1 {
		return result.Err("节点已禁用，无法开启中转")
	}
	if node.SourceProxyNodeID != nil {
		return result.Err("中转节点不能继续开启中转")
	}
	var tunnel model.Tunnel
	if err := model.DB.First(&tunnel, req.TunnelID).Error; err != nil {
		return result.Err("隧道不存在")
	}
	if tunnel.Status != tunnelStatusActive {
		return result.Err("隧道已禁用，无法创建中转")
	}
	if mode == proxyRelayModeAppend {
		return createProxyNodeRelayAppend(cu, node, req)
	}
	return createProxyNodeRelayReplace(cu, node, req)
}

func createProxyNodeRelayReplace(cu CurrentUser, node model.ProxyNode, req dto.ProxyNodeRelayDto) result.R {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "订阅节点-" + node.Name
	}
	strategy := strings.TrimSpace(req.Strategy)
	if strategy == "" {
		strategy = "fifo"
	}
	oldForwardID := int64(0)
	if node.ForwardID != nil {
		oldForwardID = *node.ForwardID
	}
	forward, r := createForwardForProxyNodeRelay(cu, dto.ForwardDto{
		Name:          name,
		TunnelID:      req.TunnelID,
		RemoteAddr:    joinHostPort(node.Server, node.Port),
		Strategy:      strategy,
		InPort:        req.InPort,
		InterfaceName: req.InterfaceName,
	})
	if r.Code != 0 {
		return r
	}
	if forward == nil || forward.ID == 0 {
		return result.Err("中转创建失败")
	}
	rollbackNewForward := func() {
		if r := DeleteForward(cu, forward.ID); r.Code != 0 {
			_ = ForceDeleteForward(cu, forward.ID)
		}
	}
	now := time.Now().UnixMilli()
	if err := model.DB.Model(&model.ProxyNode{}).Where("id = ?", node.ID).
		Updates(map[string]interface{}{
			"forward_id":           forward.ID,
			"source_proxy_node_id": nil,
			"relay_mode":           proxyRelayModeReplace,
			"updated_time":         now,
		}).Error; err != nil {
		rollbackNewForward()
		return result.Err("中转已创建，但绑定节点失败")
	}
	node.ForwardID = &forward.ID
	node.SourceProxyNodeID = nil
	node.RelayMode = proxyRelayModeReplace
	node.UpdatedTime = now
	if oldForwardID != 0 && oldForwardID != forward.ID {
		oldForwardIDForDelete := oldForwardID
		if r := deleteProxyNodeForward(cu, model.ProxyNode{ForwardID: &oldForwardIDForDelete}, true, true); r.Code != 0 {
			_ = model.DB.Model(&model.ProxyNode{}).Where("id = ?", node.ID).
				Updates(map[string]interface{}{"forward_id": oldForwardID, "updated_time": time.Now().UnixMilli()}).Error
			rollbackNewForward()
			return result.Err("原中转删除失败，已保留原绑定：" + r.Msg)
		}
	}
	return result.Ok(map[string]interface{}{
		"node":    buildProxyNodeViews([]model.ProxyNode{node})[0],
		"forward": forward,
	})
}

func createProxyNodeRelayAppend(cu CurrentUser, node model.ProxyNode, req dto.ProxyNodeRelayDto) result.R {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = node.Name + " · 中转"
	}
	strategy := strings.TrimSpace(req.Strategy)
	if strategy == "" {
		strategy = "fifo"
	}
	forward, r := createForwardForProxyNodeRelay(cu, dto.ForwardDto{
		Name:          name,
		TunnelID:      req.TunnelID,
		RemoteAddr:    joinHostPort(node.Server, node.Port),
		Strategy:      strategy,
		InPort:        req.InPort,
		InterfaceName: req.InterfaceName,
	})
	if r.Code != 0 {
		return r
	}
	if forward == nil || forward.ID == 0 {
		return result.Err("中转创建失败")
	}
	rollbackForward := func() {
		if r := DeleteForward(cu, forward.ID); r.Code != 0 {
			_ = ForceDeleteForward(cu, forward.ID)
		}
	}
	now := time.Now().UnixMilli()
	sourceID := node.ID
	child := cloneProxyNodeForRelay(node, sourceID, forward.ID, name, now)
	if err := model.DB.Create(&child).Error; err != nil {
		rollbackForward()
		return result.Err("中转已创建，但新增协议节点失败")
	}
	if err := cloneProxyNodeProfileBindings(node.ID, child.ID); err != nil {
		_ = deleteProxyNodeRecord(child.ID)
		rollbackForward()
		return result.Err("中转节点已创建，但继承订阅配置失败")
	}
	return result.Ok(map[string]interface{}{
		"node":    buildProxyNodeViews([]model.ProxyNode{child})[0],
		"forward": forward,
	})
}

func PreviewProxyNodeRelay(req dto.ProxyNodeRelayPreviewDto) result.R {
	var node model.ProxyNode
	if err := model.DB.First(&node, req.NodeID).Error; err != nil {
		return result.Err("节点不存在")
	}
	if node.SourceProxyNodeID != nil {
		return result.Err("中转节点不能继续开启中转")
	}
	var tunnel model.Tunnel
	if err := model.DB.First(&tunnel, req.TunnelID).Error; err != nil {
		return result.Err("隧道不存在")
	}
	if tunnel.Status != tunnelStatusActive {
		return result.Err("隧道已禁用，无法预览中转")
	}
	return result.Ok(buildProxyRelayPreview(node, tunnel, req.InPort, nil))
}

func CloseProxyNodeRelay(cu CurrentUser, req dto.ProxyNodeRelayCloseDto) result.R {
	var node model.ProxyNode
	if err := model.DB.First(&node, req.NodeID).Error; err != nil {
		return result.Err("节点不存在")
	}
	if node.SourceProxyNodeID != nil {
		forwards, nodes, r := closeDerivedProxyNodeRelay(cu, node)
		if r.Code != 0 {
			return r
		}
		return result.Ok(map[string]interface{}{
			"deletedForwards": forwards,
			"deletedNodes":    nodes,
		})
	}

	deletedForwards := 0
	if node.ForwardID != nil && *node.ForwardID != 0 {
		if r := deleteProxyNodeForward(cu, node, true, true); r.Code != 0 {
			return result.Err("替换中转删除失败：" + r.Msg)
		}
		deletedForwards++
	}
	if err := model.DB.Model(&model.ProxyNode{}).Where("id = ?", node.ID).Updates(map[string]interface{}{
		"forward_id":           nil,
		"source_proxy_node_id": nil,
		"relay_mode":           "",
		"updated_time":         time.Now().UnixMilli(),
	}).Error; err != nil {
		return result.Err("恢复原节点失败")
	}

	var children []model.ProxyNode
	if err := model.DB.Where("source_proxy_node_id = ?", node.ID).Find(&children).Error; err != nil {
		return result.Err("查询中转节点失败")
	}
	deletedNodes := 0
	for _, child := range children {
		forwards, nodes, r := closeDerivedProxyNodeRelay(cu, child)
		if r.Code != 0 {
			return r
		}
		deletedForwards += forwards
		deletedNodes += nodes
	}
	return result.Ok(map[string]interface{}{
		"deletedForwards": deletedForwards,
		"deletedNodes":    deletedNodes,
	})
}

func normalizeProxyRelayMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case proxyRelayModeAppend:
		return proxyRelayModeAppend
	default:
		return proxyRelayModeReplace
	}
}

func effectiveProxyRelayMode(node model.ProxyNode) string {
	if mode := normalizeProxyRelayModeOrEmpty(node.RelayMode); mode != "" {
		return mode
	}
	if node.SourceProxyNodeID != nil {
		return proxyRelayModeAppend
	}
	if node.ForwardID != nil && *node.ForwardID != 0 {
		return proxyRelayModeReplace
	}
	return ""
}

func normalizeProxyRelayModeOrEmpty(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case proxyRelayModeReplace:
		return proxyRelayModeReplace
	case proxyRelayModeAppend:
		return proxyRelayModeAppend
	default:
		return ""
	}
}

func cloneProxyNodeForRelay(source model.ProxyNode, sourceID, forwardID int64, name string, now int64) model.ProxyNode {
	forwardIDPtr := forwardID
	sourceIDPtr := sourceID
	return model.ProxyNode{
		ExternalID:        relayProxyExternalID(source.ID, forwardID),
		Name:              name,
		Protocol:          source.Protocol,
		Server:            source.Server,
		Port:              source.Port,
		UUID:              source.UUID,
		Username:          source.Username,
		Password:          source.Password,
		Method:            source.Method,
		SNI:               source.SNI,
		Network:           source.Network,
		Security:          source.Security,
		Path:              source.Path,
		Flow:              source.Flow,
		PublicKey:         source.PublicKey,
		ShortID:           source.ShortID,
		Fingerprint:       source.Fingerprint,
		SnellVersion:      source.SnellVersion,
		AllowInsecure:     source.AllowInsecure,
		UDP:               source.UDP,
		Link:              source.Link,
		Options:           source.Options,
		ForwardID:         &forwardIDPtr,
		SourceProxyNodeID: &sourceIDPtr,
		RelayMode:         proxyRelayModeAppend,
		Sort:              source.Sort,
		Status:            source.Status,
		LastReportTime:    source.LastReportTime,
		CreatedTime:       now,
		UpdatedTime:       now,
	}
}

func relayProxyExternalID(sourceID, forwardID int64) string {
	key := fmt.Sprintf("relay-proxy-node:%d:%d", sourceID, forwardID)
	return "relay-" + strings.ReplaceAll(uuid.NewSHA1(uuid.NameSpaceURL, []byte(key)).String(), "-", "")
}

func cloneProxyNodeProfileBindings(sourceNodeID, childNodeID int64) error {
	var links []model.SubscriptionProfileNode
	if err := model.DB.Where("proxy_node_id = ?", sourceNodeID).Order("sort asc, id asc").Find(&links).Error; err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	for _, link := range links {
		if err := model.DB.Create(&model.SubscriptionProfileNode{
			SubscriptionID: link.SubscriptionID,
			ProxyNodeID:    childNodeID,
			Sort:           link.Sort,
			CreatedTime:    now,
		}).Error; err != nil {
			return err
		}
	}
	return nil
}

func closeDerivedProxyNodeRelay(cu CurrentUser, node model.ProxyNode) (int, int, result.R) {
	deletedForwards := 0
	if node.ForwardID != nil && *node.ForwardID != 0 {
		if r := deleteProxyNodeForward(cu, node, true, true); r.Code != 0 {
			return deletedForwards, 0, result.Err("中转规则删除失败：" + r.Msg)
		}
		deletedForwards++
	}
	if err := model.DB.Delete(&model.SubscriptionProfileNode{}, "proxy_node_id = ?", node.ID).Error; err != nil {
		return deletedForwards, 0, result.Err("删除中转节点关联失败")
	}
	if err := model.DB.Delete(&model.ProxyNode{}, node.ID).Error; err != nil {
		return deletedForwards, 0, result.Err("删除中转节点失败")
	}
	return deletedForwards, 1, result.OkEmpty()
}

func buildProxyRelayPreview(node model.ProxyNode, tunnel model.Tunnel, inPort *int, forward *model.Forward) proxyRelayPreviewView {
	inNode, outNode := relayPreviewNodes(tunnel)
	entryPort := 0
	entryPortText := "创建时自动分配"
	if forward != nil {
		entryPort = forward.InPort
		entryPortText = strconv.Itoa(forward.InPort)
	} else if inPort != nil && *inPort > 0 {
		entryPort = *inPort
		entryPortText = strconv.Itoa(*inPort)
	}

	exitPort := 0
	exitPortText := "无独立出口端口"
	if tunnel.Type == tunnelTypeTunnelForward {
		exitPortText = "创建时自动分配"
		if forward != nil && forward.OutPort != nil && *forward.OutPort > 0 {
			exitPort = *forward.OutPort
			exitPortText = strconv.Itoa(*forward.OutPort)
		}
	}

	targetAddress := joinHostPort(node.Server, node.Port)
	if forward != nil && strings.TrimSpace(forward.RemoteAddr) != "" {
		targetAddress = forward.RemoteAddr
	}
	entryAddress := ""
	if strings.TrimSpace(tunnel.InIP) != "" {
		if entryPort > 0 {
			entryAddress = joinHostPort(tunnel.InIP, entryPort)
		} else {
			entryAddress = strings.TrimSpace(tunnel.InIP) + ":" + entryPortText
		}
	}
	protocol := ""
	if tunnel.Protocol != nil {
		protocol = *tunnel.Protocol
	}
	view := proxyRelayPreviewView{
		NodeID:              node.ID,
		NodeName:            node.Name,
		RelayMode:           effectiveProxyRelayMode(node),
		TunnelID:            tunnel.ID,
		TunnelName:          tunnel.Name,
		TunnelType:          tunnel.Type,
		TunnelTypeName:      tunnelTypeLabel(tunnel.Type),
		Protocol:            protocol,
		SubscriptionAddress: entryAddress,
		Entry: proxyRelayEndpointView{
			NodeID:   tunnel.InNodeID,
			NodeName: nodeNameOf(inNode),
			IP:       tunnel.InIP,
			Port:     entryPort,
			PortText: entryPortText,
			Address:  entryAddress,
		},
		Exit: proxyRelayEndpointView{
			NodeID:   tunnel.OutNodeID,
			NodeName: nodeNameOf(outNode),
			IP:       tunnel.OutIP,
			Port:     exitPort,
			PortText: exitPortText,
		},
		Target: proxyRelayEndpointView{
			IP:      extractHost(targetAddress),
			Port:    extractPort(targetAddress),
			Address: targetAddress,
		},
	}
	if forward != nil {
		view.ForwardID = forward.ID
		view.ForwardName = forward.Name
	}
	return view
}

func relayPreviewNodes(tunnel model.Tunnel) (*model.Node, *model.Node) {
	var inNode, outNode model.Node
	var inPtr, outPtr *model.Node
	if tunnel.InNodeID != 0 && model.DB.First(&inNode, tunnel.InNodeID).Error == nil {
		inPtr = &inNode
	}
	if tunnel.OutNodeID != 0 && model.DB.First(&outNode, tunnel.OutNodeID).Error == nil {
		outPtr = &outNode
	}
	return inPtr, outPtr
}

func nodeNameOf(node *model.Node) string {
	if node == nil {
		return ""
	}
	return node.Name
}

func tunnelTypeLabel(tunnelType int) string {
	if tunnelType == tunnelTypeTunnelForward {
		return "隧道转发"
	}
	return "端口转发"
}

func DeleteProxyNode(cu CurrentUser, id int64, deleteForward bool) result.R {
	var node model.ProxyNode
	if err := model.DB.First(&node, id).Error; err != nil {
		return result.Err("节点不存在")
	}
	if node.SourceProxyNodeID == nil {
		var childCount int64
		model.DB.Model(&model.ProxyNode{}).Where("source_proxy_node_id = ?", node.ID).Count(&childCount)
		if childCount > 0 && !deleteForward {
			return result.Err(fmt.Sprintf("该节点还有 %d 个关联中转节点，请先关闭中转或选择同时删除中转规则", childCount))
		}
		if childCount > 0 && deleteForward {
			if r := CloseProxyNodeRelay(cu, dto.ProxyNodeRelayCloseDto{NodeID: node.ID}); r.Code != 0 {
				return result.Err("关联中转删除失败：" + r.Msg)
			}
			return deleteProxyNodeRecord(id)
		}
	}
	if r := deleteProxyNodeForward(cu, node, deleteForward, false); r.Code != 0 {
		return result.Err("中转规则删除失败：" + r.Msg)
	}
	return deleteProxyNodeRecord(id)
}

func deleteReportedProxyNodeRecord(node model.ProxyNode) result.R {
	if r := CloseProxyNodeRelay(systemAdminUser(), dto.ProxyNodeRelayCloseDto{NodeID: node.ID}); r.Code != 0 {
		return result.Err("中转规则删除失败：" + r.Msg)
	}
	if node.SourceProxyNodeID != nil {
		return result.OkMsg("节点删除成功")
	}
	return deleteProxyNodeRecord(node.ID)
}

func deleteProxyNodeForward(cu CurrentUser, node model.ProxyNode, deleteForward bool, forceOnFailure bool) result.R {
	if !deleteForward || node.ForwardID == nil || *node.ForwardID == 0 {
		return result.OkEmpty()
	}
	var forward model.Forward
	err := model.DB.First(&forward, *node.ForwardID).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return result.Err("中转规则查询失败")
	}
	if err == gorm.ErrRecordNotFound {
		return result.OkEmpty()
	}
	if r := DeleteForward(cu, *node.ForwardID); r.Code != 0 {
		if !forceOnFailure {
			return r
		}
		if force := ForceDeleteForward(cu, *node.ForwardID); force.Code != 0 {
			return r
		}
	}
	return result.OkEmpty()
}

func systemAdminUser() CurrentUser {
	return CurrentUser{UserID: 1, RoleID: adminRoleID, UserName: defaultUsername}
}

func deleteProxyNodeRecord(id int64) result.R {
	if err := model.DB.Delete(&model.SubscriptionProfileNode{}, "proxy_node_id = ?", id).Error; err != nil {
		return result.Err("删除节点关联失败")
	}
	if err := model.DB.Delete(&model.ProxyNode{}, id).Error; err != nil {
		return result.Err("节点删除失败")
	}
	return result.OkMsg("节点删除成功")
}

func GetSubscriptionSettings() result.R {
	var profiles []model.SubscriptionProfile
	model.DB.Order("created_time asc").Find(&profiles)
	var nodes []model.ProxyNode
	model.DB.Order("sort asc, created_time desc").Find(&nodes)
	var links []model.SubscriptionProfileNode
	model.DB.Order("sort asc, id asc").Find(&links)
	var tunnels []model.Tunnel
	model.DB.Where("status = ?", tunnelStatusActive).Order("created_time desc").Find(&tunnels)
	return result.Ok(map[string]interface{}{
		"apiKey":               GetConfigValue(subAPIKeyConfigName),
		"profiles":             profiles,
		"nodes":                buildProxyNodeViews(nodes),
		"profileNodes":         links,
		"tunnels":              buildSubscriptionTunnelViews(tunnels),
		"defaultFormat":        defaultSubFormat,
		"vlessBootstrapScript": BuildVlessServerBootstrapCommand("", ""),
	})
}

func BuildVlessServerBootstrapCommand(panelURL, apiKey string) string {
	base := strings.TrimSpace(panelURL)
	if base == "" {
		base = GetConfigValue("ip")
	}
	if base == "" {
		base = "http://<Flux-Panel地址>"
	} else {
		base = panelBaseURL(base)
	}
	key := strings.TrimSpace(apiKey)
	if key == "" {
		key = GetConfigValue(subAPIKeyConfigName)
	}
	scriptURL := strings.TrimRight(base, "/") + "/api/v1/sub/" + url.PathEscape(vlessServerScriptName)
	return fmt.Sprintf(
		"curl -fsSL %s -o ./vless-server.sh && chmod +x ./vless-server.sh && ./vless-server.sh --flux-panel-bind %s %s",
		shellQuote(scriptURL), shellQuote(base), shellQuote(key))
}

func GetVlessServerScriptPath() (string, error) {
	candidates := []string{
		filepath.Join("subscription-assets", vlessServerScriptName),
		filepath.Join("..", "subscription-assets", vlessServerScriptName),
		filepath.Join("..", "..", "subscription-assets", vlessServerScriptName),
		filepath.Join(".", vlessServerScriptName),
	}
	if wd, err := os.Getwd(); err == nil {
		for dir, depth := wd, 0; depth < 8; depth++ {
			candidates = append(candidates,
				filepath.Join(dir, "subscription-assets", vlessServerScriptName),
				filepath.Join(dir, vlessServerScriptName),
			)
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("未找到 %s", vlessServerScriptName)
}

func GetVlessServerScriptContent() ([]byte, string) {
	if data, err := subscriptionassets.Files.ReadFile(vlessServerScriptName); err == nil && len(data) > 0 {
		return data, vlessServerScriptName
	}
	if path, err := GetVlessServerScriptPath(); err == nil {
		if data, readErr := os.ReadFile(path); readErr == nil && len(data) > 0 {
			return data, filepath.Base(path)
		}
	}
	return []byte(fallbackVlessServerBootstrap), vlessServerScriptName
}

func buildProxyNodeViews(nodes []model.ProxyNode) []proxyNodeView {
	if nodes == nil {
		return []proxyNodeView{}
	}
	nodeIDs := make([]int64, 0, len(nodes))
	nodeNames := map[int64]string{}
	relayChildCounts := map[int64]int{}
	for _, node := range nodes {
		nodeIDs = append(nodeIDs, node.ID)
		nodeNames[node.ID] = node.Name
		if node.SourceProxyNodeID != nil {
			relayChildCounts[*node.SourceProxyNodeID]++
		}
	}
	profileIDsByNode := map[int64][]int64{}
	if len(nodeIDs) > 0 {
		var links []model.SubscriptionProfileNode
		model.DB.Where("proxy_node_id IN ?", nodeIDs).Order("sort asc, id asc").Find(&links)
		for _, link := range links {
			profileIDsByNode[link.ProxyNodeID] = append(profileIDsByNode[link.ProxyNodeID], link.SubscriptionID)
		}
	}
	out := make([]proxyNodeView, 0, len(nodes))
	for _, node := range nodes {
		resolved := withResolvedAddress(node)
		provider, region, protocolLabel := proxyNodeClassification(node)
		view := proxyNodeView{
			ProxyNode:       node,
			ResolvedServer:  resolved.Address,
			ResolvedPort:    resolved.Port,
			ResolvedAddress: joinHostPort(resolved.Address, resolved.Port),
			ProfileIDs:      profileIDsByNode[node.ID],
			Provider:        provider,
			Region:          region,
			ProtocolLabel:   protocolLabel,
			RelayMode:       effectiveProxyRelayMode(node),
			RelayChildCount: relayChildCounts[node.ID],
		}
		if node.SourceProxyNodeID != nil {
			view.SourceNodeName = nodeNames[*node.SourceProxyNodeID]
		}
		if view.ProfileIDs == nil {
			view.ProfileIDs = []int64{}
		}
		if node.ForwardID != nil && *node.ForwardID != 0 {
			var row struct {
				Name       string `gorm:"column:name"`
				TunnelID   int64  `gorm:"column:tunnel_id"`
				TunnelName string `gorm:"column:tunnel_name"`
				TunnelType int    `gorm:"column:tunnel_type"`
				InIP       string `gorm:"column:in_ip"`
				OutIP      string `gorm:"column:out_ip"`
				InPort     int    `gorm:"column:in_port"`
				OutPort    *int   `gorm:"column:out_port"`
				RemoteAddr string `gorm:"column:remote_addr"`
			}
			model.DB.Raw(`
				SELECT f.name, f.tunnel_id, t.name AS tunnel_name, t.type AS tunnel_type,
					f.in_port, f.out_port, f.remote_addr, t.in_ip, t.out_ip
				FROM forward f
				LEFT JOIN tunnel t ON t.id = f.tunnel_id
				WHERE f.id = ?`, *node.ForwardID).Scan(&row)
			view.ForwardName = row.Name
			view.ForwardTunnelID = row.TunnelID
			view.ForwardTunnel = row.TunnelName
			view.ForwardTunnelType = row.TunnelType
			view.ForwardInIP = row.InIP
			view.ForwardInPort = row.InPort
			view.ForwardOutIP = row.OutIP
			view.ForwardTarget = row.RemoteAddr
			if row.OutPort != nil {
				view.ForwardOutPort = *row.OutPort
			}
		}
		out = append(out, view)
	}
	return out
}

func buildSubscriptionTunnelViews(tunnels []model.Tunnel) []subscriptionTunnelView {
	out := make([]subscriptionTunnelView, 0, len(tunnels))
	for _, tunnel := range tunnels {
		out = append(out, subscriptionTunnelView{
			ID:       tunnel.ID,
			Name:     tunnel.Name,
			InIP:     tunnel.InIP,
			Type:     tunnel.Type,
			Protocol: tunnel.Protocol,
			Status:   tunnel.Status,
		})
	}
	return out
}

func UpdateSubscriptionAPIKey(apiKey string) result.R {
	key := strings.TrimSpace(apiKey)
	if key == "" {
		key = randomToken(32)
	}
	if len(key) < 16 {
		return result.Err("API Key 长度不能少于 16 位")
	}
	updateOrCreateConfig(subAPIKeyConfigName, key)
	return result.Ok(key)
}

func CreateSubscriptionProfile(req dto.SubscriptionProfileDto) result.R {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return result.Err("订阅名称不能为空")
	}
	format := normalizeFormat(req.DefaultFormat)
	if format == "" {
		format = defaultSubFormat
	}
	token := strings.TrimSpace(req.Token)
	if token == "" {
		token = randomToken(24)
	}
	now := time.Now().UnixMilli()
	profile := model.SubscriptionProfile{
		Name:            name,
		Token:           token,
		DefaultFormat:   format,
		Description:     strings.TrimSpace(req.Description),
		SurgeTemplate:   defaultString(req.SurgeTemplate, fallbackSurgeTemplate),
		ClashTemplate:   defaultString(req.ClashTemplate, defaultClashTemplate()),
		SingboxTemplate: defaultString(req.SingboxTemplate, defaultSingboxTemplate()),
		Status:          statusOrDefault(req.Status, 1),
		CreatedTime:     now,
		UpdatedTime:     now,
	}
	if err := model.DB.Create(&profile).Error; err != nil {
		return result.Err("订阅创建失败")
	}
	syncProfileNodes(profile.ID, req.NodeIDs)
	return result.Ok(profile)
}

func UpdateSubscriptionProfile(req dto.SubscriptionProfileUpdateDto) result.R {
	var profile model.SubscriptionProfile
	if err := model.DB.First(&profile, req.ID).Error; err != nil {
		return result.Err("订阅不存在")
	}
	format := normalizeFormat(req.DefaultFormat)
	if format == "" {
		format = profile.DefaultFormat
	}
	profile.Name = strings.TrimSpace(req.Name)
	if profile.Name == "" {
		return result.Err("订阅名称不能为空")
	}
	profile.DefaultFormat = format
	profile.Description = strings.TrimSpace(req.Description)
	profile.SurgeTemplate = req.SurgeTemplate
	if strings.TrimSpace(profile.SurgeTemplate) == "" {
		profile.SurgeTemplate = fallbackSurgeTemplate
	}
	profile.ClashTemplate = req.ClashTemplate
	if strings.TrimSpace(profile.ClashTemplate) == "" {
		profile.ClashTemplate = defaultClashTemplate()
	}
	profile.SingboxTemplate = req.SingboxTemplate
	if strings.TrimSpace(profile.SingboxTemplate) == "" {
		profile.SingboxTemplate = defaultSingboxTemplate()
	}
	if req.Status != nil {
		profile.Status = *req.Status
	}
	profile.UpdatedTime = time.Now().UnixMilli()
	if err := model.DB.Save(&profile).Error; err != nil {
		return result.Err("订阅更新失败")
	}
	if req.NodeIDs != nil {
		syncProfileNodes(profile.ID, *req.NodeIDs)
	}
	return result.Ok(profile)
}

func defaultString(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func uniqueInt64(values []int64) []int64 {
	out := make([]int64, 0, len(values))
	seen := map[int64]bool{}
	for _, v := range values {
		if v <= 0 || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func statusOrDefault(status *int, def int) int {
	if status == nil {
		return def
	}
	return *status
}

func syncProfileNodes(profileID int64, nodeIDs []int64) {
	model.DB.Delete(&model.SubscriptionProfileNode{}, "subscription_id = ?", profileID)
	now := time.Now().UnixMilli()
	for i, nodeID := range nodeIDs {
		model.DB.Create(&model.SubscriptionProfileNode{
			SubscriptionID: profileID,
			ProxyNodeID:    nodeID,
			Sort:           i,
			CreatedTime:    now,
		})
	}
}

func DeleteSubscriptionProfile(id int64) result.R {
	model.DB.Delete(&model.SubscriptionProfileNode{}, "subscription_id = ?", id)
	if err := model.DB.Delete(&model.SubscriptionProfile{}, id).Error; err != nil {
		return result.Err("订阅删除失败")
	}
	return result.OkMsg("订阅删除成功")
}

func RegenerateSubscriptionToken(id int64) result.R {
	var profile model.SubscriptionProfile
	if err := model.DB.First(&profile, id).Error; err != nil {
		return result.Err("订阅不存在")
	}
	profile.Token = randomToken(24)
	profile.UpdatedTime = time.Now().UnixMilli()
	if err := model.DB.Save(&profile).Error; err != nil {
		return result.Err("Token 更新失败")
	}
	return result.Ok(profile)
}

func ParseProxyLink(link string) (dto.ProxyNodeReportDto, error) {
	raw := strings.TrimSpace(link)
	if raw == "" {
		return dto.ProxyNodeReportDto{}, fmt.Errorf("链接不能为空")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return dto.ProxyNodeReportDto{}, err
	}
	req := dto.ProxyNodeReportDto{
		Protocol: normalizeProtocol(u.Scheme),
		Name:     strings.TrimSpace(u.Fragment),
		Server:   u.Hostname(),
		Link:     raw,
	}
	if p, err := strconv.Atoi(u.Port()); err == nil {
		req.Port = p
	}
	switch req.Protocol {
	case "vless":
		req.UUID = u.User.Username()
		q := u.Query()
		req.Security = q.Get("security")
		req.Network = q.Get("type")
		req.SNI = firstNonEmpty(q.Get("sni"), q.Get("host"))
		req.Path = q.Get("path")
		req.Flow = q.Get("flow")
		req.PublicKey = q.Get("pbk")
		req.ShortID = q.Get("sid")
		req.Fingerprint = q.Get("fp")
		req.AllowInsecure = q.Get("allowInsecure") == "1" || q.Get("allowInsecure") == "true"
	case "trojan":
		req.Password = u.User.Username()
		q := u.Query()
		req.SNI = q.Get("sni")
		req.Security = "tls"
	case "socks5":
		req.Username = u.User.Username()
		req.Password, _ = u.User.Password()
	case "ss":
		req.Password = strings.TrimPrefix(u.Opaque, "//")
	default:
		return dto.ProxyNodeReportDto{}, fmt.Errorf("暂不支持解析该链接协议")
	}
	if req.Name == "" {
		req.Name = strings.ToUpper(req.Protocol) + "-" + req.Server
	}
	return req, nil
}

func ImportProxyLink(apiKey, link string) result.R {
	req, err := ParseProxyLink(link)
	if err != nil {
		return result.Err(err.Error())
	}
	return ReportProxyNode(apiKey, req)
}

func ImportProxyLinkForAdmin(link string) result.R {
	return ImportProxyLink(GetConfigValue(subAPIKeyConfigName), link)
}
