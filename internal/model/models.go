package model

// 模型与 gost.sql 的 8 张表一一对应。
// JSON 字段名与 Java 实体（Jackson 驼峰）保持一致，保证前端兼容。

// User 用户表
type User struct {
	ID               int64  `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	User             string `gorm:"column:user;size:100;not null;uniqueIndex:uidx_user_user" json:"user"`
	Pwd              string `gorm:"column:pwd;size:100;not null" json:"-"`
	TokenVersion     int64  `gorm:"column:token_version;not null;default:0" json:"-"`
	RoleID           int    `gorm:"column:role_id;not null" json:"roleId"`
	ExpTime          *int64 `gorm:"column:exp_time;not null" json:"expTime"`
	Flow             int64  `gorm:"column:flow;not null" json:"flow"`
	InFlow           int64  `gorm:"column:in_flow;not null;default:0" json:"inFlow"`
	OutFlow          int64  `gorm:"column:out_flow;not null;default:0" json:"outFlow"`
	FlowResetTime    int64  `gorm:"column:flow_reset_time;not null" json:"flowResetTime"`
	Num              int    `gorm:"column:num;not null" json:"num"`
	CreatedTime      int64  `gorm:"column:created_time;not null" json:"createdTime"`
	UpdatedTime      *int64 `gorm:"column:updated_time" json:"updatedTime"`
	Status           int    `gorm:"column:status;not null" json:"status"`
	MustChangePwd    int    `gorm:"column:must_change_pwd;not null;default:0" json:"mustChangePwd"`
	LoginFailCount   int    `gorm:"column:login_fail_count;not null;default:0" json:"-"`
	LoginLockedUntil *int64 `gorm:"column:login_locked_until" json:"-"`
}

func (User) TableName() string { return "user" }

// 用户状态常量（status 字段）
const (
	UserStatusDisabled = 0
	UserStatusActive   = 1
)

// IsActiveUserStatus 判断用户状态是否为启用。
func IsActiveUserStatus(status int) bool {
	return status == UserStatusActive
}

// Node 节点表
type Node struct {
	ID          int64   `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	Name        string  `gorm:"column:name;size:100;not null" json:"name"`
	Secret      string  `gorm:"column:secret;size:100;not null;index" json:"secret,omitempty"`
	IP          string  `gorm:"column:ip" json:"ip"`
	ServerIP    string  `gorm:"column:server_ip;size:100;not null" json:"serverIp"`
	PortSta     int     `gorm:"column:port_sta;not null" json:"portSta"`
	PortEnd     int     `gorm:"column:port_end;not null" json:"portEnd"`
	Version     *string `gorm:"column:version;size:100" json:"version"`
	HTTP        int     `gorm:"column:http;not null;default:0" json:"http"`
	TLS         int     `gorm:"column:tls;not null;default:0" json:"tls"`
	Socks       int     `gorm:"column:socks;not null;default:0" json:"socks"`
	ForwardMode string  `gorm:"column:forward_mode;size:20;not null;default:gost" json:"forwardMode"`
	// LastConnectedBaseURL 保存节点最近一次成功连接时上报的面板基址。
	// 远程升级优先复用该地址，避免系统全局地址变更后某些节点无法下载升级文件；字段仅供服务端使用，不返回前端。
	LastConnectedBaseURL  string `gorm:"column:last_connected_base_url;size:2048;not null;default:''" json:"-"`
	LastConnectedBaseTime int64  `gorm:"column:last_connected_base_time;not null;default:0" json:"-"`
	CreatedTime           int64  `gorm:"column:created_time;not null" json:"createdTime"`
	UpdatedTime           *int64 `gorm:"column:updated_time" json:"updatedTime"`
	Status                int    `gorm:"column:status;not null" json:"status"`

	LatestVersion    string `gorm:"-" json:"latestVersion,omitempty"`
	UpgradeAvailable bool   `gorm:"-" json:"upgradeAvailable"`
}

func (Node) TableName() string { return "node" }

// Tunnel 隧道表
type Tunnel struct {
	ID           int64   `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	Name         string  `gorm:"column:name;size:100;not null" json:"name"`
	TrafficRatio float64 `gorm:"column:traffic_ratio;not null;default:1.0" json:"trafficRatio"`
	InNodeID     int64   `gorm:"column:in_node_id;not null" json:"inNodeId"`
	InIP         string  `gorm:"column:in_ip;size:100;not null" json:"inIp"`
	// RelayNodeID/RelayIP 为空时保持原有两节点链路；非空时表示 nftables 中继节点。
	RelayNodeID   *int64  `gorm:"column:relay_node_id;index" json:"relayNodeId,omitempty"`
	RelayIP       *string `gorm:"column:relay_ip;size:100" json:"relayIp,omitempty"`
	OutNodeID     int64   `gorm:"column:out_node_id;not null" json:"outNodeId"`
	OutIP         string  `gorm:"column:out_ip;size:100;not null" json:"outIp"`
	Type          int     `gorm:"column:type;not null" json:"type"`
	Protocol      *string `gorm:"column:protocol;size:10" json:"protocol"`
	Flow          int     `gorm:"column:flow;not null" json:"flow"`
	TCPListenAddr string  `gorm:"column:tcp_listen_addr;size:100;not null;default:[::]" json:"tcpListenAddr"`
	UDPListenAddr string  `gorm:"column:udp_listen_addr;size:100;not null;default:[::]" json:"udpListenAddr"`
	InterfaceName *string `gorm:"column:interface_name;size:200" json:"interfaceName"`
	CreatedTime   int64   `gorm:"column:created_time;not null" json:"createdTime"`
	UpdatedTime   int64   `gorm:"column:updated_time;not null" json:"updatedTime"`
	Status        int     `gorm:"column:status;not null" json:"status"`
}

func (Tunnel) TableName() string { return "tunnel" }

// Forward 转发表
type Forward struct {
	ID               int64   `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	UserID           int64   `gorm:"column:user_id;not null;index" json:"userId"`
	UserName         string  `gorm:"column:user_name;size:100;not null" json:"userName"`
	Name             string  `gorm:"column:name;size:100;not null" json:"name"`
	TunnelID         int64   `gorm:"column:tunnel_id;not null;index" json:"tunnelId"`
	InPort           int     `gorm:"column:in_port;not null" json:"inPort"`
	OutPort          *int    `gorm:"column:out_port" json:"outPort"`
	RemoteAddr       string  `gorm:"column:remote_addr;not null" json:"remoteAddr"`
	Strategy         string  `gorm:"column:strategy;size:100;not null;default:fifo" json:"strategy"`
	TargetMode       string  `gorm:"column:target_mode;size:20;not null;default:balance" json:"targetMode"`
	ActiveRemoteAddr string  `gorm:"column:active_remote_addr;size:500;not null;default:''" json:"activeRemoteAddr"`
	ExitMode         string  `gorm:"column:exit_mode;size:20;not null;default:single" json:"exitMode"`
	ExitStrategy     string  `gorm:"column:exit_strategy;size:20;not null;default:fifo" json:"exitStrategy"`
	InterfaceName    *string `gorm:"column:interface_name;size:200" json:"interfaceName"`
	InFlow           int64   `gorm:"column:in_flow;not null;default:0" json:"inFlow"`
	OutFlow          int64   `gorm:"column:out_flow;not null;default:0" json:"outFlow"`
	CreatedTime      int64   `gorm:"column:created_time;not null" json:"createdTime"`
	UpdatedTime      int64   `gorm:"column:updated_time;not null" json:"updatedTime"`
	Status           int     `gorm:"column:status;not null" json:"status"`
	Inx              int     `gorm:"column:inx;not null;default:0" json:"inx"`
}

func (Forward) TableName() string { return "forward" }

// ForwardExitMember 转发出口成员表。
type ForwardExitMember struct {
	ID        int64 `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	ForwardID int64 `gorm:"column:forward_id;not null;index" json:"forwardId"`
	OutNodeID int64 `gorm:"column:out_node_id;not null;index" json:"outNodeId"`
	// RelayPort 是三节点 nftables 链路在中继节点上的监听端口；0 表示传统两节点链路。
	RelayPort   int   `gorm:"column:relay_port;not null;default:0" json:"relayPort"`
	OutPort     int   `gorm:"column:out_port;not null" json:"outPort"`
	Weight      int   `gorm:"column:weight;not null;default:1" json:"weight"`
	Status      int   `gorm:"column:status;not null;default:1" json:"status"`
	Active      int   `gorm:"column:active;not null;default:0" json:"active"`
	CreatedTime int64 `gorm:"column:created_time;not null" json:"createdTime"`
	UpdatedTime int64 `gorm:"column:updated_time;not null" json:"updatedTime"`
}

func (ForwardExitMember) TableName() string { return "forward_exit_member" }

// UserTunnel 用户隧道权限表
type UserTunnel struct {
	ID            int64  `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	UserID        int64  `gorm:"column:user_id;not null;index;uniqueIndex:uidx_user_tunnel_identity" json:"userId"`
	TunnelID      int64  `gorm:"column:tunnel_id;not null;index;uniqueIndex:uidx_user_tunnel_identity" json:"tunnelId"`
	SpeedID       *int64 `gorm:"column:speed_id" json:"speedId"`
	Num           int    `gorm:"column:num;not null" json:"num"`
	Flow          int64  `gorm:"column:flow;not null" json:"flow"`
	InFlow        int64  `gorm:"column:in_flow;not null;default:0" json:"inFlow"`
	OutFlow       int64  `gorm:"column:out_flow;not null;default:0" json:"outFlow"`
	FlowResetTime int64  `gorm:"column:flow_reset_time;not null" json:"flowResetTime"`
	ExpTime       *int64 `gorm:"column:exp_time;not null" json:"expTime"`
	Status        int    `gorm:"column:status;not null" json:"status"`
}

func (UserTunnel) TableName() string { return "user_tunnel" }

// SpeedLimit 限速规则表
type SpeedLimit struct {
	ID          int64  `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	Name        string `gorm:"column:name;size:100;not null" json:"name"`
	Speed       int    `gorm:"column:speed;not null" json:"speed"`
	TunnelID    int64  `gorm:"column:tunnel_id;not null" json:"tunnelId"`
	TunnelName  string `gorm:"column:tunnel_name;size:100;not null" json:"tunnelName"`
	CreatedTime int64  `gorm:"column:created_time;not null" json:"createdTime"`
	UpdatedTime *int64 `gorm:"column:updated_time" json:"updatedTime"`
	Status      int    `gorm:"column:status;not null" json:"status"`
}

func (SpeedLimit) TableName() string { return "speed_limit" }

// StatisticsFlow 流量统计表（每小时快照）
type StatisticsFlow struct {
	ID           int64  `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	UserID       int64  `gorm:"column:user_id;not null;index" json:"userId"`
	Flow         int64  `gorm:"column:flow;not null" json:"flow"`
	InFlow       int64  `gorm:"column:in_flow;not null;default:0" json:"inFlow"`
	OutFlow      int64  `gorm:"column:out_flow;not null;default:0" json:"outFlow"`
	TotalFlow    int64  `gorm:"column:total_flow;not null" json:"totalFlow"`
	TotalInFlow  int64  `gorm:"column:total_in_flow;not null;default:0" json:"totalInFlow"`
	TotalOutFlow int64  `gorm:"column:total_out_flow;not null;default:0" json:"totalOutFlow"`
	Time         string `gorm:"column:time;size:100;not null" json:"time"`
	CreatedTime  int64  `gorm:"column:created_time;not null" json:"createdTime"`
}

func (StatisticsFlow) TableName() string { return "statistics_flow" }

// TrafficHourly 用户实时双向流量小时账本。
// BucketStart 使用服务器本地整点的毫秒时间戳；用户与小时的联合唯一索引
// 保证上报增量可以在同一事务中安全累加，不能移除该唯一约束。
type TrafficHourly struct {
	ID          int64 `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	UserID      int64 `gorm:"column:user_id;not null;index;uniqueIndex:uidx_traffic_hourly_user_bucket" json:"userId"`
	BucketStart int64 `gorm:"column:bucket_start;not null;index;uniqueIndex:uidx_traffic_hourly_user_bucket" json:"bucketStart"`
	InFlow      int64 `gorm:"column:in_flow;not null;default:0" json:"inFlow"`
	OutFlow     int64 `gorm:"column:out_flow;not null;default:0" json:"outFlow"`
	CreatedTime int64 `gorm:"column:created_time;not null" json:"createdTime"`
	UpdatedTime int64 `gorm:"column:updated_time;not null" json:"updatedTime"`
}

func (TrafficHourly) TableName() string { return "traffic_hourly" }

// TrafficTunnelHourly 用户、隧道维度的实时双向流量小时账本。
// 三字段联合唯一索引保证同一上报事务可安全累加；额外的隧道、小时索引
// 用于管理员按单条隧道聚合全体用户的趋势。
type TrafficTunnelHourly struct {
	ID          int64 `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	UserID      int64 `gorm:"column:user_id;not null;index;uniqueIndex:uidx_traffic_tunnel_hourly_user_tunnel_bucket,priority:1" json:"userId"`
	TunnelID    int64 `gorm:"column:tunnel_id;not null;uniqueIndex:uidx_traffic_tunnel_hourly_user_tunnel_bucket,priority:2;index:idx_traffic_tunnel_hourly_tunnel_bucket,priority:1" json:"tunnelId"`
	BucketStart int64 `gorm:"column:bucket_start;not null;uniqueIndex:uidx_traffic_tunnel_hourly_user_tunnel_bucket,priority:3;index:idx_traffic_tunnel_hourly_tunnel_bucket,priority:2" json:"bucketStart"`
	InFlow      int64 `gorm:"column:in_flow;not null;default:0" json:"inFlow"`
	OutFlow     int64 `gorm:"column:out_flow;not null;default:0" json:"outFlow"`
	CreatedTime int64 `gorm:"column:created_time;not null" json:"createdTime"`
	UpdatedTime int64 `gorm:"column:updated_time;not null" json:"updatedTime"`
}

func (TrafficTunnelHourly) TableName() string { return "traffic_tunnel_hourly" }

// DataMigration 记录只需执行一次的数据迁移。
// Name 是稳定迁移标识；完成记录必须与迁移数据在同一事务提交，避免启动中断后
// 留下“已完成但数据不完整”的状态。
type DataMigration struct {
	Name          string `gorm:"column:name;size:128;primaryKey" json:"name"`
	CompletedTime int64  `gorm:"column:completed_time;not null" json:"completedTime"`
}

func (DataMigration) TableName() string { return "data_migration" }

// FlowReporterState is constant-size idempotency state for one node reporter.
type FlowReporterState struct {
	ID            int64  `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	NodeID        int64  `gorm:"column:node_id;not null;check:chk_flow_reporter_node_id,node_id > 0;index:idx_flow_reporter_node_reporter,unique" json:"nodeId"`
	ReporterID    string `gorm:"column:reporter_id;size:80;not null;check:chk_flow_reporter_reporter_id,reporter_id <> '' AND length(reporter_id) <= 80;index:idx_flow_reporter_node_reporter,unique" json:"reporterId"`
	LastSequence  uint64 `gorm:"column:last_sequence;not null;default:0" json:"lastSequence"`
	LastBatchID   string `gorm:"column:last_batch_id;size:80;not null;default:'';check:chk_flow_reporter_batch_id,length(last_batch_id) <= 80" json:"lastBatchId"`
	LastAckDigest string `gorm:"column:last_ack_digest;size:64;not null;default:'';check:chk_flow_reporter_ack_digest,length(last_ack_digest) IN (0,64)" json:"lastAckDigest"`
	UpdatedTime   int64  `gorm:"column:updated_time;not null" json:"updatedTime"`
}

func (FlowReporterState) TableName() string { return "flow_reporter_state" }

// NodeSyncState records one synchronization generation per node. The database
// is desired state: mutations bump DesiredGeneration inside their transaction,
// reconciliation advances AppliedGeneration only after the node acknowledged a
// complete configuration replay.
type NodeSyncState struct {
	NodeID            int64  `gorm:"column:node_id;primaryKey" json:"nodeId"`
	DesiredGeneration int64  `gorm:"column:desired_generation;not null;default:1" json:"desiredGeneration"`
	AppliedGeneration int64  `gorm:"column:applied_generation;not null;default:0" json:"appliedGeneration"`
	State             string `gorm:"column:state;size:20;not null;default:pending" json:"state"`
	LastError         string `gorm:"column:last_error;size:1000" json:"lastError"`
	LastAttemptTime   int64  `gorm:"column:last_attempt_time;not null;default:0" json:"lastAttemptTime"`
}

func (NodeSyncState) TableName() string { return "node_sync_state" }

// ViteConfig 网站配置表
type ViteConfig struct {
	ID    int64  `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	Name  string `gorm:"column:name;size:200;not null;uniqueIndex" json:"name"`
	Value string `gorm:"column:value;size:200;not null" json:"value"`
	Time  int64  `gorm:"column:time;not null" json:"time"`
}

func (ViteConfig) TableName() string { return "vite_config" }

// ProxyNode 订阅协议节点表。
type ProxyNode struct {
	ID                int64  `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	ExternalID        string `gorm:"column:external_id;size:200;not null;uniqueIndex" json:"externalId"`
	Name              string `gorm:"column:name;size:200;not null" json:"name"`
	Protocol          string `gorm:"column:protocol;size:40;not null;index" json:"protocol"`
	Server            string `gorm:"column:server;size:255;not null" json:"server"`
	Port              int    `gorm:"column:port;not null" json:"port"`
	UUID              string `gorm:"column:uuid;size:200" json:"uuid,omitempty"`
	Username          string `gorm:"column:username;size:200" json:"username,omitempty"`
	Password          string `gorm:"column:password;size:500" json:"password,omitempty"`
	Method            string `gorm:"column:method;size:100" json:"method,omitempty"`
	SNI               string `gorm:"column:sni;size:255" json:"sni,omitempty"`
	Network           string `gorm:"column:network;size:40" json:"network,omitempty"`
	Security          string `gorm:"column:security;size:40" json:"security,omitempty"`
	Path              string `gorm:"column:path;size:500" json:"path,omitempty"`
	Flow              string `gorm:"column:flow;size:100" json:"flow,omitempty"`
	PublicKey         string `gorm:"column:public_key;size:500" json:"publicKey,omitempty"`
	ShortID           string `gorm:"column:short_id;size:100" json:"shortId,omitempty"`
	Fingerprint       string `gorm:"column:fingerprint;size:100" json:"fingerprint,omitempty"`
	SnellVersion      int    `gorm:"column:snell_version;not null;default:0" json:"snellVersion,omitempty"`
	AllowInsecure     int    `gorm:"column:allow_insecure;not null;default:0" json:"allowInsecure"`
	UDP               int    `gorm:"column:udp;not null;default:1" json:"udp"`
	Link              string `gorm:"column:link;type:text" json:"link,omitempty"`
	Options           string `gorm:"column:options;type:text" json:"options,omitempty"`
	ForwardID         *int64 `gorm:"column:forward_id" json:"forwardId,omitempty"`
	SourceProxyNodeID *int64 `gorm:"column:source_proxy_node_id;index" json:"sourceProxyNodeId,omitempty"`
	RelayMode         string `gorm:"column:relay_mode;size:20" json:"relayMode,omitempty"`
	Sort              int    `gorm:"column:sort;not null;default:0" json:"sort"`
	Status            int    `gorm:"column:status;not null;default:1" json:"status"`
	LastReportTime    int64  `gorm:"column:last_report_time;not null;default:0" json:"lastReportTime"`
	CreatedTime       int64  `gorm:"column:created_time;not null" json:"createdTime"`
	UpdatedTime       int64  `gorm:"column:updated_time;not null" json:"updatedTime"`
}

func (ProxyNode) TableName() string { return "proxy_node" }

// SubscriptionProfile 订阅配置表。
type SubscriptionProfile struct {
	ID              int64  `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	Name            string `gorm:"column:name;size:200;not null" json:"name"`
	Token           string `gorm:"column:token;size:80;not null;uniqueIndex" json:"token"`
	DefaultFormat   string `gorm:"column:default_format;size:20;not null;default:surge" json:"defaultFormat"`
	Description     string `gorm:"column:description;size:500" json:"description"`
	SurgeTemplate   string `gorm:"column:surge_template;type:text" json:"surgeTemplate"`
	ClashTemplate   string `gorm:"column:clash_template;type:text" json:"clashTemplate"`
	SingboxTemplate string `gorm:"column:singbox_template;type:text" json:"singboxTemplate"`
	Status          int    `gorm:"column:status;not null;default:1" json:"status"`
	CreatedTime     int64  `gorm:"column:created_time;not null" json:"createdTime"`
	UpdatedTime     int64  `gorm:"column:updated_time;not null" json:"updatedTime"`
}

func (SubscriptionProfile) TableName() string { return "subscription_profile" }

// SubscriptionProfileNode 订阅与节点的排序关系。
type SubscriptionProfileNode struct {
	ID             int64 `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	SubscriptionID int64 `gorm:"column:subscription_id;not null;index:idx_subscription_node,unique" json:"subscriptionId"`
	ProxyNodeID    int64 `gorm:"column:proxy_node_id;not null;index:idx_subscription_node,unique" json:"proxyNodeId"`
	Sort           int   `gorm:"column:sort;not null;default:0" json:"sort"`
	CreatedTime    int64 `gorm:"column:created_time;not null" json:"createdTime"`
}

func (SubscriptionProfileNode) TableName() string { return "subscription_profile_node" }
