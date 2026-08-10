import type { ApiResponse } from "@/lib/types";

/**
 * Structured query keys for react-query. Hierarchical so a mutation can
 * invalidate at any granularity (`queries.forward.all` clears every forward
 * query, `queries.forward.list` only the list).
 */
export const queries = {
  dashboard: {
    all: ["dashboard"] as const,
    sources: (isAdmin: boolean, range: string, tunnelId?: number) =>
      ["dashboard", "sources", { isAdmin, range, tunnelId }] as const,
  },
  node: {
    all: ["node"] as const,
    // list() 仅作为批量失效前缀；原始节点与附带 WebSocket 运行态的节点结构不同，
    // 必须使用独立缓存键，避免先访问隧道/转发页后节点页误读缺少运行态的数据。
    list: () => ["node", "list"] as const,
    rawList: () => ["node", "list", "raw"] as const,
    runtimeList: () => ["node", "list", "runtime"] as const,
    install: (id: number, forwardMode?: string) =>
      ["node", "install", { id, forwardMode }] as const,
  },
  tunnel: {
    all: ["tunnel"] as const,
    list: () => ["tunnel", "list"] as const,
    byId: (id: number) => ["tunnel", "byId", { id }] as const,
    userList: (params: Record<string, unknown>) => ["tunnel", "userList", params] as const,
  },
  forward: {
    all: ["forward"] as const,
    list: () => ["forward", "list"] as const,
  },
  limit: {
    all: ["limit"] as const,
    list: () => ["limit", "list"] as const,
  },
  user: {
    all: ["user"] as const,
    list: (params: Record<string, unknown>) => ["user", "list", params] as const,
    package: (range?: string, tunnelId?: number) =>
      ["user", "package", { range, tunnelId }] as const,
  },
  config: {
    all: ["config"] as const,
    list: () => ["config", "list"] as const,
    byName: (name: string) => ["config", "byName", { name }] as const,
    systemStatus: () => ["config", "systemStatus"] as const,
  },
  subscription: {
    all: ["subscription"] as const,
    settings: () => ["subscription", "settings"] as const,
    profiles: () => ["subscription", "profiles"] as const,
    proxyNodes: () => ["subscription", "proxyNodes"] as const,
    relayPreview: (nodeId: number, tunnelId: number, inPort: number | null) =>
      ["subscription", "relayPreview", { nodeId, tunnelId, inPort }] as const,
  },
  r2: {
    all: ["r2"] as const,
    settings: () => ["r2", "settings"] as const,
  },
} as const;

/**
 * Unwrap a panel API response for react-query: throw on any non-zero code
 * so useQuery surfaces the failure through `error` / `isError` instead of
 * treating a business error as resolved data.
 */
export function unwrap<T>(res: ApiResponse<T>): T {
  if (res && res.code === 0) return res.data;
  throw new Error(res?.msg || "请求失败");
}

/**
 * Normalize the two list shapes the backend returns (bare array or
 * `{ records }` page envelope) into a plain array.
 */
export function listFrom<T>(data: T[] | { records?: T[] } | null | undefined): T[] {
  if (Array.isArray(data)) return data;
  if (data && Array.isArray(data.records)) return data.records;
  return [];
}

/**
 * Combine unwrap-style code checking with list normalization for untyped
 * list endpoints: throws on non-zero code, returns a plain array otherwise.
 */
export function listData<T>(res: ApiResponse<unknown>): T[] {
  if (res && res.code === 0) {
    return listFrom<T>(res.data as T[] | { records?: T[] } | null | undefined);
  }
  throw new Error(res?.msg || "请求失败");
}
