import { useEffect, useRef, useState, type ReactNode } from "react";
import { useSystemStatus } from "./queries";

import { PanelStatusContext } from "./panel-status-context";
/** 外壳只创建一个轮询观察者，顶栏和侧栏共享连续失败计数。 */
export function PanelStatusProvider({ children }: { children: ReactNode }) {
  const query = useSystemStatus();
  const [failures, setFailures] = useState(0);
  const [uptimeSeconds, setUptimeSeconds] = useState<number | null>(null);
  const lastError = useRef(0);
  useEffect(() => {
    if (query.errorUpdatedAt && query.errorUpdatedAt !== lastError.current) {
      lastError.current = query.errorUpdatedAt;
      setFailures((value) => value + 1);
    }
  }, [query.errorUpdatedAt]);
  useEffect(() => {
    if (query.dataUpdatedAt) setFailures(0);
  }, [query.dataUpdatedAt]);
  useEffect(() => {
    if (!query.data) return;
    const status = query.data;
    const startedAt = Number(status.startedAt || 0);
    const update = () =>
      setUptimeSeconds(
        startedAt > 0
          ? Math.max(0, Math.floor((Date.now() - startedAt) / 1000))
          : Number(status.uptimeSeconds || 0),
      );
    update();
    const timer = window.setInterval(update, 60_000);
    return () => window.clearInterval(timer);
  }, [query.data]);
  return (
    <PanelStatusContext.Provider value={{ failed: failures >= 2, uptimeSeconds }}>
      {children}
    </PanelStatusContext.Provider>
  );
}
