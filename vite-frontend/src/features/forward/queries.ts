import { resourceKeys, useBusinessEnabled } from "@/lib/query-keys";
import { useQuery } from "@tanstack/react-query";
import { loadSortedForwards } from "./list";
export const forwardKeys = resourceKeys.forward;

export function useForwards(options: { enabled?: boolean; refetchInterval?: number | false } = {}) {
  const enabled = useBusinessEnabled();
  return useQuery({
    queryKey: forwardKeys.list(),
    queryFn: ({ signal }) => loadSortedForwards(undefined, signal),
    enabled: enabled && options.enabled !== false,
    refetchInterval: options.refetchInterval,
  });
}

export { useResourceMutation as useForwardMutation } from "@/lib/query-keys";
