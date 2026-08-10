package service

import (
	"errors"
	"log"
	"math"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/nXiaoK/go-panel/internal/dto"
	"github.com/nXiaoK/go-panel/internal/gost"
	"github.com/nXiaoK/go-panel/internal/model"
)

// 流量上报处理（对应 Java FlowController 业务部分）

const defaultUserTunnelID int64 = 0

type flowServiceRef struct {
	forwardID      int64
	userID         int64
	userTunnelID   int64
	protocolSuffix string
}

func parseFlowServiceName(name string) (flowServiceRef, bool) {
	parts := strings.Split(strings.TrimSpace(name), "_")
	if len(parts) == 4 {
		switch parts[3] {
		case "tcp", "udp", "tls":
		default:
			return flowServiceRef{}, false
		}
		ref, ok := parseFlowServiceParts(parts[:3])
		if ok {
			ref.protocolSuffix = parts[3]
		}
		return ref, ok
	}
	if len(parts) != 3 {
		return flowServiceRef{}, false
	}
	return parseFlowServiceParts(parts)
}

func parseFlowServiceParts(parts []string) (flowServiceRef, bool) {
	forwardID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || forwardID <= 0 {
		return flowServiceRef{}, false
	}
	userID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || userID <= 0 {
		return flowServiceRef{}, false
	}
	userTunnelID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || userTunnelID < 0 {
		return flowServiceRef{}, false
	}
	return flowServiceRef{forwardID: forwardID, userID: userID, userTunnelID: userTunnelID}, true
}

// maxSingleReportBytes 限制单次 Gost 流量上报的最大增量（1TB）。
// 防止恶意/异常节点单次上报超大值打爆用户流量触发自动暂停。
const maxSingleReportBytes int64 = 1 << 40 // 1 TiB

// validateGostFlowBounds keeps the legacy Gost report envelope independent of
// the NFT reporter protocol constants.
func validateGostFlowBounds(u, d int64) bool {
	return u >= 0 && d >= 0 && u <= maxSingleReportBytes && d <= maxSingleReportBytes
}

func validateNftFlowBounds(up, down int64) bool {
	return up >= 0 && down >= 0 && up <= dto.MaxNftFlowItemBytes && down <= dto.MaxNftFlowItemBytes
}

func ApplyGostFlow(tx *gorm.DB, node AuthenticatedNode, flow dto.FlowDto) error {
	ref, ok := parseFlowServiceName(flow.N)
	if !ok || !validateGostFlowBounds(flow.U, flow.D) {
		return ErrInvalidFlowReport
	}
	return applyAuthenticatedFlow(tx, node, forwardModeGost, ref, flow, time.Now())
}

func ApplyNftFlowItem(tx *gorm.DB, node AuthenticatedNode, item dto.NftFlowItem) error {
	return applyNftFlowItemAt(tx, node, item, time.Now())
}

func applyNftFlowItemAt(tx *gorm.DB, node AuthenticatedNode, item dto.NftFlowItem, recordedAt time.Time) error {
	if item.ForwardID == nil || item.UserID == nil || item.Up == nil || item.Down == nil {
		return ErrInvalidFlowReport
	}
	userTunnelID := defaultUserTunnelID
	if item.UserTunnelID != nil {
		userTunnelID = *item.UserTunnelID
	}
	if *item.ForwardID <= 0 || *item.UserID <= 0 || userTunnelID < 0 || !validateNftFlowBounds(*item.Up, *item.Down) {
		return ErrInvalidFlowReport
	}
	return applyAuthenticatedFlow(tx, node, forwardModeNftables, flowServiceRef{
		forwardID: *item.ForwardID, userID: *item.UserID, userTunnelID: userTunnelID,
	}, dto.FlowDto{U: *item.Up, D: *item.Down}, recordedAt)
}

func applyAuthenticatedFlow(tx *gorm.DB, node AuthenticatedNode, expectedMode string, ref flowServiceRef, flow dto.FlowDto, recordedAt time.Time) error {
	if tx == nil {
		return ErrInvalidFlowReport
	}
	if node.ID <= 0 || normalizeForwardMode(node.ForwardMode) != expectedMode {
		return ErrFlowNodeMismatch
	}
	var forward model.Forward
	if err := tx.First(&forward, ref.forwardID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrFlowNodeMismatch
		}
		return err
	}
	var tunnel model.Tunnel
	if err := tx.First(&tunnel, forward.TunnelID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrFlowNodeMismatch
		}
		return err
	}
	if tunnel.InNodeID != node.ID || forward.UserID != ref.userID {
		return ErrFlowNodeMismatch
	}
	var user model.User
	if err := tx.First(&user, ref.userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrFlowNodeMismatch
		}
		return err
	}
	if ref.userTunnelID == defaultUserTunnelID {
		var count int64
		if err := tx.Model(&model.UserTunnel{}).
			Where("user_id = ? AND tunnel_id = ?", ref.userID, forward.TunnelID).
			Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return ErrFlowNodeMismatch
		}
	} else {
		var userTunnel model.UserTunnel
		if err := tx.First(&userTunnel, ref.userTunnelID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrFlowNodeMismatch
			}
			return err
		}
		if userTunnel.UserID != ref.userID || userTunnel.TunnelID != forward.TunnelID {
			return ErrFlowNodeMismatch
		}
	}
	adjusted := scaleFlow(flow, tunnel.TrafficRatio, tunnel.Flow)
	if err := incrementFlowCounters(tx.Model(&model.Forward{}).Where("id = ?", forward.ID), adjusted); err != nil {
		return err
	}
	if err := incrementFlowCounters(tx.Model(&model.User{}).Where("id = ?", ref.userID), adjusted); err != nil {
		return err
	}
	if ref.userTunnelID != defaultUserTunnelID {
		if err := incrementFlowCounters(tx.Model(&model.UserTunnel{}).Where("id = ?", ref.userTunnelID), adjusted); err != nil {
			return err
		}
	}
	if err := incrementTrafficHourly(tx, ref.userID, adjusted, recordedAt); err != nil {
		return err
	}
	if err := incrementTrafficTunnelHourly(tx, ref.userID, forward.TunnelID, adjusted, recordedAt); err != nil {
		return err
	}
	return nil
}

func incrementFlowCounters(query *gorm.DB, stats dto.FlowDto) error {
	result := query.Updates(map[string]interface{}{
		"in_flow":  gorm.Expr("in_flow + ?", stats.D),
		"out_flow": gorm.Expr("out_flow + ?", stats.U),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrFlowNodeMismatch
	}
	return nil
}

func incrementTrafficHourly(tx *gorm.DB, userID int64, stats dto.FlowDto, recordedAt time.Time) error {
	nowMs := recordedAt.UnixMilli()
	row := model.TrafficHourly{
		UserID:      userID,
		BucketStart: model.TrafficHourlyBucketStart(recordedAt),
		InFlow:      stats.D,
		OutFlow:     stats.U,
		CreatedTime: nowMs,
		UpdatedTime: nowMs,
	}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}, {Name: "bucket_start"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"in_flow":      gorm.Expr("in_flow + excluded.in_flow"),
			"out_flow":     gorm.Expr("out_flow + excluded.out_flow"),
			"updated_time": gorm.Expr("excluded.updated_time"),
		}),
	}).Create(&row).Error
}

func incrementTrafficTunnelHourly(tx *gorm.DB, userID, tunnelID int64, stats dto.FlowDto, recordedAt time.Time) error {
	nowMs := recordedAt.UnixMilli()
	row := model.TrafficTunnelHourly{
		UserID:      userID,
		TunnelID:    tunnelID,
		BucketStart: model.TrafficHourlyBucketStart(recordedAt),
		InFlow:      stats.D,
		OutFlow:     stats.U,
		CreatedTime: nowMs,
		UpdatedTime: nowMs,
	}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}, {Name: "tunnel_id"}, {Name: "bucket_start"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"in_flow":      gorm.Expr("in_flow + excluded.in_flow"),
			"out_flow":     gorm.Expr("out_flow + excluded.out_flow"),
			"updated_time": gorm.Expr("excluded.updated_time"),
		}),
	}).Create(&row).Error
}

type flowLimitTarget struct {
	userID       int64
	userTunnelID int64
}

// EnforceGostFlowLimits applies desired-state quota consequences after the
// caller has committed the report transaction.
func EnforceGostFlowLimits(flow dto.FlowDto) {
	ref, ok := parseFlowServiceName(flow.N)
	if !ok || ref.userTunnelID == defaultUserTunnelID {
		return
	}
	enforceCommittedFlowLimits([]flowLimitTarget{{userID: ref.userID, userTunnelID: ref.userTunnelID}})
}

// EnforceNftFlowLimits applies each affected quota target once after the whole
// NFT batch transaction has committed.
func EnforceNftFlowLimits(items []dto.NftFlowItem) {
	targets := make(map[flowLimitTarget]struct{}, len(items))
	for _, item := range items {
		if item.UserID == nil || item.UserTunnelID == nil || *item.UserID <= 0 || *item.UserTunnelID <= 0 {
			continue
		}
		targets[flowLimitTarget{userID: *item.UserID, userTunnelID: *item.UserTunnelID}] = struct{}{}
	}
	unique := make([]flowLimitTarget, 0, len(targets))
	for target := range targets {
		unique = append(unique, target)
	}
	enforceCommittedFlowLimits(unique)
}

func enforceCommittedFlowLimits(targets []flowLimitTarget) {
	users := make(map[int64]struct{}, len(targets))
	for _, target := range targets {
		users[target.userID] = struct{}{}
	}
	for userID := range users {
		checkUserRelatedLimits(userID)
	}
	for _, target := range targets {
		checkUserTunnelRelatedLimits(target.userTunnelID, target.userID)
	}
}

func checkUserRelatedLimits(userID int64) {
	var user model.User
	if err := model.DB.First(&user, userID).Error; err != nil {
		log.Printf("用户流量限制检查失败(user=%d): %v", userID, err)
		return
	}
	if flowLimitBytes(user.Flow) < totalFlowBytes(user.InFlow, user.OutFlow) ||
		(user.ExpTime != nil && *user.ExpTime <= time.Now().UnixMilli()) ||
		!isActiveUserStatus(user.Status) {
		pauseAllUserServices(userID)
	}
}

func pauseAllUserServices(userID int64) {
	var forwards []model.Forward
	if err := model.DB.Where("user_id = ?", userID).Find(&forwards).Error; err != nil {
		log.Printf("查询用户转发失败(user=%d): %v", userID, err)
		return
	}
	pauseServices(forwards)
}

func checkUserTunnelRelatedLimits(userTunnelID, userID int64) {
	var userTunnel model.UserTunnel
	if err := model.DB.First(&userTunnel, userTunnelID).Error; err != nil {
		log.Printf("用户隧道流量限制检查失败(userTunnel=%d): %v", userTunnelID, err)
		return
	}
	if totalFlowBytes(userTunnel.InFlow, userTunnel.OutFlow) >= flowLimitBytes(userTunnel.Flow) ||
		(userTunnel.ExpTime != nil && *userTunnel.ExpTime <= time.Now().UnixMilli()) ||
		userTunnel.Status != 1 {
		pauseSpecificForward(userTunnel.TunnelID, userID)
	}
}

func pauseSpecificForward(tunnelID, userID int64) {
	var forwards []model.Forward
	if err := model.DB.Where("tunnel_id = ? AND user_id = ?", tunnelID, userID).Find(&forwards).Error; err != nil {
		log.Printf("查询用户隧道转发失败(tunnel=%d, user=%d): %v", tunnelID, userID, err)
		return
	}
	pauseServices(forwards)
}

// defaultFlowType 默认计费类型：双向计费
const defaultFlowType = 2

// scaleFlow 应用倍率与计费类型，单次取整。
// flowType 非法（<=0）时回退为默认双向计费，避免误把流量清零。
func scaleFlow(flow dto.FlowDto, ratio float64, flowType int) dto.FlowDto {
	if flowType <= 0 {
		flowType = defaultFlowType
	}
	flow.D = safeFloatToInt64(float64(flow.D) * ratio * float64(flowType))
	flow.U = safeFloatToInt64(float64(flow.U) * ratio * float64(flowType))
	return flow
}

// safeFloatToInt64 安全地将 float64 转换为 int64，防止溢出
func safeFloatToInt64(f float64) int64 {
	if math.IsNaN(f) {
		log.Printf("警告：流量计算结果为 NaN，重置为 0")
		return 0
	}
	if math.IsInf(f, 1) || f >= float64(math.MaxInt64) {
		log.Printf("警告：流量计算溢出，截断为 MaxInt64")
		return math.MaxInt64
	}
	if math.IsInf(f, -1) {
		log.Printf("警告：流量计算结果为负无穷，重置为 0")
		return 0
	}
	// 检查是否超过 int64 最大值
	if f < 0 {
		log.Printf("警告：流量计算出现负数 (%.0f)，重置为 0", f)
		return 0
	}
	return int64(f)
}

func flowLimitBytes(gb int64) int64 {
	if gb <= 0 {
		return 0
	}
	if gb > math.MaxInt64/bytesToGB {
		return math.MaxInt64
	}
	return gb * bytesToGB
}

func totalFlowBytes(inFlow, outFlow int64) int64 {
	if inFlow < 0 {
		inFlow = 0
	}
	if outFlow < 0 {
		outFlow = 0
	}
	if inFlow > math.MaxInt64-outFlow {
		return math.MaxInt64
	}
	return inFlow + outFlow
}

// PauseForwards 暂停一组转发并同步节点侧状态（定时到期任务等外部调用）
func PauseForwards(forwards []model.Forward) {
	pauseServices(forwards)
}

// pauseServices 暂停一组转发（gost 模式下发 PauseService；nft 模式刷新规则）。
// 服务名按每条转发单独构建，不能复用触发上报的那条服务名。
func pauseServices(forwards []model.Forward) {
	for _, f := range forwards {
		var tunnel model.Tunnel
		if err := model.DB.First(&tunnel, f.TunnelID).Error; err == nil {
			var inNode model.Node
			nftMode := false
			if model.DB.First(&inNode, tunnel.InNodeID).Error == nil {
				nftMode = isNftablesMode(&inNode)
			}
			// 先落库再刷新，保证 nft 规则按新状态生成
			model.DB.Model(&model.Forward{}).Where("id = ?", f.ID).Update("status", 0)
			if nftMode {
				RefreshNodeForwardRules(tunnel.InNodeID)
				if tunnel.Type == tunnelTypeTunnelForward {
					for _, node := range forwardExitNodeMap(deployForwardExitMembers(&f, &tunnel)) {
						if node.ID == tunnel.InNodeID {
							continue
						}
						RefreshNodeForwardRules(node.ID)
					}
				}
			} else {
				name := buildServiceName(f.ID, f.UserID, getUserTunnel(f.UserID, tunnel.ID))
				gost.PauseService(tunnel.InNodeID, name)
				if tunnel.Type == tunnelTypeTunnelForward {
					for _, node := range forwardExitNodeMap(deployForwardExitMembers(&f, &tunnel)) {
						gost.PauseRemoteService(node.ID, name)
					}
				}
			}
			continue
		}
		model.DB.Model(&model.Forward{}).Where("id = ?", f.ID).Update("status", 0)
	}
}

// CleanNodeConfigs gost 配置自检：清理孤立的服务/链/限速器（对应 CheckGostConfigAsync）
func CleanNodeConfigs(nodeID string, cfg dto.GostConfigDto) {
	// 该函数由 SafeGo 异步执行，HTTP 中间件持有的数据库操作门可能已经释放；
	// 必须在 goroutine 内独立登记，避免站点恢复替换数据库句柄时继续查询旧连接。
	leave, ok := model.Gate.Enter()
	if !ok {
		log.Printf("跳过节点配置清理：数据库正在维护 (节点: %s)", nodeID)
		return
	}
	defer leave()

	var node model.Node
	if err := model.DB.First(&node, nodeID).Error; err != nil {
		log.Printf("跳过节点配置清理：查询节点失败 (节点: %s): %v", nodeID, err)
		return
	}

	// 孤立服务
	for _, candidate := range gostServiceCleanupCandidates(cfg.Services) {
		var count int64
		if err := model.DB.Model(&model.Forward{}).
			Where("id = ?", candidate.forwardID).
			Count(&count).Error; err != nil {
			log.Printf("跳过孤立服务清理：查询转发失败 (服务: %s, 节点: %d): %v", candidate.reportedName, node.ID, err)
			continue
		}
		if count == 0 {
			log.Printf("删除孤立的服务: %s (节点: %d)", candidate.reportedName, node.ID)
			if candidate.remote {
				gost.DeleteRemoteService(node.ID, candidate.serviceName)
			} else {
				gost.DeleteService(node.ID, candidate.serviceName)
			}
		}
	}

	// 孤立链
	for _, chain := range cfg.Chains {
		parts := strings.Split(chain.Name, "_")
		if len(parts) != 4 || parts[3] != "chains" {
			continue
		}
		forwardID, userID, userTunnelID := parts[0], parts[1], parts[2]
		var count int64
		if err := model.DB.Model(&model.Forward{}).Where("id = ?", forwardID).Count(&count).Error; err != nil {
			log.Printf("跳过孤立链清理：查询转发失败 (链: %s, 节点: %d): %v", chain.Name, node.ID, err)
			continue
		}
		if count == 0 {
			log.Printf("删除孤立的链: %s (节点: %d)", chain.Name, node.ID)
			gost.DeleteChains(node.ID, forwardID+"_"+userID+"_"+userTunnelID)
		}
	}

	// 孤立限速器
	for _, limiter := range cfg.Limiters {
		id, err := strconv.ParseInt(limiter.Name, 10, 64)
		if err != nil {
			continue
		}
		var count int64
		if err := model.DB.Model(&model.SpeedLimit{}).Where("id = ?", id).Count(&count).Error; err != nil {
			log.Printf("跳过孤立限流器清理：查询策略失败 (限流器: %s, 节点: %d): %v", limiter.Name, node.ID, err)
			continue
		}
		if count == 0 {
			log.Printf("删除孤立的限流器: %s (节点: %d)", limiter.Name, node.ID)
			gost.DeleteLimiters(node.ID, id)
		}
	}
}

type gostServiceCleanupCandidate struct {
	reportedName string
	serviceName  string
	forwardID    string
	remote       bool
}

func gostServiceCleanupCandidates(services []dto.ConfigItem) []gostServiceCleanupCandidate {
	candidates := make([]gostServiceCleanupCandidate, 0, len(services))
	seen := make(map[string]struct{}, len(services))
	for _, svc := range services {
		if svc.Name == "web_api" {
			continue
		}
		parts := strings.Split(svc.Name, "_")
		if len(parts) != 4 {
			continue
		}
		forwardID, userID, userTunnelID, typ := parts[0], parts[1], parts[2], parts[3]
		if typ != "tcp" && typ != "udp" && typ != "tls" {
			continue
		}

		serviceName := forwardID + "_" + userID + "_" + userTunnelID
		remote := typ == "tls"
		// tcp/udp 是同一条转发的成对主服务，DeleteService 一次会删除两者。
		cleanupKey := serviceName + ":main"
		if remote {
			cleanupKey = serviceName + ":remote"
		}
		if _, exists := seen[cleanupKey]; exists {
			continue
		}
		seen[cleanupKey] = struct{}{}
		candidates = append(candidates, gostServiceCleanupCandidate{
			reportedName: svc.Name,
			serviceName:  serviceName,
			forwardID:    forwardID,
			remote:       remote,
		})
	}
	return candidates
}
