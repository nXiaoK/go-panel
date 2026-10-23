import { useMemo, useState } from "react";
import { ArrowRight } from "lucide-react";
import type { ColumnDef } from "@tanstack/react-table";
import { StatTile } from "@/components/patterns/stat-tile";
import { StatusPill } from "@/components/patterns/status-pill";
import { DataTable } from "@/components/patterns/data-table";
import { EmptyState, ErrorState, LoadingRows } from "@/components/patterns/feedback";
import { Button } from "@/components/ui/button";
import { NodeLiveMetric } from "@/features/node/node-card";
import { formatBytes } from "@/lib/format";
import { nodeStatus, syncStatus } from "@/lib/status";
import type { Node } from "@/lib/types";
import { buildAttentionItems, nodeOnline, sortDashboardNodes } from "../model";
import type { useDashboardSources } from "../queries";
import { resolveTrafficTotalBytes } from "../traffic";
import { QueryTile, Section, ViewLink } from "./section";

type Props = {
  queries: ReturnType<typeof useDashboardSources>;
  connections: ReadonlyMap<number, string>;
};
export function AdminStats({ queries, connections }: Props) {
  const { nodes, tunnels, forwards, traffic } = queries;
  const offline = nodes.data?.filter((node) => !nodeOnline(node, connections)).length ?? 0;
  const failed = forwards.data?.filter((forward) => forward.status === -1).length ?? 0;
  return (
    <div className="grid min-w-0 gap-4 sm:grid-cols-2 xl:grid-cols-5" aria-label="系统指标">
      <QueryTile query={nodes} label="在线节点">
        <StatTile
          label="在线节点"
          loading={nodes.isPending}
          value={`${(nodes.data?.length ?? 0) - offline} / ${nodes.data?.length ?? 0}`}
          hint={
            offline ? (
              <ViewLink href="/node?status=offline">{offline} 离线</ViewLink>
            ) : (
              `共 ${nodes.data?.length ?? 0} 个`
            )
          }
        />
      </QueryTile>
      <QueryTile query={tunnels} label="启用隧道">
        <StatTile
          label="启用隧道"
          loading={tunnels.isPending}
          value={tunnels.data?.filter((tunnel) => tunnel.status === 1).length ?? 0}
          hint={<ViewLink href="/tunnel">共 {tunnels.data?.length ?? 0} 条</ViewLink>}
        />
      </QueryTile>
      <QueryTile query={forwards} label="运行中转发">
        <StatTile
          label="运行中转发"
          loading={forwards.isPending}
          value={forwards.data?.filter((forward) => forward.status === 1).length ?? 0}
          hint={
            failed ? (
              <ViewLink href="/forward?status=error">{failed} 异常</ViewLink>
            ) : (
              `共 ${forwards.data?.length ?? 0} 条`
            )
          }
        />
      </QueryTile>
      <QueryTile query={traffic} label="今日流量">
        <StatTile
          label="今日流量"
          loading={traffic.isPending}
          value={formatBytes(resolveTrafficTotalBytes(traffic.data?.todayTraffic))}
          hint="全系统"
        />
      </QueryTile>
      <QueryTile query={traffic} label="累计流量">
        <StatTile
          label="累计流量"
          loading={traffic.isPending}
          value={formatBytes(resolveTrafficTotalBytes(traffic.data?.trafficTotals))}
          hint="全系统"
        />
      </QueryTile>
    </div>
  );
}
export function NodeSection({ queries, connections }: Props) {
  const { nodes } = queries;
  const data = useMemo(
    () => sortDashboardNodes(nodes.data ?? [], connections).slice(0, 8),
    [nodes.data, connections],
  );
  const columns = useMemo<ColumnDef<Node>[]>(
    () => [
      {
        accessorKey: "name",
        header: "名称",
        enableSorting: false,
        cell: ({ row }) => (
          <a
            href={`/node?focus=${row.original.id}`}
            className="font-medium text-ink hover:underline"
          >
            {row.original.name}
          </a>
        ),
      },
      {
        id: "mode",
        header: "模式",
        cell: ({ row }) => (
          <span className="font-mono">
            {row.original.forwardMode === "nftables" ? "NFT" : "GOST"}
          </span>
        ),
      },
      {
        id: "status",
        header: "状态",
        cell: ({ row }) => (
          <div className="flex w-max items-center gap-1">
            <StatusPill
              {...nodeStatus({ status: nodeOnline(row.original, connections) ? 1 : 0 })}
            />
            {row.original.syncState === "failed" && <StatusPill {...syncStatus("failed")} />}
          </div>
        ),
      },
      {
        id: "cpu",
        header: "CPU",
        meta: { align: "right", numeric: true },
        cell: ({ row }) => <NodeLiveMetric node={row.original} field="cpuUsage" />,
      },
      {
        id: "memory",
        header: "内存",
        meta: { align: "right", numeric: true },
        cell: ({ row }) => <NodeLiveMetric node={row.original} field="memoryUsage" />,
      },
      {
        id: "version",
        header: "版本",
        cell: ({ row }) => (
          <span className="inline-flex w-max items-center gap-2 whitespace-nowrap font-mono">
            {row.original.version || "—"}
            {row.original.upgradeAvailable && <StatusPill tone="warning" label="可升级" />}
          </span>
        ),
      },
    ],
    [connections],
  );
  return (
    <Section title="节点状态" action={<ViewLink href="/node">查看全部</ViewLink>}>
      <DataTable
        columns={columns}
        data={data}
        getRowId={(node) => String(node.id)}
        loading={nodes.isPending}
        error={nodes.error}
        onRetry={() => {
          void nodes.refetch();
        }}
        ariaLabel="节点状态"
        emptyState={
          <EmptyState
            title="暂无节点"
            description="添加节点以连接转发资源"
            action={
              <Button asChild variant="outline">
                <a href="/node?create=1">添加节点</a>
              </Button>
            }
          />
        }
        renderMobileCard={(node) => (
          <div className="space-y-3">
            <a href={`/node?focus=${node.id}`} className="font-medium text-ink">
              {node.name}
            </a>
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-mono text-caption text-body">
                {node.forwardMode === "nftables" ? "NFT" : "GOST"}
              </span>
              <StatusPill {...nodeStatus({ status: nodeOnline(node, connections) ? 1 : 0 })} />
              {node.syncState === "failed" && <StatusPill {...syncStatus("failed")} />}
            </div>
            <dl className="grid grid-cols-2 gap-3 text-caption text-body">
              <div>
                <dt className="text-mute">CPU</dt>
                <dd>
                  <NodeLiveMetric node={node} field="cpuUsage" />
                </dd>
              </div>
              <div>
                <dt className="text-mute">内存</dt>
                <dd>
                  <NodeLiveMetric node={node} field="memoryUsage" />
                </dd>
              </div>
            </dl>
            <div className="flex flex-wrap items-center gap-2 font-mono text-caption text-body">
              {node.version || "—"}
              {node.upgradeAvailable && <StatusPill tone="warning" label="可升级" />}
            </div>
          </div>
        )}
      />
    </Section>
  );
}
export function AttentionSection({ queries, connections }: Props) {
  const [expanded, setExpanded] = useState(false);
  const { nodes, forwards, users } = queries;
  const items = useMemo(
    () =>
      buildAttentionItems({
        nodes: nodes.data,
        forwards: forwards.data,
        users: users.data,
        connections,
      }),
    [nodes.data, forwards.data, users.data, connections],
  );
  const sources = [
    { label: "节点", query: nodes },
    { label: "转发", query: forwards },
    { label: "用户", query: users },
  ];
  const pending = sources.some((source) => source.query.isPending);
  const errors = sources.filter((source) => source.query.error);
  return (
    <Section title={`需要关注 (${items.length})`}>
      <div className="space-y-3">
        {errors.map(({ label, query }) => (
          <div key={label}>
            <p className="mb-2 text-caption text-mute">{label}关注项</p>
            <ErrorState
              error={query.error}
              onRetry={() => {
                void query.refetch();
              }}
            />
          </div>
        ))}
        {pending && <LoadingRows rows={3} />}
        {(expanded ? items : items.slice(0, 8)).map((item) => (
          <a
            key={item.id}
            href={item.href}
            className="flex min-h-10 items-center justify-between gap-2 rounded-sm px-1 hover:bg-surface-hover"
          >
            <StatusPill
              tone={item.tone}
              label={item.label}
              icon={item.icon}
              className="h-auto min-h-5.5 max-w-full whitespace-normal py-1"
            />
            <ArrowRight aria-hidden="true" className="size-3 shrink-0 text-mute" />
          </a>
        ))}
        {!pending && !errors.length && !items.length && (
          <div className="py-8 text-center">
            <StatusPill tone="good" label="一切正常" />
          </div>
        )}
        {items.length > 8 && (
          <Button variant="ghost" onClick={() => setExpanded((value) => !value)}>
            {expanded ? "收起关注项" : `查看全部 ${items.length} 条`}
          </Button>
        )}
      </div>
    </Section>
  );
}
