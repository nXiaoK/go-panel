import { getSystemStatus } from "@/lib/api";
import { resourceKeys, useBusinessEnabled } from "@/lib/query-keys";
import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
export const systemKeys = resourceKeys.system;

export function useSystemStatus() {
  // 仅管理员查询进程状态；60 秒轮询，隐藏页暂停，恢复后由下一次轮询继续。
  const businessEnabled = useBusinessEnabled(true);
  const [visible, setVisible] = useState(
    () => typeof document === "undefined" || document.visibilityState !== "hidden",
  );
  useEffect(() => {
    const update = () => setVisible(document.visibilityState !== "hidden");
    document.addEventListener("visibilitychange", update);
    return () => document.removeEventListener("visibilitychange", update);
  }, []);
  return useQuery({
    queryKey: systemKeys.status(),
    queryFn: ({ signal }) => getSystemStatus({ signal }),
    enabled: businessEnabled && visible,
    retry: false,
    refetchInterval: 60_000,
    refetchIntervalInBackground: false,
    refetchOnWindowFocus: false,
  });
}

export { useResourceMutation as useSystemMutation } from "@/lib/query-keys";
