import type {
  BuildInfo,
  Forward,
  Node,
  PanelUpdateStatus,
  PanelUpdateTriggerResult,
  R2BackupRunResult,
  R2BackupSettings,
  R2BackupSettingsUpdate,
  SpeedLimit,
  SubscriptionProfile,
  TunnelForm,
  UserForm,
  UserTunnel,
  UserTunnelForm,
} from "@/lib/types";
import Network from "./network";

// 节点会同步等待最多 90 秒的升级结果；额外预留 10 秒给 HTTP 往返与响应解析。
const nodeUpgradeRequestTimeoutMs = 100_000;

// ---------- Auth ----------
export interface LoginData {
  username: string;
  password: string;
}
export interface LoginResponse {
  token: string;
  role_id: number;
  name: string;
  requirePasswordChange?: boolean;
}
export const login = (data: LoginData) => Network.post<LoginResponse>("/user/login", data);
export const updatePassword = (data: Record<string, unknown>) =>
  Network.post("/user/updatePassword", data);

// ---------- Users ----------
export const createUser = (data: UserForm | Record<string, unknown>) =>
  Network.post("/user/create", data);
export const getAllUsers = (pageData: Record<string, unknown> = {}) =>
  Network.post("/user/list", pageData);
export const updateUser = (data: UserForm | Record<string, unknown>) =>
  Network.post("/user/update", data);
export const deleteUser = (id: number) => Network.post("/user/delete", { id });
export const getUserPackageInfo = (data: { range?: string; tunnelId?: number } = {}) =>
  Network.post("/user/package", data);
export const resetUserFlow = (data: { id: number; type: number }) =>
  Network.post("/user/reset", data);

// ---------- Nodes ----------
export const createNode = (data: Partial<Node> | Record<string, unknown>) =>
  Network.post("/node/create", data);
export const getNodeList = () => Network.post("/node/list");
export const updateNode = (data: Partial<Node> | Record<string, unknown>) =>
  Network.post("/node/update", data);
export const deleteNode = (id: number) => Network.post("/node/delete", { id });
export const getNodeInstallCommand = (id: number, forwardMode?: string) =>
  Network.post("/node/install", { id, forwardMode });
export const getNodeUninstallCommand = (id: number, forwardMode?: string) =>
  Network.post("/node/uninstall", { id, forwardMode });
export const upgradeNode = (id: number) =>
  Network.postWithTimeout("/node/upgrade", { id }, nodeUpgradeRequestTimeoutMs);
export const checkNodeStatus = (nodeId?: number) =>
  Network.post("/node/check-status", nodeId ? { nodeId } : {});

// ---------- Tunnels ----------
export const createTunnel = (data: TunnelForm | Record<string, unknown>) =>
  Network.post("/tunnel/create", data);
export const getTunnelList = () => Network.post("/tunnel/list");
export const getTunnelById = (id: number) => Network.post("/tunnel/get", { id });
export const updateTunnel = (data: TunnelForm | Record<string, unknown>) =>
  Network.post("/tunnel/update", data);
export const deleteTunnel = (id: number) => Network.post("/tunnel/delete", { id });
export const diagnoseTunnel = (tunnelId: number) => Network.post("/tunnel/diagnose", { tunnelId });
export const speedTestTunnel = (data: {
  tunnelId: number;
  testId?: string;
  direction?: string;
  durationSeconds?: number;
  parallel?: number;
  port?: number;
}) =>
  Network.postWithTimeout("/tunnel/speed-test", data, ((data.durationSeconds || 10) + 30) * 1000);

// ---------- User↔Tunnel ----------
export const assignUserTunnel = (data: Record<string, unknown>) =>
  Network.post("/tunnel/user/assign", data);
export const getUserTunnelList = (queryData: Record<string, unknown> = {}) =>
  Network.post<UserTunnel[] | { records?: UserTunnel[] }>("/tunnel/user/list", queryData);
export const removeUserTunnel = (params: Record<string, unknown>) =>
  Network.post("/tunnel/user/remove", params);
export const updateUserTunnel = (data: UserTunnelForm | Record<string, unknown>) =>
  Network.post("/tunnel/user/update", data);
export const userTunnel = () => Network.post("/tunnel/user/tunnel");

// ---------- Forwards ----------
export const createForward = (data: Partial<Forward> | Record<string, unknown>) =>
  Network.post("/forward/create", data);
export const getForwardList = () => Network.post("/forward/list");
export const updateForward = (data: Partial<Forward> | Record<string, unknown>) =>
  Network.post("/forward/update", data);
export const deleteForward = (id: number) => Network.post("/forward/delete", { id });
export const forceDeleteForward = (id: number) => Network.post("/forward/force-delete", { id });
export const pauseForwardService = (id: number) => Network.post("/forward/pause", { id });
export const resumeForwardService = (id: number) => Network.post("/forward/resume", { id });
export const diagnoseForward = (forwardId: number) =>
  Network.post("/forward/diagnose", { forwardId });
export const updateForwardOrder = (data: { forwards: Array<{ id: number; inx: number }> }) =>
  Network.post("/forward/update-order", data);
export const detectNftRules = (nodeId: number) =>
  Network.post("/forward/detect-nft-rules", { nodeId });
export const detectTunnelRules = (inNodeId: number, outNodeId: number) =>
  Network.post("/forward/detect-tunnel-rules", { inNodeId, outNodeId });
export const completeFromNft = (nodeId: number, rules: unknown[]) =>
  Network.post("/forward/complete-from-nft", { nodeId, rules });

// ---------- Speed Limits ----------
export const createSpeedLimit = (data: Partial<SpeedLimit> | Record<string, unknown>) =>
  Network.post("/speed-limit/create", data);
export const getSpeedLimitList = () => Network.post("/speed-limit/list");
export const updateSpeedLimit = (data: Partial<SpeedLimit> | Record<string, unknown>) =>
  Network.post("/speed-limit/update", data);
export const deleteSpeedLimit = (id: number) => Network.post("/speed-limit/delete", { id });

// ---------- Config ----------
export const getConfigs = () => Network.post("/config/list");
export const getSystemStatus = () => Network.post("/system/status");
export const getSystemVersion = () => Network.post<BuildInfo>("/system/version");
export const checkPanelUpdate = () =>
  Network.postWithTimeout<PanelUpdateStatus>("/system/update/check", {}, 15_000);
export const applyPanelUpdate = () =>
  Network.postWithTimeout<PanelUpdateTriggerResult>("/system/update/apply", {}, 30_000);
export const getConfigByName = (name: string) => Network.post("/config/get", { name });
export const updateConfigs = (configMap: Record<string, string>) =>
  Network.post("/config/update", configMap);
export const updateConfig = (name: string, value: string) =>
  Network.post("/config/update-single", { name, value });

// ---------- Backup ----------
export const downloadSiteBackup = () => Network.download("/backup/download");
export const restoreSiteBackup = (file: File) => {
  const fd = new FormData();
  fd.append("file", file);
  return Network.upload("/backup/restore", fd);
};
export const detectExtraRules = () => Network.post("/backup/detect-extra-rules", {});
export const handleExtraRules = (rules: unknown[]) =>
  Network.post("/backup/handle-extra-rules", { rules });
export const getR2BackupSettings = () => Network.post<R2BackupSettings>("/backup/r2/settings");
export const updateR2BackupSettings = (data: R2BackupSettingsUpdate) =>
  Network.post<R2BackupSettings>("/backup/r2/update", data);
export const testR2BackupConnection = () =>
  Network.postWithTimeout<string>("/backup/r2/test", {}, 30000);
export const runR2BackupNow = () =>
  Network.postWithTimeout<R2BackupRunResult>("/backup/r2/run", {}, 60000);

// ---------- Subscription ----------
export const getSubscriptionSettings = () => Network.post("/sub/settings");
export const updateSubscriptionApiKey = (apiKey: string) =>
  Network.post("/sub/api-key", { apiKey });
export const getProxyNodeList = () => Network.post("/sub/node/list");
export const updateProxyNode = (data: Record<string, unknown>) =>
  Network.post("/sub/node/update", data);
export const deleteProxyNode = (id: number, deleteForward = false) =>
  Network.post("/sub/node/delete", { id, deleteForward });
export const assignProxyNodeProfiles = (nodeId: number, profileIds: number[]) =>
  Network.post("/sub/node/assign-profiles", { nodeId, profileIds });
export const createProxyNodeRelay = (data: Record<string, unknown>) =>
  Network.post("/sub/node/relay", data);
export const previewProxyNodeRelay = (data: Record<string, unknown>) =>
  Network.post("/sub/node/relay/preview", data);
export const closeProxyNodeRelay = (nodeId: number) =>
  Network.post("/sub/node/relay/close", { nodeId });
export const importProxyNodeLink = (link: string) => Network.post("/sub/node/import", { link });
export const createSubscriptionProfile = (
  data: Partial<SubscriptionProfile> | Record<string, unknown>,
) => Network.post("/sub/profile/create", data);
export const updateSubscriptionProfile = (
  data: Partial<SubscriptionProfile> | Record<string, unknown>,
) => Network.post("/sub/profile/update", data);
export const deleteSubscriptionProfile = (id: number) =>
  Network.post("/sub/profile/delete", { id });
export const regenerateSubscriptionToken = (id: number) =>
  Network.post("/sub/profile/token", { id });
export const previewSubscription = (token: string, format: string) =>
  Network.post("/sub/preview", { token, format });
