import { useForwards } from "@/features/forward/queries";
import { useNodes } from "@/features/node/queries";
import { useTunnels } from "@/features/tunnel/queries";
import { useUsers } from "@/features/user/queries";
import { useSession } from "@/hooks/use-session";
import { useMemo } from "react";
import { buildGlobalSearchItems } from "./model";

// 搜索只启用当前角色有权读取的共享实体查询；关闭时不额外发请求，变更失效由实体键自然带动。
export function useSearchItems(open: boolean) {
  const { isAdmin } = useSession();
  const nodes = useNodes({ enabled: open && isAdmin });
  const tunnels = useTunnels({ enabled: open && isAdmin });
  const users = useUsers({ enabled: open && isAdmin });
  const forwards = useForwards({ enabled: open });
  const items = useMemo(
    () =>
      buildGlobalSearchItems({
        nodes: isAdmin ? nodes.data : [],
        tunnels: isAdmin ? tunnels.data : [],
        users: isAdmin ? users.data : [],
        forwards: forwards.data,
      }),
    [isAdmin, nodes.data, tunnels.data, users.data, forwards.data],
  );
  const active = isAdmin ? [nodes, tunnels, users, forwards] : [forwards];
  return {
    items,
    isFetching: open && active.some((query) => query.isFetching),
    error: open ? active.find((query) => query.error)?.error : undefined,
  };
}
