import { getAllUsers } from "@/lib/api";
import { resourceKeys, useBusinessEnabled } from "@/lib/query-keys";
import { useQuery } from "@tanstack/react-query";
export const userKeys = resourceKeys.user;

export function useUsers(options: { enabled?: boolean } = {}) {
  const enabled = useBusinessEnabled(true);
  return useQuery({
    queryKey: userKeys.list(),
    queryFn: ({ signal }) => getAllUsers({ signal }),
    enabled: enabled && options.enabled !== false,
  });
}

export { useResourceMutation as useUserMutation } from "@/lib/query-keys";

// 每个用户独立的权限键；queryFn 读取signal，使关闭/切换用户后迟到响应不能回写缓存。
export function useUserTunnelRows<Row>(
  userId: number | null,
  loader: (id: number, signal?: AbortSignal) => Promise<Row[]>,
) {
  const enabled = useBusinessEnabled(true);
  return useQuery({
    queryKey: userKeys.tunnels(userId ?? 0),
    queryFn: ({ signal }) => loader(userId!, signal),
    enabled: enabled && userId !== null,
    staleTime: 0,
  });
}
