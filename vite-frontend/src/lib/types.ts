// Types ported from the original Go-backed go-panel frontend.
// Keep field names identical to the backend response shape.

export interface ApiResponse<T = unknown> {
  code: number;
  msg: string;
  data: T;
}

export interface BuildInfo {
  version: string;
  commit: string;
  buildTime: string;
  sourceUrl: string;
}

export interface PanelUpdateStatus {
  current: BuildInfo;
  enabled: boolean;
  checkedAt?: number;
  latestVersion?: string;
  updateAvailable: boolean;
  releaseName?: string;
  releaseUrl?: string;
  releaseNotes?: string;
  publishedAt?: string;
  autoUpdateConfigured: boolean;
}

export interface PanelUpdateTriggerResult {
  targetVersion: string;
  backupFile: string;
  started: boolean;
}

// Cloudflare R2 自动备份设置的脱敏视图；后端永不返回 Secret Access Key。
export interface R2BackupSettings {
  enabled: boolean;
  accountId: string;
  accessKeyId: string;
  bucket: string;
  objectPrefix: string;
  scheduleTime: string;
  retentionCount: number;
  secretConfigured: boolean;
  secretUsable: boolean;
  credentialEncryptionAvailable: boolean;
  credentialMessage?: string;
  lastAttemptAt: number;
  lastSuccessAt: number;
  lastObjectKey?: string;
  lastError?: string;
  lastSize: number;
  lastSha256?: string;
}

// secretAccessKey 为空表示保留旧密钥；clearSecret 仅在关闭自动备份时清除密钥。
export interface R2BackupSettingsUpdate {
  enabled: boolean;
  accountId: string;
  accessKeyId: string;
  secretAccessKey: string;
  clearSecret: boolean;
  bucket: string;
  objectPrefix: string;
  scheduleTime: string;
  retentionCount: number;
}

export interface R2BackupRunResult {
  objectKey: string;
  size: number;
  sha256: string;
  deletedObjects: number;
  completedAt: number;
}

export interface User {
  id: number;
  name?: string;
  user: string;
  pwd?: string;
  status: number; // 1-正常, 0-禁用
  flow: number; // 流量限制(GB)
  num: number; // 转发数量
  expTime?: number | null; // 过期时间戳
  flowResetTime?: number; // 流量重置日期(1-31号)
  createdTime?: number;
  updatedTime?: number | null;
  mustChangePwd?: number;
  inFlow?: number;
  outFlow?: number;
  roleId?: number;
}

export interface UserForm {
  id?: number;
  name?: string;
  user: string;
  pwd?: string;
  status: number;
  flow: number;
  num: number;
  expTime: Date | null;
  flowResetTime: number;
}

export interface UserTunnel {
  id: number;
  userId: number;
  tunnelId: number;
  tunnelName: string | null;
  status: number;
  flow: number;
  num: number;
  expTime: number | null;
  flowResetTime: number;
  speedId?: number | null;
  speedLimitName?: string | null;
  inFlow?: number;
  outFlow?: number;
  tunnelFlow?: number | null;
}

export interface UserTunnelForm {
  tunnelId: number | null;
  flow: number;
  num: number;
  expTime: Date | null;
  flowResetTime: number;
  speedId: number | null;
}

// ---------- Tunnel ----------
export interface Tunnel {
  id: number;
  name: string;
  type: number; // 1: 端口转发, 2: 隧道转发
  inNodeId: number;
  relayNodeId?: number;
  outNodeId?: number;
  entryNodeId?: number;
  exitNodeId?: number;
  entryNodeName?: string;
  exitNodeName?: string;
  inIp?: string;
  relayIp?: string | null;
  outIp?: string;
  protocol?: string | null;
  tcpListenAddr?: string;
  udpListenAddr?: string;
  interfaceName?: string | null;
  flow?: number; // 1: 单向, 2: 双向
  trafficRatio?: number;
  status?: number;
  createdTime?: number;
  updatedTime?: number;
}

// 普通用户选项仅包含授权与表单能力，不包含节点标识、出口地址或凭据。
export interface TunnelListItem {
  id: number;
  name: string;
  type: number;
  ip: string;
  protocol?: string | null;
  inNodePortSta: number | null;
  inNodePortEnd: number | null;
  hasRelay: boolean;
  inNodeForwardMode: string;
  available: boolean;
  unavailableReason?: string;
}

export interface UserTunnelOption extends Omit<TunnelListItem, "ip" | "protocol"> {
  inIp: string;
}

export type ForwardTunnelOption = Tunnel | UserTunnelOption;

export interface TunnelForm {
  id?: number;
  name: string;
  type: number;
  inNodeId: number | null;
  relayNodeId?: number | null;
  outNodeId?: number | null;
  protocol: string;
  tcpListenAddr: string;
  udpListenAddr: string;
  interfaceName?: string;
  flow: number;
  trafficRatio: number;
  status: number;
}

export interface DiagnosisResult {
  tunnelName?: string;
  forwardName?: string;
  forwardId?: number;
  tunnelId?: number;
  tunnelType: string;
  timestamp: number;
  results: Array<{
    success: boolean;
    description: string;
    nodeName: string;
    nodeId: number;
    targetIp: string;
    targetPort?: number;
    message?: string;
    averageTime?: number;
    packetLoss?: number;
    advisory?: boolean;
    timestamp?: number;
  }>;
}

// ---------- Node ----------
export interface NodeSystemInfo {
  cpuUsage: number;
  memoryUsage: number;
  uploadTraffic: number;
  downloadTraffic: number;
  uploadSpeed: number;
  downloadSpeed: number;
  uptime: number;
}

export type NodeSyncState = "synced" | "pending" | "failed" | "unknown";

export interface NodeStatusCheck {
  nodeId: number;
  name: string;
  forwardMode: string;
  dbStatus: number;
  connected: boolean;
  responsive: boolean;
  roundTripMs: number;
  version: string;
  latestVersion: string;
  upgradeAvailable: boolean;
  syncState: NodeSyncState;
  syncLastError?: string;
  syncLastAttempt: number;
  checkedAt: number;
  message?: string;
}

export interface Node {
  id: number;
  name: string;
  status: number; // 1: 在线, 0: 离线
  ip?: string; // 入口 IP 列表，逗号分隔
  serverIp?: string;
  serverIpv6?: string;
  portSta?: number;
  portEnd?: number;
  portRange?: string;
  version?: string | null;
  createdTime?: number;
  updatedTime?: number | null;
  latestVersion?: string;
  upgradeAvailable?: boolean;
  syncState?: NodeSyncState;
  syncLastError?: string;
  forwardMode?: "gost" | "nftables" | string;
  http?: number;
  tls?: number;
  socks?: number;
  // 运行时（前端注入，不来自 REST 接口）
  connectionStatus?: "online" | "offline";
  systemInfo?: NodeSystemInfo | null;
  // 兼容旧字段
  cpu?: number;
  mem?: number;
  disk?: number;
  netIn?: number;
  netOut?: number;
  uptime?: string;
  region?: string;
}

// ---------- Forward ----------
export type ForwardExitMode = "single" | "manual" | "balance";
export type ForwardTargetMode = "balance" | "manual";

export interface ForwardExitMember {
  id?: number;
  forwardId?: number;
  outNodeId: number;
  outNodeName?: string | null;
  outNodeIp?: string | null;
  relayPort?: number;
  outPort?: number;
  weight?: number;
  status?: number;
  active: boolean;
}

export interface Forward {
  id: number;
  userId?: number;
  userName?: string;
  tunnelId: number;
  tunnelName?: string | null;
  name: string;
  inIp?: string | null;
  inPort: number;
  outPort?: number | null;
  remoteAddr: string;
  strategy?: string;
  targetMode?: ForwardTargetMode | string;
  activeRemoteAddr?: string;
  exitMode?: ForwardExitMode | string;
  exitStrategy?: string;
  exitMembers?: ForwardExitMember[];
  status: number; // 1 running, 0 stopped, other = error
  inFlow?: number;
  outFlow?: number;
  createdTime?: number;
  updatedTime?: number;
  inx?: number;
  serviceState?: string;
  protocol?: string | null;
}

// ---------- Speed Limit ----------
export interface SpeedLimit {
  id: number;
  name: string;
  tunnelId: number;
  tunnelName?: string;
  speed: number;
  status?: number;
  createdTime?: number;
  updatedTime?: number | null;
}

// ---------- Subscription ----------
export type SubscriptionFormat = "surge" | "clash" | "singbox" | "v2rayn";
export type RelayMode = "replace" | "append";

export interface SubscriptionProfile {
  id: number;
  name: string;
  token: string;
  defaultFormat: SubscriptionFormat;
  description?: string;
  surgeTemplate: string;
  clashTemplate: string;
  singboxTemplate: string;
  status: number;
}

export interface ProfileNode {
  subscriptionId: number;
  proxyNodeId: number;
  sort: number;
}

export interface ProxyNode {
  id: number;
  externalId?: string;
  name: string;
  protocol: string;
  server: string;
  port: number;
  uuid?: string;
  username?: string;
  password?: string;
  method?: string;
  sni?: string;
  network?: string;
  security?: string;
  path?: string;
  flow?: string;
  publicKey?: string;
  shortId?: string;
  fingerprint?: string;
  snellVersion?: number;
  allowInsecure: number;
  udp: number;
  link?: string;
  options?: string;
  forwardId?: number | null;
  sourceProxyNodeId?: number | null;
  relayMode?: RelayMode | "";
  sort: number;
  status: number;
  lastReportTime: number;
  resolvedServer?: string;
  resolvedPort?: number;
  resolvedAddress?: string;
  profileIds?: number[];
  provider?: string;
  region?: string;
  protocolLabel?: string;
  sourceNodeName?: string;
  relayChildCount?: number;
  forwardName?: string;
  forwardTunnelId?: number;
  forwardTunnel?: string;
  forwardTunnelType?: number;
  forwardInIp?: string;
  forwardInPort?: number;
  forwardOutIp?: string;
  forwardOutPort?: number;
  forwardTarget?: string;
}

export interface SubTunnelOption {
  id: number;
  name: string;
  inIp: string;
  type: number;
  protocol?: string;
  status: number;
}

// 恢复列表只返回主机元数据和鉴权命令；服务端私钥、证书不进入设置页响应。
export interface SubscriptionNodeBackup {
  sourceHost: string;
  server: string;
  protocols: string[];
  updatedTime: number;
  restoreCommand: string;
}

export interface SubscriptionSettings {
  apiKey: string;
  profiles: SubscriptionProfile[];
  nodes: ProxyNode[];
  profileNodes: ProfileNode[];
  tunnels: SubTunnelOption[];
  vlessBootstrapScript: string;
  // 旧面板未返回此字段时按无备份处理，避免误把客户端订阅当作完整服务端备份。
  nodeBackups?: SubscriptionNodeBackup[];
}

// ---------- Pagination ----------
export interface Pagination {
  current: number;
  size: number;
  total: number;
}

// API 写入字段按后端 DTO 定义，表单中的 Date 在提交前转换为毫秒时间戳。
export interface PasswordUpdate {
  newUsername: string;
  currentPassword: string;
  newPassword: string;
  confirmPassword: string;
}
export type UserWrite = Omit<UserForm, "expTime"> & { expTime: number };
export type NodeWrite = Required<
  Pick<Node, "name" | "ip" | "serverIp" | "portSta" | "portEnd" | "forwardMode">
> &
  Pick<Node, "http" | "tls" | "socks"> & { id?: number };
export type TunnelWrite = Omit<TunnelForm, "inNodeId"> & { inNodeId: number };
export type ForwardWrite = Pick<Forward, "name" | "tunnelId" | "remoteAddr"> &
  Partial<
    Pick<
      Forward,
      | "id"
      | "userId"
      | "strategy"
      | "targetMode"
      | "activeRemoteAddr"
      | "exitMode"
      | "exitStrategy"
      | "exitMembers"
    >
  > & { inPort?: number | null; forceSwitchTarget?: boolean; interfaceName?: string | null };
export interface UserTunnelAssign {
  userId: number;
  tunnelId: number;
  flow: number;
  num: number;
  expTime: number;
  flowResetTime?: number;
  speedId?: number | null;
}
export type UserTunnelWrite = Pick<UserTunnel, "id" | "flow" | "num" | "status"> &
  Partial<Pick<UserTunnel, "expTime" | "flowResetTime" | "speedId">>;
export type SpeedLimitWrite = Pick<SpeedLimit, "name" | "tunnelId" | "speed"> & {
  id?: number;
  tunnelName: string;
};
export type ProxyNodeWrite = Omit<Partial<ProxyNode>, "allowInsecure" | "udp"> &
  Pick<ProxyNode, "id" | "name" | "protocol" | "server" | "port"> & {
    allowInsecure?: boolean;
    udp?: boolean;
  };
export type SubscriptionProfileWrite = Partial<SubscriptionProfile> & {
  name: string;
  nodeIds?: number[];
};
export interface RelayWrite {
  nodeId: number;
  tunnelId: number;
  inPort?: number | null;
  mode?: RelayMode;
  name?: string;
  strategy?: string;
  interfaceName?: string | null;
}
export interface RelayEndpoint {
  nodeId?: number;
  nodeName?: string;
  address?: string;
  ip?: string;
  port?: number;
  portText?: string;
}
export interface RelayPreview {
  nodeId: number;
  nodeName: string;
  tunnelId: number;
  tunnelName: string;
  tunnelType: number;
  tunnelTypeName: string;
  protocol?: string;
  entry: RelayEndpoint;
  relay?: RelayEndpoint;
  exit: RelayEndpoint;
  target: RelayEndpoint;
  subscriptionAddress?: string;
  forwardId?: number;
  forwardName?: string;
}
export interface TrafficTotals {
  inFlow: number;
  outFlow: number;
  totalFlow: number;
}
export interface TrafficTunnel {
  tunnelId: number;
  tunnelName: string;
  type: number;
  inNodeId: number;
  inNodeName: string;
  relayNodeId?: number;
  relayNodeName?: string;
  outNodeId: number;
  outNodeName: string;
}
export interface TrafficPoint {
  id: number;
  userId: number;
  flow: number;
  totalFlow: number;
  totalInFlow: number;
  totalOutFlow: number;
  createdTime: number;
  time: string;
  inFlow: number;
  outFlow: number;
}
export type PackagePermission = Omit<
  UserTunnel,
  "status" | "tunnelName" | "flowResetTime" | "expTime" | "speedLimitName" | "tunnelFlow"
> & {
  tunnelName: string | null;
  tunnelFlow: number | null;
  flowResetTime: number | null;
  expTime: number | null;
  speedLimitName: string | null;
  speed: number | null;
};
export type PackageForward = Pick<
  Forward,
  "id" | "name" | "tunnelId" | "inPort" | "remoteAddr" | "inFlow" | "outFlow" | "status"
> & { tunnelName: string | null; inIp: string | null; createdTime: number };
export interface UserPackage {
  userInfo: Omit<User, "name" | "pwd" | "roleId" | "mustChangePwd"> & {
    name: null;
    updatedTime: number | null;
  };
  tunnelPermissions: PackagePermission[];
  forwards: PackageForward[];
  trafficRange: string;
  trafficScope: "system" | "user";
  trafficTunnelId: number | null;
  trafficTunnel: TrafficTunnel | null;
  trafficTunnels: TrafficTunnel[];
  trafficTotals: TrafficTotals;
  todayTraffic: TrafficTotals;
  trafficTrendTotals: TrafficTotals;
  statisticsFlows: TrafficPoint[];
}
export interface SystemStatus {
  startedAt: number;
  uptimeSeconds: number;
}
export interface SpeedTestRequest {
  tunnelId: number;
  testId?: string;
  direction?: string;
  durationSeconds?: number;
  parallel?: number;
  port?: number;
}
export interface SpeedTestResult {
  tunnelId: number;
  testId: string;
  tunnelName: string;
  direction: string;
  sourceNodeId: number;
  sourceNodeName: string;
  targetNodeId: number;
  targetNodeName: string;
  targetHost: string;
  port: number;
  durationSeconds: number;
  parallel: number;
  summary: {
    sentMbps: number;
    receivedMbps: number;
    sentBytes: number;
    receivedBytes: number;
    seconds: number;
    retransmits: number;
    latencyMs?: number;
    lossPercent?: number;
    pingSamples?: number;
  };
  raw?: unknown;
  timestamp: number;
}
export interface DetectedForwardRule {
  inPort: number;
  outPort: number;
  targetHost: string;
  protocol: string;
  tunnelId: number;
  tunnelName: string;
  comment: string;
  rawRule: string;
  suggested: boolean;
}
export interface DetectedTunnelRule extends Omit<DetectedForwardRule, "outPort" | "rawRule"> {
  inNodeName: string;
  outNodeName: string;
  relayPort: number;
  targetPort: number;
  inRawRule: string;
  outRawRule: string;
}
export interface NftDetection {
  detected: DetectedForwardRule[];
  total: number;
  nodeName: string;
  nodeId: number;
}
export interface TunnelDetection {
  detected: DetectedTunnelRule[];
  total: number;
  inNodeId: number;
  outNodeId: number;
  inNodeName: string;
  outNodeName: string;
}
export interface CompleteForwardRule {
  tunnelId: number;
  inPort: number;
  outPort?: number | null;
  targetPort?: number | null;
  targetHost: string;
  protocol: string;
  name: string;
  rawRule: string;
  outRawRule?: string;
}
export interface NftCompletion {
  created: number;
  failed: number;
  details: Array<{ inPort: number; success: boolean; forwardId?: number; error?: string }>;
}
export interface ExtraRule {
  inPort: number;
  targetHost: string;
  outPort: number;
  protocol: string;
  ruleType: string;
  rawRule: string;
  suggestedTunnelId: number;
  suggestedTunnelName: string;
  suggestedName: string;
}
export interface ExtraRuleAction {
  nodeId: number;
  inPort: number;
  action: "keep" | "delete";
  name: string;
  tunnelId: number;
  targetHost: string;
  outPort: number;
  protocol: string;
  rawRule: string;
}
export interface ExtraRulesDetection {
  nodes: Array<{
    nodeId: number;
    nodeName: string;
    nodeIp: string;
    extraRules: ExtraRule[];
    total: number;
  }>;
  totalNodes: number;
  totalRules: number;
}
export interface ExtraRulesHandled {
  kept: number;
  deleted: number;
  details: Array<{
    nodeId: number;
    inPort: number;
    action: string;
    success: boolean;
    forwardId?: number;
    error?: string;
  }>;
}
