import { getUserPackageInfo } from "@/lib/api";
import { resourceKeys, useBusinessEnabled } from "@/lib/query-keys";
import { useQuery, useQueryClient } from "@tanstack/react-query";
export const accountKeys = resourceKeys.account;

export function useAccountPackage(options: { enabled?: boolean } = {}) {
  const enabled = useBusinessEnabled();
  return useQuery({
    queryKey: accountKeys.package(),
    queryFn: ({ signal }) => getUserPackageInfo({}, { signal }),
    enabled: enabled && options.enabled !== false,
  });
}
export function useTrafficPackage(range: string, tunnelId?: number) {
  const client = useQueryClient();
  const enabled = useBusinessEnabled();
  return useQuery({
    queryKey: accountKeys.traffic(range, tunnelId),
    queryFn: ({ signal }) => getUserPackageInfo({ range, tunnelId }, { signal }),
    enabled,
    // 仅保留仍在当前QueryClient中的前次流量；会话清缓存后不得复用旧账号响应。
    placeholderData: (previous, previousQuery) =>
      previousQuery && client.getQueryData(previousQuery.queryKey) === previous
        ? previous
        : undefined,
  });
}

export { useResourceMutation as useAccountMutation } from "@/lib/query-keys";
