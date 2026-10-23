import { Copy, Gauge, MoreHorizontal, Pencil, Stethoscope, Trash2 } from "lucide-react";
import { DataTable } from "@/components/patterns/data-table";
import { EmptyState } from "@/components/patterns/feedback";
import { StatusPill } from "@/components/patterns/status-pill";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { notify } from "@/lib/notify";
import type { Node, Tunnel } from "@/lib/types";
import type { OnChangeFn, SortingState } from "@tanstack/react-table";
import { canSpeedTest, nftModeFor } from "../model";
import { PathChips } from "./path-chips";

export function TunnelList({
  tunnels,
  nodes,
  loading,
  error,
  onRetry,
  filtered,
  onClearFilters,
  onCreate,
  onEdit,
  onDiagnose,
  onSpeed,
  onDelete,
  focusRowId,
  sorting,
  onSortingChange,
}: {
  tunnels: Tunnel[];
  nodes: ReadonlyMap<number, Node>;
  loading?: boolean;
  error?: unknown;
  onRetry: () => void;
  filtered: boolean;
  onClearFilters: () => void;
  onCreate: () => void;
  onEdit: (tunnel: Tunnel) => void;
  onDiagnose: (tunnel: Tunnel) => void;
  onSpeed: (tunnel: Tunnel) => void;
  onDelete: (tunnel: Tunnel) => void;
  focusRowId?: string;
  sorting: SortingState;
  onSortingChange: OnChangeFn<SortingState>;
}) {
  const status = (tunnel: Tunnel) => (
    <StatusPill
      tone={tunnel.status === 0 ? "neutral" : "good"}
      label={tunnel.status === 0 ? "已禁用" : "启用"}
      hollow={tunnel.status === 0}
    />
  );
  const protocol = (tunnel: Tunnel) =>
    nftModeFor(tunnel, nodes) ? "TCP+UDP" : (tunnel.protocol || "tcp").toUpperCase();
  const billing = (tunnel: Tunnel) =>
    `${tunnel.flow === 1 ? "单向" : "双向"} ×${tunnel.trafficRatio ?? 1}`;
  const actions = (tunnel: Tunnel) => (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          aria-label={`隧道操作：${tunnel.name}`}
        >
          <MoreHorizontal />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem onSelect={() => onEdit(tunnel)}>
          <Pencil />
          编辑
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={() => onDiagnose(tunnel)}>
          <Stethoscope />
          诊断
        </DropdownMenuItem>
        {canSpeedTest(tunnel) && (
          <DropdownMenuItem onSelect={() => onSpeed(tunnel)}>
            <Gauge />
            测速
          </DropdownMenuItem>
        )}
        <DropdownMenuItem
          onSelect={() => {
            void navigator.clipboard.writeText(String(tunnel.id)).then(
              () => notify.success("隧道 ID 已复制"),
              () => notify.error("复制失败，请重试"),
            );
          }}
        >
          <Copy />
          复制隧道 ID
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem
          className="text-status-critical-text focus:text-status-critical-text"
          onSelect={() => onDelete(tunnel)}
        >
          <Trash2 />
          删除
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
  return (
    <DataTable
      ariaLabel="隧道列表"
      data={tunnels}
      getRowId={(tunnel) => String(tunnel.id)}
      loading={loading}
      error={error}
      onRetry={onRetry}
      filtered={filtered}
      onClearFilters={onClearFilters}
      focusRowId={focusRowId}
      sorting={sorting}
      onSortingChange={onSortingChange}
      emptyState={
        <EmptyState
          filtered={filtered}
          onClearFilters={onClearFilters}
          title="暂无隧道"
          description="创建隧道来连接节点并提供转发链路"
          action={
            <Button type="button" onClick={onCreate}>
              新建隧道
            </Button>
          }
        />
      }
      columns={[
        {
          accessorKey: "name",
          header: "名称",
          cell: ({ row }) => (
            <div className="min-w-28">
              <span className="text-body-strong text-ink">{row.original.name}</span>
              <span className="ml-2 font-mono text-caption text-mute">#{row.original.id}</span>
            </div>
          ),
        },
        {
          id: "path",
          header: "链路",
          cell: ({ row }) => (
            <div className="min-w-52">
              <PathChips tunnel={row.original} nodes={nodes} />
            </div>
          ),
        },
        {
          id: "protocol",
          header: "协议",
          cell: ({ row }) => (
            <span className="whitespace-nowrap font-mono text-caption">
              {protocol(row.original)}
            </span>
          ),
        },
        {
          id: "billing",
          header: "计费",
          cell: ({ row }) => (
            <span className="whitespace-nowrap font-mono text-caption text-body">
              {billing(row.original)}
            </span>
          ),
        },
        { accessorKey: "status", header: "状态", cell: ({ row }) => status(row.original) },
        {
          id: "actions",
          header: "操作",
          meta: { actions: true, align: "right" },
          cell: ({ row }) => actions(row.original),
        },
      ]}
      renderMobileCard={(tunnel) => (
        <div className="space-y-3">
          <div className="flex items-start justify-between gap-2">
            <div className="min-w-0">
              <p className="break-words text-body-strong text-ink">
                {tunnel.name} <span className="font-mono text-caption text-mute">#{tunnel.id}</span>
              </p>
              <div className="mt-1">{status(tunnel)}</div>
            </div>
            {actions(tunnel)}
          </div>
          <PathChips tunnel={tunnel} nodes={nodes} />
          <p className="font-mono text-caption text-body">
            {protocol(tunnel)} · {billing(tunnel)}
          </p>
        </div>
      )}
    />
  );
}
