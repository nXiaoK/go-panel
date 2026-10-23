import { apiPost, type RequestOptions } from "@/lib/api/client";
import type {
  Forward,
  ProxyNode,
  ProxyNodeWrite,
  RelayPreview,
  RelayWrite,
  SubscriptionProfile,
  SubscriptionProfileWrite,
  SubscriptionSettings,
} from "@/lib/types";
export const getSubscriptionSettings = (opts?: RequestOptions): Promise<SubscriptionSettings> =>
  apiPost("/sub/settings", {}, opts);
export const updateSubscriptionApiKey = (apiKey: string, opts?: RequestOptions): Promise<string> =>
  apiPost("/sub/api-key", { apiKey }, opts);
export const getProxyNodeList = async (opts?: RequestOptions): Promise<ProxyNode[]> => {
  const data = await apiPost<ProxyNode[] | { records: ProxyNode[] }>("/sub/node/list", {}, opts);
  return Array.isArray(data) ? data : (data?.records ?? []);
};
export const updateProxyNode = (data: ProxyNodeWrite, opts?: RequestOptions): Promise<string> =>
  apiPost("/sub/node/update", data, opts);
export const deleteProxyNode = (
  id: number,
  deleteForward = false,
  opts?: RequestOptions,
): Promise<string | null> => apiPost("/sub/node/delete", { id, deleteForward }, opts);
export const assignProxyNodeProfiles = (
  nodeId: number,
  profileIds: number[],
  opts?: RequestOptions,
): Promise<string> => apiPost("/sub/node/assign-profiles", { nodeId, profileIds }, opts);
export const createProxyNodeRelay = (
  data: RelayWrite,
  opts?: RequestOptions,
): Promise<{ node: ProxyNode; forward: Forward }> => apiPost("/sub/node/relay", data, opts);
export const previewProxyNodeRelay = (
  data: Pick<RelayWrite, "nodeId" | "tunnelId" | "inPort">,
  opts?: RequestOptions,
): Promise<RelayPreview> => apiPost("/sub/node/relay/preview", data, opts);
export const closeProxyNodeRelay = (
  nodeId: number,
  opts?: RequestOptions,
): Promise<{ deletedForwards: number; deletedNodes: number }> =>
  apiPost("/sub/node/relay/close", { nodeId }, opts);
export const importProxyNodeLink = (link: string, opts?: RequestOptions): Promise<ProxyNode> =>
  apiPost("/sub/node/import", { link }, opts);
export const createSubscriptionProfile = (
  data: SubscriptionProfileWrite,
  opts?: RequestOptions,
): Promise<SubscriptionProfile> => apiPost("/sub/profile/create", data, opts);
export const updateSubscriptionProfile = (
  data: SubscriptionProfileWrite & { id: number },
  opts?: RequestOptions,
): Promise<SubscriptionProfile> => apiPost("/sub/profile/update", data, opts);
export const deleteSubscriptionProfile = (id: number, opts?: RequestOptions): Promise<string> =>
  apiPost("/sub/profile/delete", { id }, opts);
export const regenerateSubscriptionToken = (
  id: number,
  opts?: RequestOptions,
): Promise<SubscriptionProfile> => apiPost("/sub/profile/token", { id }, opts);
export const previewSubscription = (
  token: string,
  format: string,
  opts?: RequestOptions,
): Promise<string> => apiPost("/sub/preview", { token, format }, opts);
