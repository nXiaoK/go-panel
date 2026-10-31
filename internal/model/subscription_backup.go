package model

// ProxyNodeBackup 独立保存每台主机最后一次有效恢复快照；不会随订阅节点列表或公开订阅序列化。
// Snapshot 包含服务端私钥，和站点数据库一起备份；数据库文件必须保持仅受信任管理员可读。
type ProxyNodeBackup struct {
	SourceHost  string `gorm:"column:source_host;size:85;primaryKey" json:"sourceHost"`
	Server      string `gorm:"column:server;size:255;not null" json:"server"`
	Protocols   string `gorm:"column:protocols;type:text;not null" json:"-"`
	Snapshot    string `gorm:"column:snapshot;type:text;not null" json:"-"`
	UpdatedTime int64  `gorm:"column:updated_time;not null" json:"updatedTime"`
}

func (ProxyNodeBackup) TableName() string { return "proxy_node_backup" }
