import { useTrafficPackage } from "@/features/account/queries";
import { useForwards } from "@/features/forward/queries";
import { useNodes } from "@/features/node/queries";
import { useTunnels } from "@/features/tunnel/queries";
import { useUsers } from "@/features/user/queries";
import { useSession } from "@/hooks/use-session";
import { resourceKeys, useBusinessEnabled } from "@/lib/query-keys";
import type { DashboardLoadResult, DashboardSourceKey } from "./model";
export const dashboardKeys = resourceKeys.dashboard;
export function useDashboardSources(range: string, tunnelId?: number) {
  const { isAdmin } = useSession();
  const enabled = useBusinessEnabled();
  const traffic = useTrafficPackage(range, tunnelId);
  const nodes = useNodes();
  const tunnels = useTunnels();
  const users = useUsers();
  const forwards = useForwards({ enabled: isAdmin });
  const active = isAdmin ? [traffic, nodes, tunnels, forwards, users] : [traffic];
  const keys: DashboardSourceKey[] = isAdmin
    ? ["package", "nodes", "tunnels", "forwards", "users"]
    : ["package"];
  const updatedAt = Math.max(...active.map((query) => query.dataUpdatedAt));
  const hasPending = active.some((query) => query.isPending);
  const errors = active.map((query) => query.error);
  const missing = keys.filter((_, index) => errors[index]);
  const data: DashboardLoadResult | null = hasPending
    ? null
    : {
        status:
          missing.length === 0 ? "success" : missing.length === keys.length ? "error" : "partial",
        errors: Object.fromEntries(
          keys.flatMap((key, index) => (errors[index] ? [[key, errors[index]!.message]] : [])),
        ),
        missing,
        completedAt: missing.length === 0 ? new Date(updatedAt) : null,
        packageData: traffic.data ?? null,
        nodes: isAdmin ? (nodes.data ?? []) : [],
        tunnels: isAdmin ? (tunnels.data ?? []) : [],
        forwards: isAdmin ? (forwards.data ?? []) : [],
        users: isAdmin ? (users.data ?? []) : [],
      };
  const refetch = async () => {
    if (!enabled) return { data: null };
    const outcomes = await Promise.all(active.map((query) => query.refetch()));
    const missing = keys.filter((_, index) => outcomes[index].error);
    return {
      data: {
        status:
          missing.length === 0 ? "success" : missing.length === keys.length ? "error" : "partial",
        missing,
        errors: Object.fromEntries(
          keys.flatMap((key, index) =>
            outcomes[index].error ? [[key, outcomes[index].error!.message]] : [],
          ),
        ),
      },
    };
  };
  return {
    data,
    error: null as Error | null,
    isPending: hasPending,
    isFetching: active.some((query) => query.isFetching),
    refetch,
  };
}
