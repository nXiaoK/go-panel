import {
  getAllUsers,
  getForwardList,
  getNodeList,
  getTunnelList,
  getUserPackageInfo,
} from "@/lib/api";

export type DashboardHealthStatus = "success" | "partial" | "error";

export type DashboardSourceKey = "package" | "nodes" | "tunnels" | "forwards" | "users";

import type { Forward, Node, Tunnel, User, UserPackage } from "@/lib/types";

export type DashboardSourceResult<T = unknown> =
  { ok: true; data: T } | { ok: false; error: string };
type SourceData = {
  package: UserPackage;
  nodes: Node[];
  tunnels: Tunnel[];
  forwards: Forward[];
  users: User[];
};
export type DashboardSources = {
  [K in DashboardSourceKey]?: () => Promise<DashboardSourceResult<SourceData[K]>>;
} & { package: () => Promise<DashboardSourceResult<UserPackage>> };
export type DashboardLoadResult = {
  status: DashboardHealthStatus;
  errors: Partial<Record<DashboardSourceKey, string>>;
  completedAt: Date | null;
  packageData: UserPackage | null;
  nodes: Node[];
  tunnels: Tunnel[];
  forwards: Forward[];
  users: User[];
  missing: DashboardSourceKey[];
};
async function safeLoad<T>(loader: () => Promise<T>): Promise<DashboardSourceResult<T>> {
  try {
    return { ok: true, data: await loader() };
  } catch (error) {
    return { ok: false, error: error instanceof Error ? error.message : "请求失败" };
  }
}
function defaultSources(range: string, tunnelId?: number, signal?: AbortSignal): DashboardSources {
  return {
    package: () => safeLoad(() => getUserPackageInfo({ range, tunnelId }, { signal })),
    nodes: () => safeLoad(() => getNodeList({ signal })),
    tunnels: () => safeLoad(() => getTunnelList({ signal })),
    forwards: () => safeLoad(() => getForwardList({ signal })),
    users: () => safeLoad(() => getAllUsers({ signal })),
  };
}
// 注入的数据源也可能抛错，保留局部失败语义而不丢弃其他已完成的数据。
async function readSource<T>(
  source: () => Promise<DashboardSourceResult<T>>,
): Promise<DashboardSourceResult<T>> {
  try {
    return await source();
  } catch (error) {
    return { ok: false, error: error instanceof Error ? error.message : "请求失败" };
  }
}

function summarize(
  outcomes: Array<{ key: DashboardSourceKey; result: DashboardSourceResult }>,
): Pick<DashboardLoadResult, "status" | "errors" | "completedAt" | "missing"> {
  const errors: Partial<Record<DashboardSourceKey, string>> = {};
  const missing: DashboardSourceKey[] = [];
  let successCount = 0;

  for (const { key, result } of outcomes) {
    if (result.ok) {
      successCount += 1;
    } else {
      errors[key] = result.error;
      missing.push(key);
    }
  }

  if (successCount === outcomes.length) {
    return { status: "success", errors, completedAt: new Date(), missing };
  }
  if (successCount === 0) {
    return { status: "error", errors, completedAt: null, missing };
  }
  return { status: "partial", errors, completedAt: null, missing };
}

export async function loadDashboardSources(
  isAdmin: boolean,
  range: string,
  tunnelId?: number,
  sources?: DashboardSources,
  signal?: AbortSignal,
): Promise<DashboardLoadResult> {
  const loaders = sources ?? defaultSources(range, tunnelId, signal);
  const [packageResult, nodes, tunnels, forwards, users] = await Promise.all([
    readSource(loaders.package),
    isAdmin && loaders.nodes ? readSource(loaders.nodes) : undefined,
    isAdmin && loaders.tunnels ? readSource(loaders.tunnels) : undefined,
    isAdmin && loaders.forwards ? readSource(loaders.forwards) : undefined,
    isAdmin && loaders.users ? readSource(loaders.users) : undefined,
  ]);
  const outcomes: Array<{ key: DashboardSourceKey; result: DashboardSourceResult }> = [
    { key: "package", result: packageResult },
  ];
  if (nodes) outcomes.push({ key: "nodes", result: nodes });
  if (tunnels) outcomes.push({ key: "tunnels", result: tunnels });
  if (forwards) outcomes.push({ key: "forwards", result: forwards });
  if (users) outcomes.push({ key: "users", result: users });
  return {
    ...summarize(outcomes),
    packageData: packageResult.ok ? packageResult.data : null,
    nodes: nodes?.ok ? nodes.data : [],
    tunnels: tunnels?.ok ? tunnels.data : [],
    forwards: forwards?.ok ? forwards.data : [],
    users: users?.ok ? users.data : [],
  };
}

/** Test helper: merge partial source overrides. */
export function fakeSources(overrides: Partial<DashboardSources>): DashboardSources {
  return { package: overrides.package || failSource("not provided"), ...overrides };
}
export function successSource<T>(data: T): () => Promise<DashboardSourceResult<T>> {
  return async () => ({ ok: true, data });
}
export function failSource(error: string): () => Promise<{ ok: false; error: string }> {
  return async () => ({ ok: false, error });
}

export function healthBadge(status: DashboardHealthStatus): {
  label: string;
  className: string;
  tone: "success" | "warning" | "danger";
} {
  switch (status) {
    case "success":
      return {
        label: "system nominal",
        className: "border-success/40 bg-success/10 text-success",
        tone: "success",
      };
    case "partial":
      return {
        label: "partial sync",
        className: "border-warning/40 bg-warning/10 text-warning",
        tone: "warning",
      };
    case "error":
    default:
      return {
        label: "sync failed",
        className: "border-destructive/40 bg-destructive/10 text-destructive",
        tone: "danger",
      };
  }
}

export function formatMissingSources(missing: DashboardSourceKey[]): string {
  if (missing.length === 0) return "";
  const labels: Record<DashboardSourceKey, string> = {
    package: "流量套餐",
    nodes: "节点",
    tunnels: "隧道",
    forwards: "转发",
    users: "用户",
  };
  return missing.map((key) => labels[key] || key).join("、");
}
