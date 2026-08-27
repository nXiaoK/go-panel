package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/result"
	"github.com/nXiaoK/go-panel/internal/ws"
	"gorm.io/gorm"
)

const nodeStatusOnline = 1

// CreateTunnel 创建隧道
func CreateTunnel(req dto.TunnelDto) result.R {
	if req.Type != tunnelTypePortForward && req.Type != tunnelTypeTunnelForward {
		return result.Err("隧道类型参数错误")
	}
	if req.Type == tunnelTypeTunnelForward && (req.OutNodeID == nil || *req.OutNodeID <= 0) {
		return result.Err("隧道转发必须选择出口节点")
	}
	if req.Type == tunnelTypePortForward && req.RelayNodeID != nil {
		return result.Err("端口转发不支持中继节点")
	}
	if req.RelayNodeID != nil && *req.RelayNodeID <= 0 {
		return result.Err("中继节点参数错误")
	}
	// 与节点模式切换共用 saga 锁，保证“三台均为 nftables”的校验结果
	// 一直稳定到隧道落库；否则并发 UpdateNode 可能在校验后切成 Gost。
	nodeIDs := []int64{req.InNodeID}
	if req.RelayNodeID != nil {
		nodeIDs = append(nodeIDs, *req.RelayNodeID)
	}
	if req.OutNodeID != nil {
		nodeIDs = append(nodeIDs, *req.OutNodeID)
	}
	unlockNodes := lockNftSagaNodes(nodeIDs)
	defer unlockNodes()

	// 名称唯一性
	var exist model.Tunnel
	if err := model.DB.Where("name = ?", req.Name).First(&exist).Error; err == nil {
		return result.Err("隧道名称已存在")
	}

	// 入口节点校验
	var inNode model.Node
	if err := model.DB.First(&inNode, req.InNodeID).Error; err != nil {
		return result.Err("入口节点不存在")
	}
	if inNode.Status != nodeStatusOnline {
		return result.Err("入口节点当前离线，请确保节点正常运行")
	}

	tunnel := model.Tunnel{
		Name:          req.Name,
		InNodeID:      req.InNodeID,
		InIP:          inNode.IP,
		Type:          req.Type,
		Flow:          req.Flow,
		TrafficRatio:  1.0,
		InterfaceName: req.InterfaceName,
		TCPListenAddr: "0.0.0.0",
		UDPListenAddr: "0.0.0.0",
	}
	if req.TrafficRatio != nil {
		tunnel.TrafficRatio = *req.TrafficRatio
	}
	if strings.TrimSpace(req.TCPListenAddr) != "" {
		tunnel.TCPListenAddr = req.TCPListenAddr
	}
	if strings.TrimSpace(req.UDPListenAddr) != "" {
		tunnel.UDPListenAddr = req.UDPListenAddr
	}

	// 协议（仅隧道转发）
	if req.Type == tunnelTypeTunnelForward {
		protocol := req.Protocol
		if strings.TrimSpace(protocol) == "" {
			protocol = "tls"
		}
		tunnel.Protocol = &protocol
	}

	// 出口参数
	if req.Type == tunnelTypePortForward {
		tunnel.OutNodeID = req.InNodeID
		tunnel.OutIP = inNode.ServerIP
	} else {
		if req.InNodeID == *req.OutNodeID {
			return result.Err("隧道转发模式下，入口和出口不能是同一个节点")
		}
		if strings.TrimSpace(req.Protocol) == "" {
			return result.Err("协议类型必选")
		}
		var outNode model.Node
		if err := model.DB.First(&outNode, *req.OutNodeID).Error; err != nil {
			return result.Err("出口节点不存在")
		}
		if outNode.Status != nodeStatusOnline {
			return result.Err("出口节点当前离线，请确保节点正常运行")
		}
		tunnel.OutNodeID = *req.OutNodeID
		tunnel.OutIP = outNode.ServerIP

		if req.RelayNodeID != nil {
			if *req.RelayNodeID == req.InNodeID || *req.RelayNodeID == *req.OutNodeID {
				return result.Err("入口、中继和出口节点不能重复")
			}
			var relayNode model.Node
			if err := model.DB.First(&relayNode, *req.RelayNodeID).Error; err != nil {
				return result.Err("中继节点不存在")
			}
			if relayNode.Status != nodeStatusOnline {
				return result.Err("中继节点当前离线，请确保节点正常运行")
			}
			if msg := validateRelayNftNodes(&inNode, &relayNode, &outNode); msg != "" {
				return result.Err(msg)
			}
			relayID := relayNode.ID
			relayIP := relayNode.ServerIP
			tunnel.RelayNodeID = &relayID
			tunnel.RelayIP = &relayIP
			protocol := "tcp+udp"
			tunnel.Protocol = &protocol
		}
	}

	now := time.Now().UnixMilli()
	tunnel.Status = 1
	tunnel.CreatedTime = now
	tunnel.UpdatedTime = now

	if err := model.DB.Create(&tunnel).Error; err != nil {
		return result.Err("隧道创建失败")
	}
	return result.OkMsg("隧道创建成功")
}

// GetAllTunnels 隧道列表
func GetAllTunnels() result.R {
	var list []model.Tunnel
	model.DB.Find(&list)
	return result.Ok(list)
}

func GetTunnelByID(id int64) result.R {
	var tunnel model.Tunnel
	if err := model.DB.First(&tunnel, id).Error; err != nil {
		return result.Err("隧道不存在")
	}
	return result.Ok(tunnel)
}

// UpdateTunnel 更新隧道（名称/计费/倍率/协议/监听地址），监听配置变化时同步所有转发
func UpdateTunnel(req dto.TunnelUpdateDto) result.R {
	tunnel, unlock, err := lockTunnelSagaSnapshot(req.ID)
	if err != nil {
		return result.Err("隧道不存在")
	}

	// Keep the fresh duplicate check, the tunnel write and the forward snapshot
	// in one short database transaction while the tunnel's current node saga is
	// held. Complete/Create cannot publish a new forward between that snapshot
	// and the tunnel configuration linearization point.
	var forwardIDs []int64
	needSync := false
	failMsg := ""
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		var current model.Tunnel
		if err := tx.First(&current, req.ID).Error; err != nil {
			failMsg = "隧道不存在"
			return err
		}
		var dup model.Tunnel
		if err := tx.Where("name = ? AND id <> ?", req.Name, req.ID).First(&dup).Error; err == nil {
			failMsg = "隧道名称已存在"
			return errors.New(failMsg)
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		// nil 与空串视为等价，避免端口转发隧道（Protocol 为 nil）
		// 每次更新都误触发全量同步。
		needSync = current.TCPListenAddr != req.TCPListenAddr ||
			current.UDPListenAddr != req.UDPListenAddr ||
			strPtrVal(current.Protocol) != req.Protocol ||
			strPtrVal(current.InterfaceName) != strPtrVal(req.InterfaceName)
		updates := map[string]interface{}{
			"name":            req.Name,
			"flow":            req.Flow,
			"tcp_listen_addr": req.TCPListenAddr,
			"udp_listen_addr": req.UDPListenAddr,
			"protocol":        req.Protocol,
			"interface_name":  req.InterfaceName,
			"updated_time":    time.Now().UnixMilli(),
		}
		if req.TrafficRatio != nil {
			updates["traffic_ratio"] = *req.TrafficRatio
		}
		updated := tx.Model(&model.Tunnel{}).Where("id = ?", req.ID).Updates(updates)
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			failMsg = "隧道不存在"
			return errors.New(failMsg)
		}
		if needSync {
			if err := tx.Model(&model.Forward{}).Where("tunnel_id = ?", req.ID).Pluck("id", &forwardIDs).Error; err != nil {
				return err
			}
		}
		current.Name = req.Name
		current.Flow = req.Flow
		current.TCPListenAddr = req.TCPListenAddr
		current.UDPListenAddr = req.UDPListenAddr
		current.Protocol = &req.Protocol
		current.InterfaceName = req.InterfaceName
		if req.TrafficRatio != nil {
			current.TrafficRatio = *req.TrafficRatio
		}
		tunnel = current
		return nil
	})
	// updateForwardInternal and RefreshNodeForwardRules acquire the same node
	// locks themselves. Release here so those calls cannot recursively deadlock.
	unlock()
	if err != nil {
		if failMsg != "" {
			return result.Err(failMsg)
		}
		return result.Err("隧道更新失败")
	}

	errCount := 0
	if needSync {
		for _, forwardID := range forwardIDs {
			// The forward may have been edited or moved after the tunnel
			// transaction released its node locks. Let the forward saga take
			// its own lock and rebuild the request from a fresh row so tunnel
			// synchronization cannot overwrite a newer user update.
			r := syncForwardAfterTunnelUpdate(forwardID, req.ID)
			if r.Code != 0 {
				errCount++
			}
		}
	}

	RefreshNodeForwardRules(tunnel.InNodeID)
	if errCount != 0 {
		return result.Err("隧道信息更新成功，但部分转发同步更新失败")
	}
	return result.OkMsg("隧道更新成功")
}

// strPtrVal 解引用字符串指针，nil 视为空串
func strPtrVal(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// DeleteTunnel 删除隧道（检查转发与用户权限占用）
func DeleteTunnel(id int64) result.R {
	_, unlock, err := lockTunnelSagaSnapshot(id)
	if err != nil {
		return result.Err("隧道不存在")
	}

	// The occupancy checks and delete are one transaction under the same node
	// saga as Complete/Create. This remains safe with SQLite foreign_keys=off:
	// no node-scoped forward creator can insert between Count and Delete.
	failMsg := ""
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		var current model.Tunnel
		if err := tx.First(&current, id).Error; err != nil {
			failMsg = "隧道不存在"
			return err
		}
		var forwardCount int64
		if err := tx.Model(&model.Forward{}).Where("tunnel_id = ?", id).Count(&forwardCount).Error; err != nil {
			return err
		}
		if forwardCount > 0 {
			failMsg = fmt.Sprintf("该隧道还有 %d 个转发在使用，请先删除相关转发", forwardCount)
			return errors.New(failMsg)
		}
		var utCount int64
		if err := tx.Model(&model.UserTunnel{}).Where("tunnel_id = ?", id).Count(&utCount).Error; err != nil {
			return err
		}
		if utCount > 0 {
			failMsg = fmt.Sprintf("该隧道还有 %d 个用户权限关联，请先取消用户权限分配", utCount)
			return errors.New(failMsg)
		}
		var speedLimitCount int64
		if err := tx.Model(&model.SpeedLimit{}).Where("tunnel_id = ?", id).Count(&speedLimitCount).Error; err != nil {
			return err
		}
		if speedLimitCount > 0 {
			failMsg = fmt.Sprintf("该隧道还有 %d 个限速规则，请先删除相关限速规则", speedLimitCount)
			return errors.New(failMsg)
		}
		// 隧道删除后不再有合法查询入口；同步清理其短期趋势账本，避免留下孤儿数据。
		if err := tx.Where("tunnel_id = ?", id).Delete(&model.TrafficTunnelHourly{}).Error; err != nil {
			return err
		}
		deleted := tx.Delete(&model.Tunnel{}, id)
		if deleted.Error != nil {
			return deleted.Error
		}
		if deleted.RowsAffected != 1 {
			failMsg = "隧道不存在"
			return errors.New(failMsg)
		}
		return nil
	})
	unlock()
	if err != nil {
		if failMsg != "" {
			return result.Err(failMsg)
		}
		return result.Err("隧道删除失败")
	}
	return result.OkMsg("隧道删除成功")
}

// lockTunnelSagaSnapshot locks the tunnel's current entry/exit node union and
// only then returns a fresh tunnel snapshot. If an out-of-band writer changed
// either endpoint while the caller waited, expand the union and retry.
func lockTunnelSagaSnapshot(id int64) (model.Tunnel, func(), error) {
	var before model.Tunnel
	if err := model.DB.First(&before, id).Error; err != nil {
		return model.Tunnel{}, nil, err
	}
	lockedNodeIDs := tunnelPathNodeIDs(&before)
	for {
		lockedNodeIDs = normalizeNodeSagaLockIDs(lockedNodeIDs)
		unlock := lockNftSagaNodes(lockedNodeIDs)
		var current model.Tunnel
		if err := model.DB.First(&current, id).Error; err != nil {
			unlock()
			return model.Tunnel{}, nil, err
		}
		actualNodeIDs := tunnelPathNodeIDs(&current)
		if nodeIDSetContains(lockedNodeIDs, actualNodeIDs) {
			return current, unlock, nil
		}
		unlock()
		lockedNodeIDs = append(lockedNodeIDs, actualNodeIDs...)
	}
}

// UserTunnelList 当前用户可用隧道（创建转发时选择）
func UserTunnelList(userID int64, roleID int) result.R {
	var tunnels []model.Tunnel
	if roleID == adminRoleID {
		model.DB.Where("status = 1").Find(&tunnels)
	} else {
		var uts []model.UserTunnel
		model.DB.Where("user_id = ?", userID).Find(&uts)
		if len(uts) == 0 {
			return result.Ok([]dto.TunnelListItem{})
		}
		ids := make([]int64, 0, len(uts))
		for _, ut := range uts {
			ids = append(ids, ut.TunnelID)
		}
		model.DB.Where("id IN ? AND status = 1", ids).Find(&tunnels)
	}

	items := make([]dto.TunnelListItem, 0, len(tunnels))
	for _, t := range tunnels {
		item := dto.TunnelListItem{
			ID: t.ID, Name: t.Name, IP: t.InIP,
			Type: t.Type, Protocol: t.Protocol,
		}
		var inNode model.Node
		if err := model.DB.First(&inNode, t.InNodeID).Error; err == nil {
			item.InNodePortSta = &inNode.PortSta
			item.InNodePortEnd = &inNode.PortEnd
		}
		items = append(items, item)
	}
	return result.Ok(items)
}

// DiagnosisResult 诊断结果
type DiagnosisResult struct {
	NodeID      int64   `json:"nodeId"`
	NodeName    string  `json:"nodeName"`
	TargetIP    string  `json:"targetIp"`
	TargetPort  int     `json:"targetPort"`
	Description string  `json:"description"`
	Success     bool    `json:"success"`
	Message     string  `json:"message"`
	AverageTime float64 `json:"averageTime"`
	PacketLoss  float64 `json:"packetLoss"`
	Timestamp   int64   `json:"timestamp"`
}

// performTcpPing 向节点下发 TcpPing 并解析结果
func performTcpPing(node *model.Node, targetIP string, port int, description string, count, timeoutMs int) DiagnosisResult {
	res := ws.SendMsg(node.ID, map[string]interface{}{
		"ip":      targetIP,
		"port":    port,
		"count":   count,
		"timeout": timeoutMs,
	}, "TcpPing")

	dr := DiagnosisResult{
		NodeID: node.ID, NodeName: node.Name,
		TargetIP: targetIP, TargetPort: port,
		Description: description,
		Timestamp:   time.Now().UnixMilli(),
	}

	if res.Msg == "OK" {
		if len(res.Data) > 0 {
			var ping struct {
				Success      bool    `json:"success"`
				AverageTime  float64 `json:"averageTime"`
				PacketLoss   float64 `json:"packetLoss"`
				ErrorMessage string  `json:"errorMessage"`
			}
			if err := json.Unmarshal(res.Data, &ping); err == nil {
				dr.Success = ping.Success
				if ping.Success {
					dr.Message = "TCP连接成功"
					dr.AverageTime = ping.AverageTime
					dr.PacketLoss = ping.PacketLoss
				} else {
					dr.Message = ping.ErrorMessage
					dr.AverageTime = -1.0
					dr.PacketLoss = 100.0
				}
				return dr
			}
			dr.Success = true
			dr.Message = "TCP连接成功，但无法解析详细数据"
			return dr
		}
		dr.Success = true
		dr.Message = "TCP连接成功"
		return dr
	}

	dr.Success = false
	dr.Message = res.Msg
	dr.AverageTime = -1.0
	dr.PacketLoss = 100.0
	return dr
}

// DiagnoseTunnel 隧道诊断
func DiagnoseTunnel(tunnelID int64) result.R {
	var tunnel model.Tunnel
	if err := model.DB.First(&tunnel, tunnelID).Error; err != nil {
		return result.Err("隧道不存在")
	}
	var inNode model.Node
	if err := model.DB.First(&inNode, tunnel.InNodeID).Error; err != nil {
		return result.Err("入口节点不存在")
	}

	var results []DiagnosisResult
	if tunnel.Type == tunnelTypePortForward {
		results = append(results, performTcpPing(&inNode, "www.google.com", 443, "入口->外网", 2, 3000))
	} else {
		var outNode model.Node
		if err := model.DB.First(&outNode, tunnel.OutNodeID).Error; err != nil {
			return result.Err("出口节点不存在")
		}
		// 入口->出口：
		// - 有活跃转发 → 测其 OutPort（验证 gost remote service 是否在跑）
		// - 无活跃转发 → 回退测出口节点 22（验证两机网络连通性），并在描述中标注
		// 参数 count=2, timeout=3000ms：总耗时 ≤ 6s < 面板 10s 响应超时，避免“等待响应超时”误判
		outPort := 22
		relayPort := 22
		hasActiveForward := false
		var forwards []model.Forward
		if err := model.DB.Where("tunnel_id = ? AND status = 1", tunnelID).Limit(1).Find(&forwards).Error; err == nil && len(forwards) > 0 {
			if member := activeForwardExitMember(&forwards[0], &tunnel); member != nil && member.OutPort > 0 {
				outPort = member.OutPort
				relayPort = member.RelayPort
				hasActiveForward = true
			} else if forwards[0].OutPort != nil && *forwards[0].OutPort > 0 {
				outPort = *forwards[0].OutPort
				hasActiveForward = true
			}
		}

		if relayNodeID := tunnelRelayNodeID(&tunnel); relayNodeID > 0 {
			var relayNode model.Node
			if err := model.DB.First(&relayNode, relayNodeID).Error; err != nil {
				return result.Err("中继节点不存在")
			}
			firstDesc := "入口->中继"
			secondDesc := "中继->出口"
			if !hasActiveForward || relayPort <= 0 {
				relayPort = 22
				outPort = 22
				firstDesc += "(未检测到转发,默认测SSH 22)"
				secondDesc += "(未检测到转发,默认测SSH 22)"
			}
			// 三个检查单次最多约 2.5 秒，串行总时长仍低于面板 API 超时。
			results = append(results, performTcpPing(&inNode, relayNode.ServerIP, relayPort, firstDesc, 1, 2500))
			results = append(results, performTcpPing(&relayNode, outNode.ServerIP, outPort, secondDesc, 1, 2500))
			results = append(results, performTcpPing(&outNode, "www.google.com", 443, "出口->外网", 1, 2500))
		} else {
			desc := "入口->出口"
			if !hasActiveForward {
				desc = "入口->出口(未检测到转发,默认测SSH 22)"
			}
			results = append(results, performTcpPing(&inNode, outNode.ServerIP, outPort, desc, 2, 3000))
			results = append(results, performTcpPing(&outNode, "www.google.com", 443, "出口->外网", 2, 3000))
		}
	}

	tunnelTypeName := "端口转发"
	if tunnel.Type != tunnelTypePortForward {
		tunnelTypeName = "隧道转发"
	}
	return result.Ok(map[string]interface{}{
		"tunnelId":   tunnelID,
		"tunnelName": tunnel.Name,
		"tunnelType": tunnelTypeName,
		"results":    results,
		"timestamp":  time.Now().UnixMilli(),
	})
}
