import { useSession } from "@/hooks/use-session";
import { getTunnelList, userTunnel } from "@/lib/api";
import { resourceKeys, useBusinessEnabled } from "@/lib/query-keys";
import type { ForwardTunnelOption } from "@/lib/types";
import { useQuery } from "@tanstack/react-query";
export const tunnelKeys = resourceKeys.tunnel;

export function useTunnels(options: { enabled?: boolean } = {}) {
  const enabled = useBusinessEnabled(true);
  return useQuery({
    queryKey: tunnelKeys.list(),
    queryFn: ({ signal }) => getTunnelList({ signal }),
    enabled: enabled && options.enabled !== false,
  });
}
export function useForwardTunnels() {
  const enabled = useBusinessEnabled();
  const { isAdmin } = useSession();
  return useQuery({
    queryKey: isAdmin ? tunnelKeys.list() : tunnelKeys.userOptions(),
    queryFn: async ({ signal }): Promise<ForwardTunnelOption[]> => {
      if (isAdmin) return getTunnelList({ signal });
      const options = await userTunnel({ signal });
      return options.map(({ ip, protocol: _protocol, ...option }) => ({ ...option, inIp: ip }));
    },
    enabled,
  });
}

export { useResourceMutation as useTunnelMutation } from "@/lib/query-keys";
