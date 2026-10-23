import { useQueryClient } from "@tanstack/react-query";
import { useTrafficPackage } from "@/features/account/queries";
import { useForwards } from "@/features/forward/queries";
import { useNodes } from "@/features/node/queries";
import { useForwardTunnels, useTunnels } from "@/features/tunnel/queries";
import { useUsers } from "@/features/user/queries";
import { useSession } from "@/hooks/use-session";
import { resourceKeys, useBusinessEnabled } from "@/lib/query-keys";
export const dashboardKeys = resourceKeys.dashboard;
/** 实体查询与列表页共用缓存；只有流量键包含范围和隧道，失败由所属区块分别呈现。 */
export function useDashboardSources(range: string, tunnelId?: number) {
  const { isAdmin } = useSession();
  const enabled = useBusinessEnabled();
  const client = useQueryClient();
  const traffic = useTrafficPackage(range, tunnelId);
  const nodes = useNodes();
  const tunnels = useTunnels();
  const users = useUsers();
  const forwards = useForwards({ enabled: isAdmin });
  // 此 hook 管理员复用同一个隧道键；普通用户只访问 T01 的安全选项接口。
  const userTunnels = useForwardTunnels();
  const active = isAdmin ? [traffic, nodes, tunnels, forwards, users] : [traffic, userTunnels];
  const lastSync = Math.max(0, ...active.map((query) => query.dataUpdatedAt));
  const refetch = async () => {
    if (!enabled) return;
    const keys = isAdmin
      ? [
          resourceKeys.account.traffic(range, tunnelId),
          resourceKeys.node.rawList(),
          resourceKeys.tunnel.list(),
          resourceKeys.forward.list(),
          resourceKeys.user.list(),
        ]
      : [resourceKeys.account.traffic(range, tunnelId), resourceKeys.tunnel.userOptions()];
    await Promise.all(keys.map((queryKey) => client.invalidateQueries({ queryKey, exact: true })));
  };
  return {
    traffic,
    nodes,
    tunnels,
    forwards,
    users,
    userTunnels,
    lastSync,
    isFetching: active.some((query) => query.isFetching),
    refetch,
  };
}
