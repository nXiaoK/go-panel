import {
  getAllUsers,
  getForwardList,
  getNodeList,
  getTunnelList,
  getUserPackageInfo,
} from "@/lib/api";

export type DashboardHealthStatus = "success" | "partial" | "error";

export type DashboardSourceKey = "package" | "nodes" | "tunnels" | "forwards" | "users";

export type DashboardSourceResult<T = unknown> =
  { ok: true; data: T } | { ok: false; error: string };

export type DashboardSources = {
  package: () => Promise<DashboardSourceResult>;
  nodes?: () => Promise<DashboardSourceResult>;
  tunnels?: () => Promise<DashboardSourceResult>;
  forwards?: () => Promise<DashboardSourceResult>;
  users?: () => Promise<DashboardSourceResult>;
};

export type DashboardLoadResult = {
  status: DashboardHealthStatus;
  errors: Partial<Record<DashboardSourceKey, string>>;
  completedAt: Date | null;
  packageData: unknown | null;
  nodes: unknown[];
  tunnels: unknown[];
  forwards: unknown[];
  users: unknown[];
  missing: DashboardSourceKey[];
};

export type ApiLikeResponse<T = unknown> = {
  code?: number;
  msg?: string;
  data?: T;
};

export function listFromResponse(data: unknown): unknown[] {
  if (Array.isArray(data)) return data;
  if (data && typeof data === "object" && Array.isArray((data as { records?: unknown }).records)) {
    return (data as { records: unknown[] }).records;
  }
  return [];
}

export function resultFromApiResponse(res: ApiLikeResponse): DashboardSourceResult {
  if (res && res.code === 0) {
    return { ok: true, data: res.data };
  }
  return { ok: false, error: res?.msg || "请求失败" };
}

async function safeLoad(
  loader: () => Promise<DashboardSourceResult>,
): Promise<DashboardSourceResult> {
  try {
    return await loader();
  } catch (error) {
    return {
      ok: false,
      error: error instanceof Error ? error.message : "请求失败",
    };
  }
}

function defaultSources(range: string, tunnelId?: number): DashboardSources {
  return {
    package: async () => resultFromApiResponse(await getUserPackageInfo({ range, tunnelId })),
    nodes: async () => resultFromApiResponse(await getNodeList()),
    tunnels: async () => resultFromApiResponse(await getTunnelList()),
    forwards: async () => resultFromApiResponse(await getForwardList()),
    users: async () => resultFromApiResponse(await getAllUsers({ current: 1, size: 500 })),
  };
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
  sources: DashboardSources = defaultSources(range, tunnelId),
): Promise<DashboardLoadResult> {
  const packageResult = await safeLoad(sources.package);

  const adminKeys = (["nodes", "tunnels", "forwards", "users"] as const).filter(
    (key) => typeof sources[key] === "function",
  );

  const adminResults =
    isAdmin && adminKeys.length > 0
      ? await Promise.all(
          adminKeys.map(async (key) => ({
            key: key as DashboardSourceKey,
            result: await safeLoad(sources[key]!),
          })),
        )
      : [];

  const outcomes: Array<{ key: DashboardSourceKey; result: DashboardSourceResult }> = [
    { key: "package", result: packageResult },
    ...adminResults,
  ];
  const summary = summarize(outcomes);

  const byKey = new Map(outcomes.map((item) => [item.key, item.result]));
  const packageOk = byKey.get("package");
  const nodesOk = byKey.get("nodes");
  const tunnelsOk = byKey.get("tunnels");
  const forwardsOk = byKey.get("forwards");
  const usersOk = byKey.get("users");

  return {
    ...summary,
    packageData: packageOk?.ok ? packageOk.data : null,
    nodes: nodesOk?.ok ? listFromResponse(nodesOk.data) : [],
    tunnels: tunnelsOk?.ok ? listFromResponse(tunnelsOk.data) : [],
    forwards: forwardsOk?.ok ? listFromResponse(forwardsOk.data) : [],
    users: usersOk?.ok ? listFromResponse(usersOk.data) : [],
  };
}

/** Test helper: merge partial source overrides. */
export function fakeSources(
  overrides: Partial<Record<DashboardSourceKey, () => Promise<DashboardSourceResult>>>,
): DashboardSources {
  const failAll = async (): Promise<DashboardSourceResult> => ({
    ok: false,
    error: "not provided",
  });
  return {
    package: overrides.package || failAll,
    nodes: overrides.nodes,
    tunnels: overrides.tunnels,
    forwards: overrides.forwards,
    users: overrides.users,
  };
}

export function successSource(data: unknown = null): () => Promise<DashboardSourceResult> {
  return async () => ({ ok: true, data });
}

export function failSource(error: string): () => Promise<DashboardSourceResult> {
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
