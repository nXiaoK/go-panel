import { useEffect, useRef, useState, type ReactNode } from "react";
import { Meter } from "@/components/patterns/meter";
import { StatusPill } from "@/components/patterns/status-pill";
import { formatBytes, formatDuration, formatRate } from "@/lib/format";
import { nodeStatus, syncStatus } from "@/lib/status";
import type { Node, NodeSystemInfo } from "@/lib/types";
import { cn } from "@/lib/utils";
import { useNodeLive } from "./use-node-live";

export function NodeLiveMetric({ node, field }: { node: Node; field: keyof NodeSystemInfo }) {
  const live = useNodeLive(node.id);
  const online = live ? live.connection === "online" : node.status === 1;
  const info = online ? live?.info : null;
  const value = info?.[field];
  return (
    <span className="font-mono tabular">
      {value == null
        ? "—"
        : field === "cpuUsage" || field === "memoryUsage"
          ? `${value.toFixed(1)}%`
          : formatRate(value)}
    </span>
  );
}
export function NodeVersion({ node }: { node: Node }) {
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-2">
      <span className="break-all font-mono">{node.version || "—"}</span>
      {node.upgradeAvailable && (
        <StatusPill
          tone="warning"
          label={`可升级${node.latestVersion ? ` → ${node.latestVersion}` : ""}`}
        />
      )}
    </div>
  );
}
export function NodeSync({ node }: { node: Node }) {
  return (
    <span title={node.syncLastError}>
      <StatusPill {...syncStatus(node.syncState ?? "unknown")} />
    </span>
  );
}
export function NodeCard({
  node,
  actions,
  focus = false,
  framed = true,
}: {
  node: Node;
  actions: ReactNode;
  focus?: boolean;
  framed?: boolean;
}) {
  const live = useNodeLive(node.id);
  const online = live ? live.connection === "online" : node.status === 1;
  const info = online ? live?.info : null;
  const element = useRef<HTMLElement>(null);
  const [highlight, setHighlight] = useState(false);
  useEffect(() => {
    if (!focus) return;
    element.current?.scrollIntoView?.({ block: "center", behavior: "instant" });
    element.current?.focus({ preventScroll: true });
    setHighlight(true);
    const timer = setTimeout(() => setHighlight(false), 2000);
    return () => clearTimeout(timer);
  }, [focus]);
  return (
    <article
      ref={element}
      tabIndex={focus ? 0 : undefined}
      data-node-id={node.id}
      data-highlighted={highlight || undefined}
      aria-label={`节点 ${node.name}`}
      className={cn(
        "min-w-0 space-y-4 text-sm leading-5",
        framed && "rounded-md border border-hairline bg-canvas p-5",
        highlight && "bg-surface-selected ring-1 ring-primary-soft",
      )}
    >
      <div className="flex flex-wrap items-start justify-between gap-2">
        <h2 className="min-w-0 break-words text-section-title text-ink">{node.name}</h2>
        <div className="flex items-center gap-2">
          <span className="rounded-xs border border-hairline px-1.5 font-mono text-caption text-body">
            {node.forwardMode === "nftables" ? "NFT" : "GOST"}
          </span>
          <StatusPill {...nodeStatus({ status: online ? 1 : 0 })} />
        </div>
      </div>
      <div className="space-y-1 font-mono text-caption text-mute">
        <p className="break-all">{node.serverIp || "—"}</p>
        <p>
          端口 {node.portSta ?? "—"}–{node.portEnd ?? "—"}
        </p>
      </div>
      {online ? (
        <div className="space-y-3">
          <div className="grid grid-cols-2 gap-4">
            {[
              { label: "CPU", value: info?.cpuUsage },
              { label: "内存", value: info?.memoryUsage },
            ].map((metric) => (
              <div key={metric.label}>
                <p className="mb-2 text-caption text-mute">{metric.label}</p>
                {metric.value == null ? (
                  <p className="font-mono text-caption text-mute">—</p>
                ) : (
                  <Meter
                    value={metric.value}
                    max={100}
                    label={`${node.name} ${metric.label}`}
                    formatValue={(value) => `${value.toFixed(1)}%`}
                  />
                )}
              </div>
            ))}
          </div>
          <dl className="grid grid-cols-2 gap-x-4 gap-y-2 font-mono text-caption tabular text-body">
            <div>
              <dt className="text-mute">↑ 上行</dt>
              <dd>{info ? formatRate(info.uploadSpeed) : "—"}</dd>
            </div>
            <div>
              <dt className="text-mute">↓ 下行</dt>
              <dd>{info ? formatRate(info.downloadSpeed) : "—"}</dd>
            </div>
            <div>
              <dt className="text-mute">↑ 累计</dt>
              <dd>{info ? formatBytes(info.uploadTraffic) : "—"}</dd>
            </div>
            <div>
              <dt className="text-mute">↓ 累计</dt>
              <dd>{info ? formatBytes(info.downloadTraffic) : "—"}</dd>
            </div>
          </dl>
          <p className="text-caption text-mute">
            运行 <span className="font-mono">{info ? formatDuration(info.uptime) : "—"}</span>
          </p>
        </div>
      ) : (
        <p className="min-h-28 text-sm leading-5 text-mute">离线</p>
      )}
      <div className="space-y-2 text-caption text-body">
        <NodeVersion node={node} />
        <NodeSync node={node} />
      </div>
      {node.syncState === "failed" && (
        <p
          className="break-words text-caption text-status-critical-text"
          title={node.syncLastError}
        >
          规则同步失败{node.syncLastError ? `：${node.syncLastError}` : ""}
        </p>
      )}
      <div className="border-t border-hairline-subtle pt-3">{actions}</div>
    </article>
  );
}
