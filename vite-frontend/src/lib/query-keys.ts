import {
  useMutation,
  useQueryClient,
  type QueryClient,
  type UseMutationOptions,
} from "@tanstack/react-query";
import { useSession } from "@/hooks/use-session";

export const resourceKeys = {
  dashboard: {
    all: ["dashboard"] as const,
  },
  node: {
    all: ["node"] as const,
    // list() 是批量失效前缀；所有页面共用 REST 缓存，实时指标由 node/live-store 独立维护。
    // runtimeList 只兼容旧调用并返回相同键，避免节点页与控制台重复获取列表。
    list: () => ["node", "list"] as const,
    rawList: () => ["node", "list", "raw"] as const,
    runtimeList: () => ["node", "list", "raw"] as const,
    install: (id: number, forwardMode?: string) =>
      ["node", "install", { id, forwardMode }] as const,
  },
  tunnel: {
    all: ["tunnel"] as const,
    list: () => ["tunnel", "list"] as const,
    byId: (id: number) => ["tunnel", "byId", { id }] as const,
    userOptions: () => ["tunnel", "userOptions"] as const,
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
    list: () => ["user", "list"] as const,
    tunnels: (userId: number) => ["user", "tunnels", userId] as const,
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
  account: {
    all: ["account"] as const,
    package: () => ["account", "package"] as const,
    traffic: (range: string, tunnelId?: number) =>
      ["account", "traffic", { range, tunnelId }] as const,
  },
  auth: { all: ["auth"] as const },
  system: { all: ["system"] as const, status: () => ["system", "status"] as const },
  r2: {
    all: ["r2"] as const,
    settings: () => ["r2", "settings"] as const,
  },
} as const;

// 后端删除节点/隧道/限速时拒绝仍被引用的资源；用户删除会移除其转发、授权和账本。
// 搜索直接订阅这些实体键，不保存第二份索引，因而无需独立 search 缓存失效。
// account 包含套餐与流量；授权、重置流量、用户/隧道调整都必须同步该账户数据。
export const invalidationMatrix = {
  "node.create": ["node", "tunnel", "forward", "dashboard"],
  "node.update": ["node", "tunnel", "forward", "dashboard"],
  "node.delete": ["node", "tunnel", "forward", "dashboard"],
  "node.upgrade": ["node", "tunnel", "forward", "dashboard"],
  "tunnel.create": ["tunnel", "forward", "limit", "user", "account", "dashboard"],
  "tunnel.update": ["tunnel", "forward", "limit", "user", "account", "dashboard"],
  "tunnel.delete": ["tunnel", "forward", "limit", "user", "account", "dashboard"],
  "forward.create": ["forward", "account", "dashboard"],
  "forward.update": ["forward", "account", "dashboard"],
  "forward.delete": ["forward", "account", "dashboard"],
  "forward.status": ["forward", "account", "dashboard"],
  "forward.order": ["forward", "account", "dashboard"],
  "limit.create": ["limit", "user", "account"],
  "limit.update": ["limit", "user", "account"],
  "limit.delete": ["limit", "user", "account"],
  "user.create": ["user", "forward", "account", "dashboard"],
  "user.update": ["user", "forward", "account", "dashboard"],
  "user.delete": ["user", "forward", "account", "dashboard"],
  "user.reset": ["user", "forward", "account", "dashboard"],
  "user.authorize": ["user", "tunnel", "forward", "account", "dashboard"],
  "subscription.update": ["subscription"],
  "subscription.relay": ["subscription", "forward", "account", "dashboard"],
  "config.update": ["config"],
  "account.password": ["account"],
  "system.update": ["system", "config"],
  "system.backup": ["r2"],
} as const;
export type MutationEvent = keyof typeof invalidationMatrix;
export function invalidateFor(client: QueryClient, event: MutationEvent) {
  return Promise.all(
    invalidationMatrix[event].map((resource) => client.invalidateQueries({ queryKey: [resource] })),
  );
}
export function useBusinessEnabled(adminOnly = false) {
  const { token, mustChangePassword, isAdmin } = useSession();
  return Boolean(token) && !mustChangePassword && (!adminOnly || isAdmin);
}
export function useResourceMutation<TData, TVariables>(
  event: MutationEvent,
  options: UseMutationOptions<TData, Error, TVariables>,
) {
  const client = useQueryClient();
  return useMutation({
    ...options,
    onSuccess: (data, variables, result, context) => {
      void invalidateFor(client, event);
      return options.onSuccess?.(data, variables, result, context);
    },
  });
}
