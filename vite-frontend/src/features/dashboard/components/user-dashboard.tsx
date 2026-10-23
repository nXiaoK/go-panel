import { useMemo } from "react";
import { AlertCircle } from "lucide-react";
import type { ColumnDef } from "@tanstack/react-table";
import { StatTile } from "@/components/patterns/stat-tile";
import { StatusPill } from "@/components/patterns/status-pill";
import { DataTable } from "@/components/patterns/data-table";
import { EmptyState, ErrorState, LoadingRows } from "@/components/patterns/feedback";
import { formatBytes, formatDate, formatDateTime } from "@/lib/format";
import { expiryStatus } from "@/lib/status";
import { mergeMyTunnels, type MyTunnel } from "../model";
import type { useDashboardSources } from "../queries";
import { resolveTrafficTotalBytes } from "../traffic";
import { FlowQuota, Section, ViewLink } from "./section";

type Props = { queries: ReturnType<typeof useDashboardSources> };
export function UserStats({ queries }: Props) {
  const { traffic } = queries;
  if (traffic.error)
    return (
      <Section title="账户概况">
        <ErrorState
          error={traffic.error}
          onRetry={() => {
            void traffic.refetch();
          }}
        />
      </Section>
    );
  const account = traffic.data?.userInfo;
  const forwards = traffic.data?.forwards ?? [];
  const used = Number(account?.inFlow || 0) + Number(account?.outFlow || 0);
  const paused = forwards.filter((forward) => forward.status === 0).length;
  return (
    <div className="grid min-w-0 gap-4 sm:grid-cols-2 xl:grid-cols-4" aria-label="账户指标">
      <div className="min-w-0 rounded-md border border-hairline p-5">
        <p className="mb-3 text-caption text-mute">流量配额</p>
        {traffic.isPending ? (
          <LoadingRows rows={1} />
        ) : (
          <FlowQuota used={used} quota={account?.flow ?? 0} label="账户流量配额" />
        )}
        <p className="mt-2 text-caption text-mute">
          {account?.flowResetTime === 0
            ? "不自动重置流量"
            : account?.flowResetTime != null &&
                Number.isInteger(account.flowResetTime) &&
                account.flowResetTime >= 1 &&
                account.flowResetTime <= 31
              ? `每月 ${account.flowResetTime} 日重置`
              : "重置日期未知"}
        </p>
      </div>
      <StatTile
        label="今日流量"
        loading={traffic.isPending}
        value={formatBytes(resolveTrafficTotalBytes(traffic.data?.todayTraffic))}
        hint="当前账号"
      />
      <StatTile
        label="转发"
        loading={traffic.isPending}
        value={`${forwards.filter((forward) => forward.status === 1).length} / ${account?.num ?? 0}`}
        hint={
          <div className="flex flex-wrap items-center gap-2">
            <span>运行中</span>
            {paused > 0 && <ViewLink href="/forward?status=0">{paused} 已暂停</ViewLink>}
          </div>
        }
      />
      <StatTile
        label="账号到期"
        loading={traffic.isPending}
        value={account?.expTime && account.expTime > 0 ? formatDate(account.expTime) : "长期有效"}
        hint={
          account && (
            <span title={formatDateTime(account.expTime)}>
              <StatusPill {...expiryStatus(account.expTime)} />
            </span>
          )
        }
      />
    </div>
  );
}
function TunnelQuota({ row }: { row: MyTunnel }) {
  return row.permission ? (
    <FlowQuota
      used={Number(row.permission.inFlow || 0) + Number(row.permission.outFlow || 0)}
      quota={row.permission.flow}
      label={`${row.name}流量配额`}
    />
  ) : (
    <span className="text-mute">—</span>
  );
}
function TunnelAvailability({ row }: { row: MyTunnel }) {
  return row.unavailableReason ? (
    <StatusPill
      tone="critical"
      label={row.unavailableReason}
      icon={AlertCircle}
      className="h-auto min-h-[22px] whitespace-normal py-1"
    />
  ) : null;
}
export function MyTunnels({ queries }: Props) {
  const { traffic, userTunnels } = queries;
  const data = useMemo(
    () => mergeMyTunnels(traffic.data?.tunnelPermissions ?? [], userTunnels.data ?? []),
    [traffic.data?.tunnelPermissions, userTunnels.data],
  );
  const columns: ColumnDef<MyTunnel>[] = [
    {
      accessorKey: "name",
      header: "隧道",
      enableSorting: false,
      cell: ({ row }) => (
        <div className="space-y-1">
          <p className="font-medium text-ink">{row.original.name}</p>
          <TunnelAvailability row={row.original} />
        </div>
      ),
    },
    {
      accessorKey: "inIp",
      header: "入口地址",
      cell: ({ row }) => <span className="font-mono">{row.original.inIp}</span>,
    },
    {
      accessorKey: "ports",
      header: "端口范围",
      cell: ({ row }) => <span className="whitespace-nowrap font-mono">{row.original.ports}</span>,
    },
    {
      id: "flow",
      header: "流量",
      cell: ({ row }) => (
        <div className="min-w-36">
          <TunnelQuota row={row.original} />
        </div>
      ),
    },
    {
      id: "expiry",
      header: "到期",
      cell: ({ row }) => (
        <span
          title={formatDateTime(row.original.permission?.expTime)}
          className="whitespace-nowrap"
        >
          {row.original.permission?.expTime
            ? formatDate(row.original.permission.expTime)
            : "长期有效"}
        </span>
      ),
    },
    {
      id: "speed",
      header: "限速",
      meta: { align: "right", numeric: true },
      cell: ({ row }) =>
        row.original.permission?.speed != null ? `${row.original.permission.speed} Mbps` : "不限速",
    },
  ];
  return (
    <Section title="我的隧道">
      <DataTable
        columns={columns}
        data={data}
        getRowId={(row) => String(row.id)}
        loading={traffic.isPending || userTunnels.isPending}
        error={traffic.error || userTunnels.error}
        onRetry={() => {
          void traffic.refetch();
          void userTunnels.refetch();
        }}
        ariaLabel="我的隧道"
        emptyState={<EmptyState title="暂无隧道授权" description="联系管理员开通可用隧道" />}
        renderMobileCard={(row) => (
          <div className="space-y-3">
            <p className="font-medium text-ink">{row.name}</p>
            <TunnelAvailability row={row} />
            <dl className="space-y-2 text-caption text-body">
              <div className="flex justify-between gap-4">
                <dt className="shrink-0 text-mute">入口地址</dt>
                <dd className="break-all font-mono">{row.inIp}</dd>
              </div>
              <div className="flex justify-between gap-4">
                <dt className="text-mute">端口范围</dt>
                <dd className="font-mono">{row.ports}</dd>
              </div>
              <div>
                <dt className="mb-2 text-mute">流量</dt>
                <dd>
                  <TunnelQuota row={row} />
                </dd>
              </div>
              <div className="flex justify-between gap-4">
                <dt className="text-mute">到期</dt>
                <dd>{row.permission?.expTime ? formatDate(row.permission.expTime) : "长期有效"}</dd>
              </div>
              <div className="flex justify-between gap-4">
                <dt className="text-mute">限速</dt>
                <dd className="font-mono">
                  {row.permission?.speed != null ? `${row.permission.speed} Mbps` : "不限速"}
                </dd>
              </div>
            </dl>
          </div>
        )}
      />
    </Section>
  );
}
