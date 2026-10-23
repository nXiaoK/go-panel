import { getSubscriptionSettings } from "@/lib/api";
import { resourceKeys, useBusinessEnabled } from "@/lib/query-keys";
import { useQuery } from "@tanstack/react-query";
export const subscriptionKeys = resourceKeys.subscription;

export function useSubscriptionSettings() {
  const enabled = useBusinessEnabled(true);
  return useQuery({
    queryKey: subscriptionKeys.settings(),
    queryFn: ({ signal }) => getSubscriptionSettings({ signal }),
    enabled,
  });
}

export { useResourceMutation as useSubscriptionMutation } from "@/lib/query-keys";
