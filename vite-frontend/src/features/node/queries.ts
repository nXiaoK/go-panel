import { getNodeList } from "@/lib/api";
import { resourceKeys, useBusinessEnabled } from "@/lib/query-keys";
import { useQuery } from "@tanstack/react-query";
export const nodeKeys = resourceKeys.node;

export function useNodes(
  options: { runtime?: boolean; enabled?: boolean; refetchInterval?: number | false } = {},
) {
  const enabled = useBusinessEnabled(true);
  return useQuery({
    // runtime 仅兼容旧调用参数，REST 查询统一复用原始资源缓存；实时指标由 live-store 独立维护。
    queryKey: nodeKeys.rawList(),
    queryFn: ({ signal }) => getNodeList({ signal }),
    enabled: enabled && options.enabled !== false,
    refetchInterval: options.refetchInterval,
  });
}

export { useResourceMutation as useNodeMutation } from "@/lib/query-keys";
