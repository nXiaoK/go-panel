import type { NodeSystemInfo } from "@/lib/types";

export type NodeLive = {
  connection: "online" | "offline";
  info: NodeSystemInfo | null;
  updatedAt: number;
};
export type NodeLiveMessage = { id?: number; type?: string; data?: unknown };

function numeric(metrics: Record<string, unknown>, ...keys: string[]): number {
  for (const key of keys) {
    const value = metrics[key];
    if (value === undefined || value === null || value === "") continue;
    const number = Number(value);
    if (Number.isFinite(number) && number >= 0) return number;
  }
  return 0;
}

/** 独立于 REST 缓存与 React；每次写入生成稳定快照，供 useSyncExternalStore 订阅。 */
export function createNodeLiveStore() {
  let snapshot: ReadonlyMap<number, NodeLive> = new Map();
  const listeners = new Set<() => void>();
  const publish = () => listeners.forEach((listener) => listener());
  return {
    getSnapshot: () => snapshot,
    subscribe(listener: () => void) {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    clear() {
      if (!snapshot.size) return;
      snapshot = new Map();
      publish();
    },
    receive(message: NodeLiveMessage, now = Date.now()) {
      const { id, type, data } = message;
      if (!Number.isSafeInteger(id) || !id || id < 1) return;
      const previous = snapshot.get(id);
      let next: NodeLive;
      if (type === "status" && (data === 0 || data === 1)) {
        next = {
          connection: data === 1 ? "online" : "offline",
          info: data === 0 ? null : (previous?.info ?? null),
          updatedAt: now,
        };
      } else if (type === "info") {
        let parsed: unknown = data;
        if (typeof data === "string") {
          try {
            parsed = JSON.parse(data);
          } catch {
            return;
          }
        }
        if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return;
        const metrics = parsed as Record<string, unknown>;
        const uploadTraffic = numeric(metrics, "bytes_transmitted", "bytesTransmitted");
        const downloadTraffic = numeric(metrics, "bytes_received", "bytesReceived");
        const uptime = numeric(metrics, "uptime");
        const old = previous?.info;
        const td = old ? uptime - old.uptime : 0;
        // 节点约每秒推送一次；只在 0 < td <= 10 时计算速率，重启/回绕或长时间断流不制造负值与尖峰。
        const validInterval = old && td > 0 && td <= 10;
        next = {
          connection: "online",
          updatedAt: now,
          info: {
            cpuUsage: numeric(metrics, "cpu_usage", "cpuUsage"),
            memoryUsage: numeric(metrics, "memory_usage", "memoryUsage"),
            uploadTraffic,
            downloadTraffic,
            uptime,
            uploadSpeed:
              validInterval && uploadTraffic >= old.uploadTraffic
                ? (uploadTraffic - old.uploadTraffic) / td
                : 0,
            downloadSpeed:
              validInterval && downloadTraffic >= old.downloadTraffic
                ? (downloadTraffic - old.downloadTraffic) / td
                : 0,
          },
        };
      } else return;
      snapshot = new Map(snapshot).set(id, next);
      publish();
    },
  };
}

export const nodeLiveStore = createNodeLiveStore();
