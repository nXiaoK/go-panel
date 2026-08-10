package service

import (
	"errors"
	"fmt"
	"sort"

	"gorm.io/gorm"

	"github.com/nXiaoK/go-panel/internal/gost"
	"github.com/nXiaoK/go-panel/internal/model"
	"github.com/nXiaoK/go-panel/internal/ws"
)

const maxGostManagedRuntimeDesired = 10000

var reconcileGostManagedRuntime = gost.ReconcileManagedRuntimeLifecycle

func desiredGostManagedRuntimeNames(nodeID int64) ([]string, []string, error) {
	serviceSet := map[string]struct{}{}
	chainSet := map[string]struct{}{}
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var node model.Node
		if err := tx.First(&node, nodeID).Error; err != nil {
			return err
		}
		if isNftablesMode(&node) {
			return nil
		}
		var forwards []model.Forward
		if err := tx.Where("status IN ?", []int{forwardStatusActive, forwardStatusPaused}).Find(&forwards).Error; err != nil {
			return err
		}
		for i := range forwards {
			forward := &forwards[i]
			var tunnel model.Tunnel
			if err := tx.First(&tunnel, forward.TunnelID).Error; err != nil {
				return err
			}
			if tunnel.Status != tunnelStatusActive {
				continue
			}
			var permission model.UserTunnel
			permissionErr := tx.Where("user_id = ? AND tunnel_id = ?", forward.UserID, tunnel.ID).First(&permission).Error
			if permissionErr == nil {
				// A disabled permission must not retain a reconnect-time runtime
				// object. Re-enable uses the lifecycle Update->Add path, so deleting
				// it here is both safe and strictly non-serving.
				if permission.Status != 1 {
					continue
				}
			} else if errors.Is(permissionErr, gorm.ErrRecordNotFound) {
				var owner model.User
				if err := tx.Select("id", "role_id").First(&owner, forward.UserID).Error; err != nil {
					return err
				}
				if owner.RoleID != adminRoleID {
					continue
				}
				permission = model.UserTunnel{}
			} else {
				return permissionErr
			}
			base := buildServiceName(forward.ID, forward.UserID, func() *model.UserTunnel {
				if permission.ID == 0 {
					return nil
				}
				return &permission
			}())
			if tunnel.InNodeID == nodeID {
				serviceSet[base+"_tcp"] = struct{}{}
				serviceSet[base+"_udp"] = struct{}{}
				if tunnel.Type == tunnelTypeTunnelForward {
					chainSet[base+"_chains"] = struct{}{}
				}
			}
			if tunnel.Type == tunnelTypeTunnelForward {
				members, err := deployForwardExitMembersDB(tx, forward, &tunnel)
				if err != nil {
					return err
				}
				for _, member := range members {
					if member.OutNodeID == nodeID {
						serviceSet[base+"_tls"] = struct{}{}
						break
					}
				}
			}
			if len(serviceSet)+len(chainSet) > maxGostManagedRuntimeDesired {
				return fmt.Errorf("managed runtime desired exceeds %d items", maxGostManagedRuntimeDesired)
			}
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	services := make([]string, 0, len(serviceSet))
	for name := range serviceSet {
		services = append(services, name)
	}
	chains := make([]string, 0, len(chainSet))
	for name := range chainSet {
		chains = append(chains, name)
	}
	sort.Strings(services)
	sort.Strings(chains)
	return services, chains, nil
}

func reconcileGostManagedRuntimeOnConnect(nodeID int64) ws.GostResult {
	services, chains, err := desiredGostManagedRuntimeNames(nodeID)
	if err != nil {
		return ws.GostResult{Msg: "构建 Gost managed runtime 期望失败: " + err.Error()}
	}
	return reconcileGostManagedRuntime(nodeID, services, chains)
}
