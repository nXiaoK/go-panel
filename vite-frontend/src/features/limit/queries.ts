import { getSpeedLimitList } from "@/lib/api";
import { resourceKeys, useBusinessEnabled } from "@/lib/query-keys";
import { useQuery } from "@tanstack/react-query";
export const limitKeys = resourceKeys.limit;

export function useLimits() {
  const enabled = useBusinessEnabled(true);
  return useQuery({
    queryKey: limitKeys.list(),
    queryFn: ({ signal }) => getSpeedLimitList({ signal }),
    enabled,
  });
}

export { useResourceMutation as useLimitMutation } from "@/lib/query-keys";
