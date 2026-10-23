import { apiPost, type RequestOptions } from "./client";
import type { Node, NodeWrite, NodeStatusCheck } from "@/lib/types";
export const createNode = (data: NodeWrite, opts?: RequestOptions): Promise<string> =>
  apiPost("/node/create", data, opts);
export const updateNode = (data: NodeWrite, opts?: RequestOptions): Promise<string> =>
  apiPost("/node/update", data, opts);
export const getNodeList = async (opts?: RequestOptions): Promise<Node[]> => {
  const data = await apiPost<Node[] | { records: Node[] }>("/node/list", {}, opts);
  return Array.isArray(data) ? data : (data?.records ?? []);
};
export const deleteNode = (id: number, opts?: RequestOptions): Promise<string> =>
  apiPost("/node/delete", { id }, opts);
export const getNodeInstallCommand = (
  id: number,
  forwardMode?: string,
  opts?: RequestOptions,
): Promise<string> => apiPost("/node/install", { id, forwardMode }, opts);
export const getNodeUninstallCommand = (
  id: number,
  forwardMode?: string,
  opts?: RequestOptions,
): Promise<string> => apiPost("/node/uninstall", { id, forwardMode }, opts);
// 后端等待节点升级最多 90 秒，额外预留 10 秒用于传输与解析。
const nodeUpgradeRequestTimeoutMs = 100_000;
export const upgradeNode = (
  id: number,
  opts?: RequestOptions,
): Promise<{ nodeId: number; latestVersion: string; message: string }> =>
  apiPost("/node/upgrade", { id }, { ...opts, timeoutMs: nodeUpgradeRequestTimeoutMs });
export const checkNodeStatus = (nodeId: number, opts?: RequestOptions): Promise<NodeStatusCheck> =>
  apiPost("/node/check-status", { nodeId }, opts);
