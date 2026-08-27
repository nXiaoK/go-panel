package dto

// 请求/响应结构，JSON 字段与 Java DTO（Jackson 驼峰）一致

// LoginDto 登录请求
type LoginDto struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// UserDto 创建用户
type UserDto struct {
	User          string `json:"user" binding:"required"`
	Pwd           string `json:"pwd" binding:"required"`
	Flow          int64  `json:"flow" binding:"min=0"`
	Num           int    `json:"num" binding:"min=0"`
	ExpTime       int64  `json:"expTime" binding:"required"`
	FlowResetTime int64  `json:"flowResetTime"`
	Status        *int   `json:"status"`
}

// UserUpdateDto 更新用户
type UserUpdateDto struct {
	ID            int64  `json:"id" binding:"required"`
	User          string `json:"user" binding:"required"`
	Pwd           string `json:"pwd"`
	Flow          int64  `json:"flow" binding:"min=0"`
	Num           int    `json:"num" binding:"min=0"`
	ExpTime       int64  `json:"expTime" binding:"required"`
	FlowResetTime int64  `json:"flowResetTime"`
	Status        *int   `json:"status"`
}

// ChangePasswordDto 修改账号密码
type ChangePasswordDto struct {
	NewUsername     string `json:"newUsername" binding:"required"`
	CurrentPassword string `json:"currentPassword" binding:"required"`
	NewPassword     string `json:"newPassword" binding:"required"`
	ConfirmPassword string `json:"confirmPassword" binding:"required"`
}

// ResetFlowDto 流量清零
type ResetFlowDto struct {
	ID   int64 `json:"id" binding:"required"`
	Type int   `json:"type"`
}

// UserPackageQueryDto 控制台套餐/统计查询
type UserPackageQueryDto struct {
	Range    string `json:"range"`
	TunnelID *int64 `json:"tunnelId"`
}

// NodeDto 创建节点
type NodeDto struct {
	Name        string `json:"name" binding:"required"`
	IP          string `json:"ip" binding:"required"`
	ServerIP    string `json:"serverIp" binding:"required"`
	PortSta     int    `json:"portSta" binding:"required,min=1,max=65535"`
	PortEnd     int    `json:"portEnd" binding:"required,min=1,max=65535"`
	HTTP        *int   `json:"http"`
	TLS         *int   `json:"tls"`
	Socks       *int   `json:"socks"`
	ForwardMode string `json:"forwardMode" binding:"required"`
}

// NodeUpdateDto 更新节点
type NodeUpdateDto struct {
	ID          int64  `json:"id" binding:"required"`
	Name        string `json:"name" binding:"required"`
	IP          string `json:"ip" binding:"required"`
	ServerIP    string `json:"serverIp" binding:"required"`
	PortSta     int    `json:"portSta" binding:"required,min=1,max=65535"`
	PortEnd     int    `json:"portEnd" binding:"required,min=1,max=65535"`
	HTTP        *int   `json:"http"`
	TLS         *int   `json:"tls"`
	Socks       *int   `json:"socks"`
	ForwardMode string `json:"forwardMode" binding:"required"`
}

// TunnelDto 创建隧道
type TunnelDto struct {
	Name          string   `json:"name" binding:"required"`
	InNodeID      int64    `json:"inNodeId" binding:"required"`
	RelayNodeID   *int64   `json:"relayNodeId"`
	OutNodeID     *int64   `json:"outNodeId"`
	Type          int      `json:"type" binding:"required"`
	Flow          int      `json:"flow" binding:"required"`
	TrafficRatio  *float64 `json:"trafficRatio"`
	InterfaceName *string  `json:"interfaceName"`
	Protocol      string   `json:"protocol"`
	TCPListenAddr string   `json:"tcpListenAddr"`
	UDPListenAddr string   `json:"udpListenAddr"`
}

// TunnelUpdateDto 更新隧道
type TunnelUpdateDto struct {
	ID            int64    `json:"id" binding:"required"`
	Name          string   `json:"name" binding:"required"`
	Flow          int      `json:"flow" binding:"required"`
	TrafficRatio  *float64 `json:"trafficRatio"`
	Protocol      string   `json:"protocol" binding:"required"`
	TCPListenAddr string   `json:"tcpListenAddr" binding:"required"`
	UDPListenAddr string   `json:"udpListenAddr" binding:"required"`
	InterfaceName *string  `json:"interfaceName"`
}

// TunnelSpeedTestDto 节点间 iperf3 测速请求
type TunnelSpeedTestDto struct {
	TunnelID        int64  `json:"tunnelId" binding:"required"`
	TestID          string `json:"testId"`
	Direction       string `json:"direction"`
	DurationSeconds int    `json:"durationSeconds"`
	Parallel        int    `json:"parallel"`
	Port            int    `json:"port"`
}

// ForwardDto 创建转发
type ForwardDto struct {
	Name              string                 `json:"name" binding:"required"`
	TunnelID          int64                  `json:"tunnelId" binding:"required"`
	RemoteAddr        string                 `json:"remoteAddr" binding:"required"`
	Strategy          string                 `json:"strategy"`
	InPort            *int                   `json:"inPort"`
	TargetMode        string                 `json:"targetMode"`
	ActiveRemoteAddr  string                 `json:"activeRemoteAddr"`
	ForceSwitchTarget bool                   `json:"forceSwitchTarget"`
	ExitMode          string                 `json:"exitMode"`
	ExitStrategy      string                 `json:"exitStrategy"`
	ExitMembers       []ForwardExitMemberDto `json:"exitMembers"`
	InterfaceName     *string                `json:"interfaceName"`
}

// ForwardUpdateDto 更新转发
type ForwardUpdateDto struct {
	ID                int64                  `json:"id" binding:"required"`
	UserID            int64                  `json:"userId"`
	Name              string                 `json:"name" binding:"required"`
	TunnelID          int64                  `json:"tunnelId" binding:"required"`
	RemoteAddr        string                 `json:"remoteAddr" binding:"required"`
	Strategy          string                 `json:"strategy"`
	InPort            *int                   `json:"inPort"`
	TargetMode        string                 `json:"targetMode"`
	ActiveRemoteAddr  string                 `json:"activeRemoteAddr"`
	ForceSwitchTarget bool                   `json:"forceSwitchTarget"`
	ExitMode          string                 `json:"exitMode"`
	ExitStrategy      string                 `json:"exitStrategy"`
	ExitMembers       []ForwardExitMemberDto `json:"exitMembers"`
	InterfaceName     *string                `json:"interfaceName"`
}

// ForwardExitMemberDto 转发出口候选节点。
type ForwardExitMemberDto struct {
	OutNodeID int64 `json:"outNodeId" binding:"required"`
	Active    bool  `json:"active"`
	Weight    int   `json:"weight"`
}

// UserTunnelDto 分配用户隧道权限
type UserTunnelDto struct {
	UserID        int64  `json:"userId" binding:"required"`
	TunnelID      int64  `json:"tunnelId" binding:"required"`
	Flow          int64  `json:"flow" binding:"min=0"`
	Num           int    `json:"num" binding:"min=0"`
	FlowResetTime int64  `json:"flowResetTime"`
	ExpTime       int64  `json:"expTime" binding:"required"`
	SpeedID       *int64 `json:"speedId"`
}

// UserTunnelUpdateDto 更新用户隧道权限
type UserTunnelUpdateDto struct {
	ID            int64  `json:"id" binding:"required"`
	Flow          int64  `json:"flow" binding:"min=0"`
	Num           int    `json:"num" binding:"min=0"`
	FlowResetTime *int64 `json:"flowResetTime"`
	ExpTime       *int64 `json:"expTime"`
	Status        *int   `json:"status" binding:"required,oneof=0 1"`
	SpeedID       *int64 `json:"speedId"`
}

// UserTunnelQueryDto 查询用户隧道权限
type UserTunnelQueryDto struct {
	UserID int64 `json:"userId" binding:"required"`
}

// SpeedLimitDto 创建限速规则
type SpeedLimitDto struct {
	Name       string `json:"name" binding:"required"`
	Speed      int    `json:"speed" binding:"required,min=1"`
	TunnelID   int64  `json:"tunnelId" binding:"required"`
	TunnelName string `json:"tunnelName" binding:"required"`
}

// SpeedLimitUpdateDto 更新限速规则
type SpeedLimitUpdateDto struct {
	ID         int64  `json:"id" binding:"required"`
	Name       string `json:"name" binding:"required"`
	Speed      int    `json:"speed" binding:"required,min=1"`
	TunnelID   int64  `json:"tunnelId" binding:"required"`
	TunnelName string `json:"tunnelName" binding:"required"`
}

// FlowDto gost 流量上报：n=服务名(forwardId_userId_userTunnelId), u=上行, d=下行
type FlowDto struct {
	N string `json:"n"`
	U int64  `json:"u"`
	D int64  `json:"d"`
}

// NFT flow protocol limits are shared by the panel and node reporter.
const (
	MaxNftFlowBatchItems       = 10_000
	MaxNftFlowItemBytes  int64 = 1 << 40
)

// NftFlowBatchDto nftables 流量批量上报
type NftFlowBatchDto struct {
	Items []NftFlowItem `json:"items"`
}

// NftFlowBatchV2Dto is the durable, idempotent nft counter upload contract.
type NftFlowBatchV2Dto struct {
	ReporterID string `json:"reporterId"`
	Sequence   uint64 `json:"sequence"`
	BatchID    string `json:"batchId"`
	// CapturedAt 是节点冻结本批 nft 计数快照时的 Unix 毫秒时间戳。
	// omitempty 保证升级前已持久化的批次仍保持原始 JSON 与摘要，可安全重传。
	CapturedAt int64         `json:"capturedAt,omitempty"`
	Items      []NftFlowItem `json:"items"`
}

// NftFlowAckDto identifies the exact committed batch acknowledged by the panel.
type NftFlowAckDto struct {
	ReporterID string `json:"reporterId"`
	Sequence   uint64 `json:"sequence"`
	BatchID    string `json:"batchId"`
	AckDigest  string `json:"ackDigest"`
}

// NftFlowItem 单条 nft 计数项
type NftFlowItem struct {
	ForwardID    *int64 `json:"forwardId"`
	UserID       *int64 `json:"userId"`
	UserTunnelID *int64 `json:"userTunnelId"`
	Up           *int64 `json:"up"`
	Down         *int64 `json:"down"`
}

// ConfigItem gost 配置项（自检上报）
type ConfigItem struct {
	Name string `json:"name"`
}

// ProxyNodeReportDto 节点协议上报/导入请求。
type ProxyNodeReportDto struct {
	ExternalID       string `json:"externalId"`
	SourceHost       string `json:"sourceHost"`
	LegacyExternalID string `json:"legacyExternalId"`
	LegacySourceHost string `json:"legacySourceHost"`
	Name             string `json:"name"`
	Protocol         string `json:"protocol" binding:"required"`
	Server           string `json:"server" binding:"required"`
	Port             int    `json:"port" binding:"required,min=1,max=65535"`
	UUID             string `json:"uuid"`
	Username         string `json:"username"`
	Password         string `json:"password"`
	Method           string `json:"method"`
	SNI              string `json:"sni"`
	Network          string `json:"network"`
	Security         string `json:"security"`
	Path             string `json:"path"`
	Flow             string `json:"flow"`
	PublicKey        string `json:"publicKey"`
	ShortID          string `json:"shortId"`
	Fingerprint      string `json:"fingerprint"`
	SnellVersion     int    `json:"snellVersion"`
	AllowInsecure    bool   `json:"allowInsecure"`
	UDP              *bool  `json:"udp"`
	Link             string `json:"link"`
	Options          string `json:"options"`
	ForwardID        *int64 `json:"forwardId"`
	Sort             *int   `json:"sort"`
	Status           *int   `json:"status"`
}

// ProxyNodeDeleteReportDto 节点协议卸载/整机卸载上报请求。
type ProxyNodeDeleteReportDto struct {
	ExternalID       string `json:"externalId"`
	LegacyExternalID string `json:"legacyExternalId"`
	Protocol         string `json:"protocol"`
	Server           string `json:"server"`
	SourceHost       string `json:"sourceHost"`
	LegacySourceHost string `json:"legacySourceHost"`
	CleanupMode      string `json:"cleanupMode"`
}

// ProxyNodeUpdateDto 管理端更新订阅节点。
type ProxyNodeUpdateDto struct {
	ID            int64  `json:"id" binding:"required"`
	ExternalID    string `json:"externalId"`
	Name          string `json:"name" binding:"required"`
	Protocol      string `json:"protocol" binding:"required"`
	Server        string `json:"server" binding:"required"`
	Port          int    `json:"port" binding:"required,min=1,max=65535"`
	UUID          string `json:"uuid"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	Method        string `json:"method"`
	SNI           string `json:"sni"`
	Network       string `json:"network"`
	Security      string `json:"security"`
	Path          string `json:"path"`
	Flow          string `json:"flow"`
	PublicKey     string `json:"publicKey"`
	ShortID       string `json:"shortId"`
	Fingerprint   string `json:"fingerprint"`
	SnellVersion  int    `json:"snellVersion"`
	AllowInsecure bool   `json:"allowInsecure"`
	UDP           *bool  `json:"udp"`
	Link          string `json:"link"`
	Options       string `json:"options"`
	ForwardID     *int64 `json:"forwardId"`
	Sort          *int   `json:"sort"`
	Status        *int   `json:"status"`
}

// ProxyNodeProfileAssignDto 从节点侧分配订阅配置。
type ProxyNodeProfileAssignDto struct {
	NodeID     int64   `json:"nodeId" binding:"required"`
	ProfileIDs []int64 `json:"profileIds"`
}

// ProxyNodeRelayDto 为协议节点创建并绑定中转。
type ProxyNodeRelayDto struct {
	NodeID        int64   `json:"nodeId" binding:"required"`
	TunnelID      int64   `json:"tunnelId" binding:"required"`
	Mode          string  `json:"mode"`
	Name          string  `json:"name"`
	InPort        *int    `json:"inPort"`
	Strategy      string  `json:"strategy"`
	InterfaceName *string `json:"interfaceName"`
}

// ProxyNodeRelayPreviewDto 预览协议节点中转入口和出口信息。
type ProxyNodeRelayPreviewDto struct {
	NodeID   int64 `json:"nodeId" binding:"required"`
	TunnelID int64 `json:"tunnelId" binding:"required"`
	InPort   *int  `json:"inPort"`
}

// ProxyNodeRelayCloseDto 关闭协议节点关联中转。
type ProxyNodeRelayCloseDto struct {
	NodeID int64 `json:"nodeId" binding:"required"`
}

// SubscriptionProfileDto 创建订阅配置。
type SubscriptionProfileDto struct {
	Name            string  `json:"name" binding:"required"`
	Token           string  `json:"token"`
	DefaultFormat   string  `json:"defaultFormat"`
	Description     string  `json:"description"`
	SurgeTemplate   string  `json:"surgeTemplate"`
	ClashTemplate   string  `json:"clashTemplate"`
	SingboxTemplate string  `json:"singboxTemplate"`
	Status          *int    `json:"status"`
	NodeIDs         []int64 `json:"nodeIds"`
}

// SubscriptionProfileUpdateDto 更新订阅配置。
type SubscriptionProfileUpdateDto struct {
	ID              int64    `json:"id" binding:"required"`
	Name            string   `json:"name" binding:"required"`
	DefaultFormat   string   `json:"defaultFormat"`
	Description     string   `json:"description"`
	SurgeTemplate   string   `json:"surgeTemplate"`
	ClashTemplate   string   `json:"clashTemplate"`
	SingboxTemplate string   `json:"singboxTemplate"`
	Status          *int     `json:"status"`
	NodeIDs         *[]int64 `json:"nodeIds"`
}

// GostConfigDto gost 配置自检上报
type GostConfigDto struct {
	Limiters []ConfigItem `json:"limiters"`
	Chains   []ConfigItem `json:"chains"`
	Services []ConfigItem `json:"services"`
}

// NftRuleDto nft 规则下发项
type NftRuleDto struct {
	Rule        string `json:"rule"`
	ForwardID   int64  `json:"forwardId"`
	ForwardName string `json:"forwardName"`
	TunnelType  int    `json:"tunnelType"`
	InPort      int    `json:"inPort"`
	TargetHost  string `json:"targetHost"`
	TargetPort  int    `json:"targetPort"`
	Protocol    string `json:"protocol"`
}

// ForwardWithTunnel 转发列表（连隧道信息）
type ForwardWithTunnel struct {
	ID               int64                   `json:"id"`
	UserID           int64                   `json:"userId"`
	Name             string                  `json:"name"`
	TunnelID         int64                   `json:"tunnelId"`
	InPort           int                     `json:"inPort"`
	OutPort          *int                    `json:"outPort"`
	RemoteAddr       string                  `json:"remoteAddr"`
	Status           int                     `json:"status"`
	CreatedTime      int64                   `json:"createdTime"`
	UpdatedTime      int64                   `json:"updatedTime"`
	UserName         string                  `json:"userName"`
	InFlow           int64                   `json:"inFlow"`
	OutFlow          int64                   `json:"outFlow"`
	Strategy         string                  `json:"strategy"`
	TargetMode       string                  `json:"targetMode"`
	ActiveRemoteAddr string                  `json:"activeRemoteAddr"`
	ExitMode         string                  `json:"exitMode"`
	ExitStrategy     string                  `json:"exitStrategy"`
	ExitMembers      []ForwardExitMemberView `json:"exitMembers" gorm:"-"`
	Inx              int                     `json:"inx"`
	InterfaceName    *string                 `json:"interfaceName"`
	TunnelName       *string                 `json:"tunnelName"`
	InIP             *string                 `json:"inIp"`
	OutIP            *string                 `json:"outIp"`
	Type             *int                    `json:"type"`
	Protocol         *string                 `json:"protocol"`
}

// ForwardExitMemberView 转发出口候选节点响应。
type ForwardExitMemberView struct {
	ID          int64   `json:"id"`
	ForwardID   int64   `json:"forwardId"`
	OutNodeID   int64   `json:"outNodeId"`
	OutNodeName *string `json:"outNodeName"`
	OutNodeIP   *string `json:"outNodeIp"`
	RelayPort   int     `json:"relayPort"`
	OutPort     int     `json:"outPort"`
	Weight      int     `json:"weight"`
	Status      int     `json:"status"`
	Active      bool    `json:"active"`
}

// UserTunnelWithDetail 用户隧道权限详情（连表）
type UserTunnelWithDetail struct {
	ID             int64   `json:"id"`
	UserID         int64   `json:"userId"`
	TunnelID       int64   `json:"tunnelId"`
	Flow           int64   `json:"flow"`
	InFlow         int64   `json:"inFlow"`
	OutFlow        int64   `json:"outFlow"`
	Num            int     `json:"num"`
	FlowResetTime  int64   `json:"flowResetTime"`
	ExpTime        *int64  `json:"expTime"`
	SpeedID        *int64  `json:"speedId"`
	Status         int     `json:"status"`
	TunnelName     *string `json:"tunnelName"`
	TunnelFlow     *int    `json:"tunnelFlow"`
	InIP           *string `json:"inIp"`
	OutIP          *string `json:"outIp"`
	Type           *int    `json:"type"`
	Protocol       *string `json:"protocol"`
	SpeedLimitName *string `json:"speedLimitName"`
	Speed          *int    `json:"speed"`
}

// UserTunnelDetail 套餐页隧道权限
type UserTunnelDetail struct {
	ID             int64   `json:"id"`
	UserID         int64   `json:"userId"`
	TunnelID       int64   `json:"tunnelId"`
	TunnelName     *string `json:"tunnelName"`
	TunnelFlow     *int    `json:"tunnelFlow"`
	Flow           int64   `json:"flow"`
	InFlow         int64   `json:"inFlow"`
	OutFlow        int64   `json:"outFlow"`
	Num            int     `json:"num"`
	FlowResetTime  *int64  `json:"flowResetTime"`
	ExpTime        *int64  `json:"expTime"`
	SpeedID        *int64  `json:"speedId"`
	SpeedLimitName *string `json:"speedLimitName"`
	Speed          *int    `json:"speed"`
}

// UserForwardDetail 套餐页转发详情
type UserForwardDetail struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	TunnelID    int64   `json:"tunnelId"`
	TunnelName  *string `json:"tunnelName"`
	InIP        *string `json:"inIp"`
	InPort      int     `json:"inPort"`
	RemoteAddr  string  `json:"remoteAddr"`
	InFlow      int64   `json:"inFlow"`
	OutFlow     int64   `json:"outFlow"`
	Status      int     `json:"status"`
	CreatedTime int64   `json:"createdTime"`
}

// TrafficTunnelOption 控制台流量趋势可选隧道。
// 节点名称用于展示“入口 → 出口”，避免同名或含义不清的隧道难以辨认。
type TrafficTunnelOption struct {
	TunnelID      int64  `json:"tunnelId"`
	TunnelName    string `json:"tunnelName"`
	Type          int    `json:"type"`
	InNodeID      int64  `json:"inNodeId"`
	InNodeName    string `json:"inNodeName"`
	RelayNodeID   *int64 `json:"relayNodeId,omitempty"`
	RelayNodeName string `json:"relayNodeName,omitempty"`
	OutNodeID     int64  `json:"outNodeId"`
	OutNodeName   string `json:"outNodeName"`
}

// TunnelListItem 用户可用隧道（创建转发时下拉）
type TunnelListItem struct {
	ID            int64   `json:"id"`
	Name          string  `json:"name"`
	IP            string  `json:"ip"`
	InNodePortSta *int    `json:"inNodePortSta"`
	InNodePortEnd *int    `json:"inNodePortEnd"`
	Type          int     `json:"type"`
	Protocol      *string `json:"protocol"`
}
