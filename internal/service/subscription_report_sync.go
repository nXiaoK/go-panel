package service

import (
	"github.com/nXiaoK/go-panel/internal/model"
	"gorm.io/gorm"
)

// saveReportedProxyNode 同步原节点与追加中转副本的连接参数。
// 恢复后 Reality 公钥可能改变，副本必须使用相同凭据，否则刷新订阅后仍无法连接；
// 只更新实际服务配置，保留各副本的名称、启停状态、排序、中转规则和订阅绑定。
// 证书校验、客户端指纹、UDP 开关和扩展选项属于副本自身策略，不随原节点上报覆盖，
// 尤其不能把严格校验证书的副本静默改成 allow_insecure=1。
func saveReportedProxyNode(node *model.ProxyNode) error {
	return model.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(node).Error; err != nil {
			return err
		}
		if node.SourceProxyNodeID != nil {
			return nil
		}
		return tx.Model(&model.ProxyNode{}).Where("source_proxy_node_id = ?", node.ID).
			Updates(map[string]interface{}{
				"protocol": node.Protocol, "server": node.Server, "port": node.Port,
				"uuid": node.UUID, "username": node.Username, "password": node.Password,
				"method": node.Method, "sni": node.SNI, "network": node.Network,
				"security": node.Security, "path": node.Path, "flow": node.Flow,
				"public_key": node.PublicKey, "short_id": node.ShortID,
				"snell_version": node.SnellVersion, "link": node.Link,
				"last_report_time": node.LastReportTime, "updated_time": node.UpdatedTime,
			}).Error
	})
}
