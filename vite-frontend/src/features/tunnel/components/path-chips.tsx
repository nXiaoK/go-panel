import { ArrowRight } from "lucide-react";
import { Fragment } from "react";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import type { Node, TunnelForm } from "@/lib/types";

export function PathChips({
  tunnel,
  nodes,
}: {
  tunnel: Pick<TunnelForm, "type" | "inNodeId" | "relayNodeId" | "outNodeId"> &
    Partial<Pick<TunnelForm, "tcpListenAddr" | "udpListenAddr">>;
  nodes: ReadonlyMap<number, Node>;
}) {
  const ids = [
    tunnel.inNodeId,
    ...(tunnel.type === 2 ? [tunnel.relayNodeId, tunnel.outNodeId] : []),
  ].filter((id): id is number => Boolean(id));
  return (
    <div aria-label="隧道链路" className="flex min-w-0 flex-wrap items-center gap-1.5">
      {ids.map((id, index) => {
        const node = nodes.get(id);
        return (
          <Fragment key={`${id}-${index}`}>
            {index > 0 && <ArrowRight aria-hidden="true" className="size-3.5 shrink-0 text-mute" />}
            <Tooltip>
              <TooltipTrigger asChild>
                <span
                  tabIndex={0}
                  className="inline-flex max-w-full items-center gap-1.5 rounded-xs border border-hairline bg-canvas-soft px-2 py-1 text-caption text-ink"
                >
                  {node?.status !== 1 && (
                    <span
                      role="img"
                      aria-label={node ? "节点离线" : "节点状态未知"}
                      className="size-1.5 shrink-0 rounded-full bg-status-critical"
                    />
                  )}
                  <span className="max-w-40 truncate">{node?.name || `#${id}`}</span>
                  <span className="font-mono text-mute">
                    {node?.forwardMode === "nftables"
                      ? "NFT"
                      : node?.forwardMode === "gost" || !node?.forwardMode
                        ? "GOST"
                        : node.forwardMode}
                  </span>
                </span>
              </TooltipTrigger>
              <TooltipContent className="max-w-72 break-words">
                <p>
                  {node?.name || `#${id}`} · {node?.status === 1 ? "在线" : "离线或未知"}
                </p>
                <p className="font-mono">IP：{node?.ip || node?.serverIp || "—"}</p>
                <p className="font-mono">TCP 监听：{tunnel.tcpListenAddr || "—"}</p>
                <p className="font-mono">UDP 监听：{tunnel.udpListenAddr || "—"}</p>
              </TooltipContent>
            </Tooltip>
          </Fragment>
        );
      })}
      {ids.length === 0 && <span className="text-caption text-mute">选择节点后预览链路</span>}
      {ids.length > 0 && tunnel.type === 1 && (
        <span className="text-caption text-mute">（端口转发）</span>
      )}
    </div>
  );
}
