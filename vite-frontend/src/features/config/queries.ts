import { getConfigs } from "@/lib/api";
import { resourceKeys, useBusinessEnabled } from "@/lib/query-keys";
import { useQuery } from "@tanstack/react-query";
import { normalizeConfigItems } from "./model";
export const configKeys = resourceKeys.config;

export function useConfigs() {
  // 保持侧栏既有访问：后端按角色返回公开品牌配置/进程时长；版本、升级仍由管理员专用接口保护。
  const enabled = useBusinessEnabled();
  return useQuery({
    queryKey: configKeys.list(),
    queryFn: async ({ signal }) => normalizeConfigItems(await getConfigs({ signal })),
    enabled,
  });
}

export { useResourceMutation as useConfigMutation } from "@/lib/query-keys";
