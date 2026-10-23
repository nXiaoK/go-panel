import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import type { ColumnDef } from "@tanstack/react-table";
import {
  LoaderCircle,
  MoreHorizontal,
  Pencil,
  Plus,
  RefreshCw,
  Stethoscope,
  Trash2,
} from "lucide-react";
import { PageHeader } from "@/components/patterns/page-header";
import { TableToolbar } from "@/components/patterns/table-toolbar";
import { DataTable } from "@/components/patterns/data-table";
import { DiagnosisSheet } from "@/components/patterns/diagnosis-sheet";
import { EmptyState, ErrorState, LoadingRows } from "@/components/patterns/feedback";
import { FilterSelect } from "@/components/patterns/filter-select";
import { SegmentedControl } from "@/components/patterns/segmented-control";
import { StatusPill } from "@/components/patterns/status-pill";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";
import { useConfirm } from "@/hooks/use-confirm";
import { usePreferences } from "@/hooks/use-preferences";
import { useSession } from "@/hooks/use-session";
import { useUrlState } from "@/hooks/use-url-state";
import { getTunnelList } from "@/lib/api";
import { errorMessage } from "@/lib/api/client";
import { notify } from "@/lib/notify";
import { invalidateFor, resourceKeys } from "@/lib/query-keys";
import { nodeStatus } from "@/lib/status";
import type { Node, NodeStatusCheck, NodeWrite } from "@/lib/types";
import { checkNodeStatus, createNode, deleteNode, updateNode, upgradeNode } from "./api";
import {
  emptyNodeForm,
  readNodeView,
  saveNodeView,
  validateNodeForm,
  type NodeForm,
} from "./model";
import { NodeCard, NodeLiveMetric, NodeSync, NodeVersion } from "./node-card";
import { NodeCommand, type NodeCommandTarget } from "./node-command";
import { NodeFormSheet } from "./node-form-sheet";
import { useNodeMutation, useNodes } from "./queries";
import { useNodeConnections } from "./use-node-live";
import { useNodeSocket } from "./use-node-socket";

const defaults = {
  q: "",
  status: "",
  mode: "",
  focus: undefined as number | undefined,
  create: undefined as number | undefined,
  action: undefined as string | undefined,
};
export function NodePage() {
  const client = useQueryClient();
  const confirm = useConfirm();
  const { isAdmin } = useSession();
  const { autoRefresh } = usePreferences();
  const { state, setState } = useUrlState(defaults);
  const nodesQuery = useNodes({ refetchInterval: autoRefresh ? 30_000 : false });
  useNodeSocket();
  const connections = useNodeConnections();
  const nodes = useMemo(() => nodesQuery.data ?? [], [nodesQuery.data]);
  const online = (node: Node) =>
    (connections.get(node.id) ?? (node.status === 1 ? "online" : "offline")) === "online";
  const [view, setView] = useState(readNodeView);
  const [formOpen, setFormOpen] = useState(false);
  const [form, setForm] = useState<NodeForm>(emptyNodeForm);
  const [initialForm, setInitialForm] = useState<NodeForm>(form);
  const [errors, setErrors] = useState<Partial<Record<keyof NodeForm, string>>>({});
  const [command, setCommand] = useState<NodeCommandTarget | null>(null);
  const [upgrading, setUpgrading] = useState<Set<number>>(new Set());
  const upgradingRef = useRef(new Set<number>());
  const [diagnosis, setDiagnosis] = useState<{
    node: Node;
    loading: boolean;
    data?: NodeStatusCheck;
    error?: unknown;
  } | null>(null);
  const diagnosisRequest = useRef(0);
  const lastFocus = useRef<number | undefined>(undefined);
  const saveMutation = useNodeMutation("node.update", {
    mutationFn: ({ payload, editing }: { payload: NodeWrite; editing: boolean }) =>
      editing ? updateNode(payload) : createNode(payload),
    onSuccess: () => {
      notify.success(form.id ? "已更新节点" : "已添加节点");
      setFormOpen(false);
    },
  });
  const deleteMutation = useNodeMutation("node.delete", {
    mutationFn: (id: number) => deleteNode(id),
    onSuccess: () => notify.success("已删除节点"),
    onError: (error) => notify.error(errorMessage(error)),
  });
  const resetSaveMutation = saveMutation.reset;
  const openAdd = useCallback(() => {
    resetSaveMutation();
    const next = emptyNodeForm();
    setForm(next);
    setInitialForm(next);
    setErrors({});
    setFormOpen(true);
  }, [resetSaveMutation]);
  function openEdit(node: Node) {
    saveMutation.reset();
    const next: NodeForm = {
      id: node.id,
      name: node.name,
      serverIp: node.serverIp ?? "",
      ipString: (node.ip ?? "")
        .split(",")
        .map((value) => value.trim())
        .filter(Boolean)
        .join("\n"),
      portSta: node.portSta ?? 1000,
      portEnd: node.portEnd ?? 65535,
      http: node.http ?? 0,
      tls: node.tls ?? 0,
      socks: node.socks ?? 0,
      forwardMode: node.forwardMode === "nftables" ? "nftables" : "gost",
    };
    setForm(next);
    setInitialForm(next);
    setErrors({});
    setFormOpen(true);
  }
  useEffect(() => {
    if (state.create !== 1 && state.action !== "create") return;
    if (isAdmin) openAdd();
    void setState({ create: undefined, action: undefined });
  }, [state.create, state.action, isAdmin, setState, openAdd]);
  useEffect(() => {
    if (!state.focus) {
      lastFocus.current = undefined;
      return;
    }
    if (lastFocus.current === state.focus) return;
    lastFocus.current = state.focus;
    if (state.q || state.status || state.mode) void setState({ q: "", status: "", mode: "" });
  }, [state.focus, state.q, state.status, state.mode, setState]);
  async function submit() {
    const nextErrors = validateNodeForm(form);
    setErrors(nextErrors);
    if (Object.keys(nextErrors).length) return;
    const payload: NodeWrite = {
      name: form.name.trim(),
      serverIp: form.serverIp.trim(),
      ip: form.ipString
        .split("\n")
        .map((value) => value.trim())
        .filter(Boolean)
        .join(","),
      portSta: form.portSta,
      portEnd: form.portEnd,
      http: form.http,
      tls: form.tls,
      socks: form.socks,
      forwardMode: form.forwardMode,
      ...(form.id ? { id: form.id } : {}),
    };
    try {
      await saveMutation.mutateAsync({ payload, editing: form.id !== null });
    } catch (error) {
      notify.error(errorMessage(error));
    }
  }
  async function remove(node: Node) {
    try {
      const tunnels = await client.ensureQueryData({
        queryKey: resourceKeys.tunnel.list(),
        queryFn: ({ signal }) => getTunnelList({ signal }),
      });
      const count = tunnels.filter((tunnel) =>
        [
          tunnel.inNodeId,
          tunnel.relayNodeId,
          tunnel.outNodeId,
          tunnel.entryNodeId,
          tunnel.exitNodeId,
        ].includes(node.id),
      ).length;
      if (
        await confirm({
          title: `删除节点「${node.name}」`,
          description: `依赖该节点的 ${count} 条隧道将无法工作。节点连接配置将被移除，此操作无法恢复。后端仍会检查现有依赖。`,
          tone: "danger",
          requireText: node.name,
          confirmText: "删除节点",
        })
      )
        deleteMutation.mutate(node.id);
    } catch (error) {
      notify.error(errorMessage(error));
    }
  }
  async function runUpgrade(node: Node) {
    if (!online(node) || !node.upgradeAvailable || upgradingRef.current.has(node.id)) return;
    if (
      !(await confirm({
        title: `升级节点「${node.name}」`,
        description: `升级到 ${node.latestVersion || "最新版本"} 后，节点会自动重启并短暂离线，期间转发可能中断。`,
        confirmText: "升级节点",
      }))
    )
      return;
    if (upgradingRef.current.has(node.id)) return;
    upgradingRef.current.add(node.id);
    setUpgrading(new Set(upgradingRef.current));
    try {
      await notify.promise(upgradeNode(node.id), {
        loading: "升级中…",
        success: "升级已触发，等待节点重连",
        error: errorMessage,
      });
      void invalidateFor(client, "node.upgrade");
    } catch {
      /* notify.promise 已显示后端错误。 */
    } finally {
      upgradingRef.current.delete(node.id);
      setUpgrading(new Set(upgradingRef.current));
    }
  }
  async function runDiagnosis(node: Node) {
    const request = ++diagnosisRequest.current;
    setDiagnosis({ node, loading: true });
    try {
      const data = await checkNodeStatus(node.id);
      if (request === diagnosisRequest.current) setDiagnosis({ node, loading: false, data });
    } catch (error) {
      if (request === diagnosisRequest.current) setDiagnosis({ node, loading: false, error });
    }
  }
  function actions(node: Node, compact = false) {
    const offline = !online(node);
    const pendingUpgrade = upgrading.has(node.id);
    return (
      <div
        className={compact ? "flex w-max items-center gap-2" : "flex flex-wrap items-center gap-2"}
      >
        {!compact && (
          <Button
            type="button"
            size="sm"
            variant="outline"
            onClick={() => setCommand({ node, kind: "install" })}
          >
            安装命令
          </Button>
        )}
        {!compact && node.upgradeAvailable && (
          <TooltipProvider>
            <Tooltip>
              <TooltipTrigger asChild>
                <span tabIndex={offline ? 0 : undefined}>
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    loading={pendingUpgrade}
                    disabled={offline}
                    onClick={() => void runUpgrade(node)}
                  >
                    升级
                  </Button>
                </span>
              </TooltipTrigger>
              {offline && <TooltipContent>节点离线，无法远程升级</TooltipContent>}
            </Tooltip>
          </TooltipProvider>
        )}
        <Button type="button" size="sm" variant="ghost" onClick={() => void runDiagnosis(node)}>
          检测
        </Button>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              aria-label={`节点操作：${node.name}`}
            >
              <MoreHorizontal aria-hidden="true" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            {compact && (
              <>
                <DropdownMenuItem onClick={() => setCommand({ node, kind: "install" })}>
                  安装命令
                </DropdownMenuItem>
                {node.upgradeAvailable && (
                  <>
                    <DropdownMenuItem
                      aria-label="升级"
                      aria-busy={pendingUpgrade || undefined}
                      aria-describedby={offline ? `node-upgrade-reason-${node.id}` : undefined}
                      disabled={offline || pendingUpgrade}
                      onClick={() => void runUpgrade(node)}
                    >
                      {pendingUpgrade && (
                        <LoaderCircle aria-hidden="true" className="animate-spin" />
                      )}
                      {pendingUpgrade ? "升级中…" : "升级"}
                    </DropdownMenuItem>
                    {offline && (
                      <p
                        id={`node-upgrade-reason-${node.id}`}
                        className="px-2 pb-2 text-caption text-mute"
                      >
                        节点离线，无法远程升级
                      </p>
                    )}
                  </>
                )}
                <DropdownMenuSeparator />
              </>
            )}
            <DropdownMenuItem onClick={() => openEdit(node)}>
              <Pencil aria-hidden="true" />
              编辑节点
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => void runDiagnosis(node)}>
              <Stethoscope aria-hidden="true" />
              检测状态
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => setCommand({ node, kind: "uninstall" })}>
              卸载命令
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem
              disabled={deleteMutation.isPending}
              className="text-status-critical-text focus:text-status-critical-text"
              onClick={() => void remove(node)}
            >
              <Trash2 aria-hidden="true" />
              删除节点
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    );
  }
  const query = (state.q ?? "").trim().toLowerCase();
  const filtered = Boolean(query || state.status || state.mode);
  const visible = nodes
    .filter(
      (node) =>
        (!query ||
          [node.name, node.serverIp, node.ip, node.version].some((value) =>
            value?.toLowerCase().includes(query),
          )) &&
        (!state.status ||
          (state.status === "offline"
            ? !online(node)
            : state.status === "online"
              ? online(node)
              : state.status === "failed"
                ? node.syncState === "failed"
                : true)) &&
        (!state.mode || (node.forwardMode || "gost") === state.mode),
    )
    .sort(
      (a, b) =>
        Number(!online(b) || b.syncState === "failed") -
          Number(!online(a) || a.syncState === "failed") || a.name.localeCompare(b.name, "zh-CN"),
    );
  const columns: ColumnDef<Node>[] = [
    {
      accessorKey: "name",
      header: "名称",
      cell: ({ row }) => <span className="text-body-strong text-ink">{row.original.name}</span>,
    },
    {
      accessorKey: "forwardMode",
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
      cell: ({ row }) => <StatusPill {...nodeStatus({ status: online(row.original) ? 1 : 0 })} />,
    },
    {
      accessorKey: "serverIp",
      header: "IP",
      cell: ({ row }) => <span className="font-mono">{row.original.serverIp || "—"}</span>,
    },
    {
      id: "ports",
      header: "端口范围",
      meta: { align: "right", numeric: true },
      cell: ({ row }) => `${row.original.portSta ?? "—"}–${row.original.portEnd ?? "—"}`,
    },
    ...(
      [
        { key: "cpuUsage", title: "CPU" },
        { key: "memoryUsage", title: "内存" },
        { key: "uploadSpeed", title: "上行速率" },
        { key: "downloadSpeed", title: "下行速率" },
      ] as const
    ).map(({ key, title }) => ({
      id: key,
      header: title,
      meta: { align: "right" as const, numeric: true },
      cell: ({ row }: { row: { original: Node } }) => (
        <NodeLiveMetric node={row.original} field={key} />
      ),
    })),
    {
      accessorKey: "version",
      header: "版本",
      cell: ({ row }) => <NodeVersion node={row.original} compact />,
    },
    { id: "sync", header: "同步", cell: ({ row }) => <NodeSync node={row.original} /> },
    {
      id: "actions",
      header: "操作",
      meta: { actions: true },
      cell: ({ row }) => actions(row.original, true),
    },
  ];
  const clearFilters = () => {
    void setState({ q: "", status: "", mode: "" });
  };
  if (!isAdmin) return <EmptyState title="无权访问节点" description="节点管理仅对管理员开放" />;
  return (
    <div className="min-w-0 space-y-6 [&_td]:whitespace-nowrap [&_th]:whitespace-nowrap">
      <PageHeader
        title="节点"
        description="管理节点与实时运行状态"
        actions={
          <>
            <Button
              type="button"
              variant="outline"
              loading={nodesQuery.isFetching}
              onClick={() => void nodesQuery.refetch()}
            >
              <RefreshCw aria-hidden="true" />
              刷新
            </Button>
            <Button type="button" onClick={openAdd}>
              <Plus aria-hidden="true" />
              添加节点
            </Button>
          </>
        }
      />
      <TableToolbar
        searchValue={state.q ?? ""}
        onSearchChange={(q) => void setState({ q, focus: undefined })}
        searchPlaceholder="名称 / IP / 版本"
        filters={
          <>
            <FilterSelect
              label="节点状态"
              value={state.status ?? ""}
              onChange={(status) => void setState({ status, focus: undefined })}
              options={[
                { value: "", label: "全部状态" },
                { value: "online", label: "在线" },
                { value: "offline", label: "离线" },
                { value: "failed", label: "同步失败" },
              ]}
            />
            <FilterSelect
              label="转发模式"
              value={state.mode ?? ""}
              onChange={(mode) => void setState({ mode, focus: undefined })}
              options={[
                { value: "", label: "全部模式" },
                { value: "gost", label: "GOST" },
                { value: "nftables", label: "nftables" },
              ]}
            />
          </>
        }
        count={
          <>
            <span>
              {visible.length} 个 / 共 {nodes.length} 个
            </span>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => void setState({ status: "offline", focus: undefined })}
            >
              离线 {nodes.filter((node) => !online(node)).length}
            </Button>
          </>
        }
        viewSwitch={
          <SegmentedControl
            label="节点视图"
            value={view}
            onChange={(next) => {
              setView(next);
              saveNodeView(next);
            }}
            options={[
              { value: "grid", label: "网格" },
              { value: "list", label: "列表" },
            ]}
          />
        }
      />
      {view === "list" ? (
        <DataTable
          columns={columns}
          data={visible}
          getRowId={(node) => String(node.id)}
          loading={nodesQuery.isPending}
          error={nodesQuery.error}
          onRetry={() => void nodesQuery.refetch()}
          filtered={filtered}
          onClearFilters={clearFilters}
          focusRowId={String(state.focus ?? "")}
          ariaLabel="节点列表"
          renderMobileCard={(node) => (
            <NodeCard node={node} actions={actions(node)} framed={false} />
          )}
        />
      ) : nodesQuery.isPending ? (
        <LoadingRows />
      ) : nodesQuery.error ? (
        <ErrorState error={nodesQuery.error} onRetry={() => void nodesQuery.refetch()} />
      ) : !visible.length ? (
        <EmptyState
          title="暂无节点"
          description="添加节点后查看运行状态"
          filtered={filtered}
          onClearFilters={clearFilters}
          action={
            <Button type="button" onClick={openAdd}>
              添加节点
            </Button>
          }
        />
      ) : (
        <div
          aria-label="节点网格"
          className="grid items-start gap-4 lg:grid-cols-2 2xl:grid-cols-3"
        >
          {visible.map((node) => (
            <NodeCard
              key={node.id}
              node={node}
              focus={state.focus === node.id}
              actions={actions(node)}
            />
          ))}
        </div>
      )}
      <NodeFormSheet
        open={formOpen}
        onOpenChange={setFormOpen}
        form={form}
        setForm={setForm}
        errors={errors}
        online={Boolean(nodes.find((node) => node.id === form.id && online(node)))}
        saving={saveMutation.isPending}
        error={saveMutation.error}
        dirty={JSON.stringify(form) !== JSON.stringify(initialForm)}
        onSubmit={submit}
      />
      <NodeCommand
        key={command ? `${command.node.id}-${command.kind}` : "closed"}
        target={command}
        onClose={() => setCommand(null)}
      />
      <DiagnosisSheet
        open={Boolean(diagnosis)}
        onOpenChange={(open) => {
          if (!open) {
            diagnosisRequest.current++;
            setDiagnosis(null);
          }
        }}
        title={`节点检测${diagnosis ? ` · ${diagnosis.node.name}` : ""}`}
        data={diagnosis?.data}
        error={diagnosis?.error}
        loading={diagnosis?.loading}
        onRetry={diagnosis ? () => void runDiagnosis(diagnosis.node) : undefined}
      />
    </div>
  );
}
