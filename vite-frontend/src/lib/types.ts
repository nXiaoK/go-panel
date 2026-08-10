// Types ported from the original Go-backed Flux Panel frontend.
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
  expTime?: number; // 过期时间戳
  flowResetTime?: number; // 流量重置日期(1-31号)
  createdTime?: number;
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
  tunnelName: string;
  status: number;
  flow: number;
  num: number;
  expTime: number;
  flowResetTime: number;
  speedId?: number | null;
  speedLimitName?: string;
  inFlow?: number;
  outFlow?: number;
  tunnelFlow?: number;
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
  outNodeId?: number;
  entryNodeId?: number;
  exitNodeId?: number;
  entryNodeName?: string;
  exitNodeName?: string;
  inIp?: string;
  outIp?: string;
  protocol?: string;
  tcpListenAddr?: string;
  udpListenAddr?: string;
  interfaceName?: string;
  flow?: number; // 1: 单向, 2: 双向
  trafficRatio?: number;
  status?: number;
  createdTime?: string;
}

export interface TunnelForm {
  id?: number;
  name: string;
  type: number;
  inNodeId: number | null;
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
  tunnelName: string;
  tunnelType: string;
  timestamp: number;
  results: Array<{
    success: boolean;
    description: string;
    nodeName: string;
    nodeId: string;
    targetIp: string;
    targetPort?: number;
    message?: string;
    averageTime?: number;
    packetLoss?: number;
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
  version?: string;
  latestVersion?: string;
  upgradeAvailable?: boolean;
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
  outNodeName?: string;
  outNodeIp?: string;
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
  tunnelName?: string;
  name: string;
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
  createdTime?: string;
  inx?: number;
  serviceState?: string;
  protocol?: string;
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
  updatedTime?: number;
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

export interface SubscriptionSettings {
  apiKey: string;
  profiles: SubscriptionProfile[];
  nodes: ProxyNode[];
  profileNodes: ProfileNode[];
  tunnels: SubTunnelOption[];
  vlessBootstrapScript: string;
}

// ---------- Pagination ----------
export interface Pagination {
  current: number;
  size: number;
  total: number;
}
