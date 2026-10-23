import { getSystemStatus } from "@/lib/api";
import { resourceKeys, useBusinessEnabled } from "@/lib/query-keys";
import { useQuery } from "@tanstack/react-query";
export const systemKeys = resourceKeys.system;

export function useSystemStatus() {
  // 保持侧栏既有访问：后端按角色返回公开品牌配置/进程时长；版本、升级仍由管理员专用接口保护。
  const enabled = useBusinessEnabled();
  return useQuery({
    queryKey: systemKeys.status(),
    queryFn: ({ signal }) => getSystemStatus({ signal }),
    enabled,
  });
}

export { useResourceMutation as useSystemMutation } from "@/lib/query-keys";
