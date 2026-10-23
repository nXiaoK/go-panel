import { getNodeList } from "@/lib/api";
import { resourceKeys, useBusinessEnabled } from "@/lib/query-keys";
import type { NodeSystemInfo } from "@/lib/types";
import { useQuery } from "@tanstack/react-query";
export const nodeKeys = resourceKeys.node;

export function useNodes(
  options: { runtime?: boolean; enabled?: boolean; refetchInterval?: number | false } = {},
) {
  const enabled = useBusinessEnabled(true);
  return useQuery({
    queryKey: options.runtime ? nodeKeys.runtimeList() : nodeKeys.rawList(),
    queryFn: async ({ signal }) => {
      const rows = await getNodeList({ signal });
      return options.runtime
        ? rows.map((node) => ({
            ...node,
            connectionStatus: (node.status === 1 ? "online" : "offline") as "online" | "offline",
            systemInfo: null as NodeSystemInfo | null,
          }))
        : rows;
    },
    enabled: enabled && options.enabled !== false,
    refetchInterval: options.refetchInterval,
  });
}

export { useResourceMutation as useNodeMutation } from "@/lib/query-keys";
