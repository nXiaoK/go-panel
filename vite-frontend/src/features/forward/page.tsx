import { PageHeader } from "@/components/patterns/page-header";
import { DataTable } from "@/components/patterns/data-table";
import { DiagnosisSheet } from "@/components/patterns/diagnosis-sheet";
import { EmptyState, ErrorState } from "@/components/patterns/feedback";
import { StatusPill } from "@/components/patterns/status-pill";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { ForwardFormDialog } from "./components/forward-form-dialog";
import { NftDetectDialog } from "./components/nft-import-dialog";
import { exitModeLabel, isTunnelForward } from "./exit-members";
import { moveForwardByID, type MoveDirection } from "./order";
import { splitTargetAddresses } from "./model";
import { useNodes } from "@/features/node/queries";
import { useForwardTunnels } from "@/features/tunnel/queries";
import { useConfirm } from "@/hooks/use-confirm";
import { usePreferences } from "@/hooks/use-preferences";
import { useSession } from "@/hooks/use-session";
import { useUrlState } from "@/hooks/use-url-state";
import {
  deleteForward,
  diagnoseForward,
  forceDeleteForward,
  pauseForwardService,
  resumeForwardService,
  updateForwardOrder,
} from "@/lib/api";
import { ApiError, errorMessage } from "@/lib/api/client";
import { runBatch, type BatchResult } from "@/lib/batch";
import { formatBytes } from "@/lib/format";
import { notify } from "@/lib/notify";
import { invalidateFor, resourceKeys } from "@/lib/query-keys";
import { forwardStatus } from "@/lib/status";
import type { Forward, ForwardTunnelOption } from "@/lib/types";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { ColumnDef, RowSelectionState, SortingState } from "@tanstack/react-table";
import {
  ArrowDown,
  ArrowDownToLine,
  ArrowUp,
  ArrowUpToLine,
  Copy,
  GripVertical,
  MoreHorizontal,
  Pause,
  Pencil,
  Play,
  Plus,
  RefreshCw,
  Scan,
  Search,
  Stethoscope,
  Trash2,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { useForwardMutation, useForwards } from "./queries";

// URL 默认不筛选并使用后端 inx 自定义顺序；仅列表导航写入查询参数，不持久化到本地存储。
const URL_DEFAULTS: {
  q: string;
  tunnel: string | number;
  status: string;
  sort: string;
  focus?: number;
  create?: number;
  action?: string;
} = { q: "", tunnel: "all", status: "all", sort: "" };
const SORT_LABELS: Record<string, string> = { name: "名称", traffic: "流量" };
type BatchAction = "暂停" | "启动" | "删除";

export function ForwardPage() {
  const queryClient = useQueryClient();
  const requestConfirm = useConfirm();
  const { isAdmin } = useSession();
  const { autoRefresh } = usePreferences();
  const { state, setState } = useUrlState(URL_DEFAULTS);
  const query = state.q ?? "";
  const tunnelFilter = String(state.tunnel ?? "all");
  const statusFilter = state.status ?? "all";
  const [formOpen, setFormOpen] = useState(false);
  const [editing, setEditing] = useState<Forward | null>(null);
  const [nftOpen, setNftOpen] = useState(false);
  const [diagnosing, setDiagnosing] = useState<Forward | null>(null);
  const [orderDraft, setOrderDraft] = useState<Forward[] | null>(null);
  const [orderError, setOrderError] = useState<unknown>(null);
  const [selection, setSelection] = useState<RowSelectionState>({});
  const [batchProgress, setBatchProgress] = useState<{
    action: BatchAction;
    done: number;
    total: number;
  } | null>(null);
  const [batchSummary, setBatchSummary] = useState<{
    action: BatchAction;
    results: BatchResult<Forward>[];
  } | null>(null);
  const forwardsQuery = useForwards({
    refetchInterval: autoRefresh && !orderDraft ? 30_000 : false,
  });
  const tunnelsQuery = useForwardTunnels();
  const nodesQuery = useNodes();
  const forwards = useMemo(() => forwardsQuery.data ?? [], [forwardsQuery.data]);
  const tunnels = useMemo(() => tunnelsQuery.data ?? [], [tunnelsQuery.data]);
  const nodes = useMemo(() => nodesQuery.data ?? [], [nodesQuery.data]);
  const tunnelMap = useMemo(
    () => new Map<number, ForwardTunnelOption>(tunnels.map((t) => [t.id, t])),
    [tunnels],
  );
  const errorCount = forwards.filter((f) => f.status === -1).length;
  const clearFilters = useCallback(
    () => void setState({ q: "", tunnel: "all", status: "all", sort: "" }),
    [setState],
  );
  useEffect(() => {
    if (state.focus) {
      void setState({ q: "", tunnel: "all", status: "all", sort: "" });
      setOrderDraft(null);
    }
  }, [state.focus, setState]);
  useEffect(() => {
    if (state.create === 1 || state.action === "create") {
      setEditing(null);
      setFormOpen(true);
      void setState({ create: undefined, action: undefined });
    }
  }, [state.create, state.action, setState]);
  const refresh = useCallback(() => {
    void invalidateFor(queryClient, "forward.status");
    void queryClient.invalidateQueries({
      queryKey: isAdmin ? resourceKeys.tunnel.list() : resourceKeys.tunnel.userOptions(),
    });
    if (isAdmin) void queryClient.invalidateQueries({ queryKey: resourceKeys.node.list() });
  }, [queryClient, isAdmin]);
  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    return forwards.filter((f) => {
      if (tunnelFilter !== "all" && String(f.tunnelId) !== tunnelFilter) return false;
      if (statusFilter === "1" && f.status !== 1) return false;
      if (statusFilter === "0" && f.status !== 0) return false;
      if (statusFilter === "error" && f.status !== -1) return false;
      return (
        !q ||
        [
          f.name,
          f.id,
          f.inPort,
          f.remoteAddr,
          f.tunnelName || tunnelMap.get(f.tunnelId)?.name,
          f.userName,
        ].some((value) =>
          String(value ?? "")
            .toLowerCase()
            .includes(q),
        )
      );
    });
  }, [forwards, query, tunnelFilter, statusFilter, tunnelMap]);
  const [sortID, sortDirection] = (state.sort ?? "").split(".");
  const sorting: SortingState =
    !orderDraft && SORT_LABELS[sortID] ? [{ id: sortID, desc: sortDirection === "desc" }] : [];
  const orderMutation = useForwardMutation("forward.order", {
    mutationFn: (rows: Forward[]) =>
      updateForwardOrder({ forwards: rows.map((row, index) => ({ id: row.id, inx: index + 1 })) }),
  });
  const diagnosis = useMutation({ mutationFn: (id: number) => diagnoseForward(id) });
  const openCreate = () => {
    setEditing(null);
    setFormOpen(true);
  };
  const openEdit = (row: Forward) => {
    setEditing(row);
    setFormOpen(true);
  };
  const copy = async (value: string) => {
    try {
      await navigator.clipboard.writeText(value);
      notify.success("地址已复制");
    } catch {
      notify.error("复制失败");
    }
  };
  const remove = async (row: Forward) => {
    if (
      !(await requestConfirm({
        title: `删除转发「${row.name}」`,
        description: "删除后入口端口将立即停止转发，且无法恢复。",
        confirmText: "删除转发",
        tone: "danger",
      }))
    )
      return;
    try {
      await deleteForward(row.id);
      notify.success("已删除");
      await invalidateFor(queryClient, "forward.delete");
    } catch (error) {
      if (
        !(error instanceof ApiError) ||
        error.httpStatus !== 200 ||
        [401, 4031, 4032].includes(error.code)
      ) {
        notify.error(errorMessage(error));
        return;
      }
      if (
        await requestConfirm({
          title: `强制删除转发「${row.name}」`,
          description: `删除失败：${errorMessage(error)}。强制删除会移除面板记录；即使显示成功，节点仍可能残留转发规则，需要检查并手动清理。`,
          confirmText: "强制删除",
          tone: "danger",
          requireText: row.name,
        })
      ) {
        try {
          await forceDeleteForward(row.id);
          notify.success("已强制删除");
          await invalidateFor(queryClient, "forward.delete");
        } catch (forcedError) {
          notify.error(errorMessage(forcedError));
        }
      }
    }
  };
  const togglePause = async (row: Forward) => {
    const patch = (status: number) =>
      queryClient.setQueryData<Forward[]>(resourceKeys.forward.list(), (prev) =>
        (prev ?? []).map((f) => (f.id === row.id ? { ...f, status } : f)),
      );
    patch(row.status === 1 ? 0 : 1);
    try {
      await (row.status === 1 ? pauseForwardService(row.id) : resumeForwardService(row.id));
      notify.success(row.status === 1 ? "已暂停" : "已启动");
      await invalidateFor(queryClient, "forward.status");
    } catch (error) {
      patch(row.status);
      notify.error(errorMessage(error));
    }
  };
  const runDiagnose = (row: Forward) => {
    setDiagnosing(row);
    diagnosis.mutate(row.id);
  };
  const batch = async (rows: Forward[], action: BatchAction) => {
    if (batchProgress) return;
    if (
      action === "删除" &&
      !(await requestConfirm({
        title: `删除 ${rows.length} 条转发`,
        description: `将删除 ${rows.length} 条转发，入口端口会立即停止转发，且无法恢复。`,
        confirmText: "删除转发",
        tone: "danger",
      }))
    )
      return;
    setBatchProgress({ action, done: 0, total: rows.length });
    const results = await runBatch(
      rows,
      (row) =>
        action === "删除"
          ? deleteForward(row.id)
          : action === "暂停"
            ? pauseForwardService(row.id)
            : resumeForwardService(row.id),
      {
        nameOf: (row) => `${row.name} #${row.id}`,
        onProgress: ({ done, total }) => setBatchProgress({ action, done, total }),
      },
    );
    await invalidateFor(queryClient, action === "删除" ? "forward.delete" : "forward.status");
    setSelection({});
    setBatchProgress(null);
    setBatchSummary({ action, results });
  };
  const saveOrder = async () => {
    if (!orderDraft) return;
    try {
      await orderMutation.mutateAsync(orderDraft);
      setOrderDraft(null);
      setOrderError(null);
      notify.success("顺序已保存");
    } catch (error) {
      setOrderDraft(null);
      setOrderError(error);
    }
  };
  const reorder = (row: Forward, direction: MoveDirection) =>
    setOrderDraft((current) => (current ? moveForwardByID(current, row.id, direction) : current));
  const entry = (row: Forward) =>
    `${row.inIp ?? tunnelMap.get(row.tunnelId)?.inIp ?? "—"}:${row.inPort}`;
  const tunnelDescription = (row: Forward) => {
    const tunnel = tunnelMap.get(row.tunnelId);
    return `${isTunnelForward(tunnel) ? "隧道转发" : "端口转发"}${isTunnelForward(tunnel) ? ` · ${exitModeLabel(row.exitMode || "single")}` : ""}`;
  };
  const targets = (row: Forward) => {
    const addresses = splitTargetAddresses(row.remoteAddr || "");
    return (
      <div className="max-w-56 break-all">
        <div className="font-mono text-caption text-body">{addresses[0] || "—"}</div>
        {addresses.length > 1 && (
          <div className="text-caption text-mute">+{addresses.length - 1} 个目标</div>
        )}
        {row.targetMode === "manual" && row.activeRemoteAddr && (
          <div className="mt-1 font-mono text-caption text-mute">当前 {row.activeRemoteAddr}</div>
        )}
      </div>
    );
  };
  const actions = (row: Forward) => (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          aria-label={`转发操作：${row.name}`}
          disabled={Boolean(batchProgress)}
        >
          <MoreHorizontal className="size-4" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem onClick={() => openEdit(row)}>
          <Pencil className="size-4" />
          编辑
        </DropdownMenuItem>
        <DropdownMenuItem onClick={() => void togglePause(row)}>
          {row.status === 1 ? <Pause className="size-4" /> : <Play className="size-4" />}
          {row.status === 1 ? "暂停" : "启动"}
        </DropdownMenuItem>
        <DropdownMenuItem onClick={() => runDiagnose(row)}>
          <Stethoscope className="size-4" />
          诊断
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem onClick={() => void copy(entry(row))}>
          <Copy className="size-4" />
          复制入口
        </DropdownMenuItem>
        <DropdownMenuItem onClick={() => void copy(row.remoteAddr)}>
          <Copy className="size-4" />
          复制目标
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem
          className="text-status-critical-text focus:text-status-critical-text"
          onClick={() => void remove(row)}
        >
          <Trash2 className="size-4" />
          删除
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
  const orderControls = (row: Forward) => {
    const index = orderDraft?.findIndex((item) => item.id === row.id) ?? -1;
    return (
      <div className="flex items-center gap-1">
        <GripVertical aria-hidden="true" className="size-4 shrink-0 text-mute" />
        {(
          [
            ["top", "置顶", ArrowUpToLine],
            ["up", "上移", ArrowUp],
            ["down", "下移", ArrowDown],
            ["bottom", "置底", ArrowDownToLine],
          ] as const
        ).map(([direction, label, Icon]) => (
          <Button
            key={direction}
            type="button"
            variant="ghost"
            size="icon"
            aria-label={`${label}：${row.name}`}
            disabled={
              orderMutation.isPending ||
              (direction === "top" || direction === "up"
                ? index <= 0
                : index >= (orderDraft?.length ?? 0) - 1)
            }
            onClick={() => reorder(row, direction)}
          >
            <Icon className="size-4" />
          </Button>
        ))}
      </div>
    );
  };
  const columns: ColumnDef<Forward>[] = [
    ...(orderDraft
      ? [
          {
            id: "order",
            header: "调整顺序",
            cell: ({ row }) => orderControls(row.original),
          } satisfies ColumnDef<Forward>,
        ]
      : []),
    {
      accessorKey: "name",
      header: "名称",
      enableSorting: !orderDraft,
      cell: ({ row }) => (
        <div className="min-w-28">
          <div className="font-semibold text-ink">{row.original.name}</div>
          <div className="font-mono text-caption text-mute">#{row.original.id}</div>
        </div>
      ),
    },
    {
      id: "tunnel",
      header: "隧道",
      cell: ({ row }) => (
        <div className="min-w-28">
          <div>
            {row.original.tunnelName ||
              tunnelMap.get(row.original.tunnelId)?.name ||
              `#${row.original.tunnelId}`}
          </div>
          <div className="text-caption text-mute">{tunnelDescription(row.original)}</div>
        </div>
      ),
    },
    {
      id: "entry",
      header: "入口",
      cell: ({ row }) => (
        <div className="group/entry flex items-center gap-1">
          <span className="whitespace-nowrap font-mono text-caption">{entry(row.original)}</span>
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="md:opacity-0 md:group-hover/entry:opacity-100 md:focus-visible:opacity-100"
            aria-label={`复制入口：${row.original.name}`}
            onClick={() => void copy(entry(row.original))}
          >
            <Copy className="size-3.5" />
          </Button>
        </div>
      ),
    },
    { id: "target", header: "目标", cell: ({ row }) => targets(row.original) },
    {
      id: "status",
      header: "状态",
      cell: ({ row }) => <StatusPill {...forwardStatus(row.original)} />,
    },
    {
      id: "traffic",
      accessorFn: (row) => (row.inFlow ?? 0) + (row.outFlow ?? 0),
      header: "流量 ↓/↑",
      enableSorting: !orderDraft,
      meta: { align: "right", numeric: true },
      cell: ({ row }) => (
        <div className="whitespace-nowrap text-caption text-body">
          <div>↓ {formatBytes(row.original.inFlow)}</div>
          <div>↑ {formatBytes(row.original.outFlow)}</div>
        </div>
      ),
    },
    ...(isAdmin
      ? [
          {
            id: "user",
            header: "用户",
            cell: ({ row }) => (
              <span className="text-body">
                {row.original.userName || (row.original.userId ? `#${row.original.userId}` : "—")}
              </span>
            ),
          } satisfies ColumnDef<Forward>,
        ]
      : []),
    ...(!orderDraft
      ? [
          {
            id: "actions",
            header: "操作",
            meta: { actions: true, align: "right" as const },
            cell: ({ row }) => actions(row.original),
          } satisfies ColumnDef<Forward>,
        ]
      : []),
  ];
  const hasFilters = Boolean(query || tunnelFilter !== "all" || statusFilter !== "all");
  return (
    <div className="min-w-0 space-y-6">
      <PageHeader
        title="转发规则"
        description="管理端口转发、目标与运行状态"
        actions={
          <>
            <Button
              type="button"
              variant="outline"
              onClick={refresh}
              disabled={Boolean(orderDraft) || forwardsQuery.isFetching}
            >
              <RefreshCw className="size-4" />
              刷新
            </Button>
            {isAdmin && (
              <Button
                type="button"
                variant="outline"
                onClick={() => setNftOpen(true)}
                disabled={Boolean(orderDraft)}
              >
                <Scan className="size-4" />
                识别 NFT 规则
              </Button>
            )}
            <Button type="button" onClick={openCreate} disabled={Boolean(orderDraft)}>
              <Plus className="size-4" />
              新建转发
            </Button>
          </>
        }
      />
      {(tunnelsQuery.error || (isAdmin && nodesQuery.error)) && (
        <ErrorState error={tunnelsQuery.error || nodesQuery.error} onRetry={refresh} />
      )}
      {orderError != null && <ErrorState error={orderError} />}
      {orderDraft ? (
        <div
          role="region"
          aria-label="排序模式"
          className="flex flex-wrap items-center gap-3 rounded-md border border-hairline bg-canvas-soft p-4"
        >
          <p className="mr-auto text-body">使用按钮调整顺序</p>
          <Button
            type="button"
            variant="outline"
            disabled={orderMutation.isPending}
            onClick={() => {
              setOrderDraft(null);
              setOrderError(null);
            }}
          >
            取消
          </Button>
          <Button type="button" loading={orderMutation.isPending} onClick={() => void saveOrder()}>
            保存顺序
          </Button>
        </div>
      ) : (
        <div className="flex min-w-0 flex-wrap items-center gap-3">
          <div className="relative min-w-0 basis-full md:min-w-48 md:flex-1 md:basis-auto">
            <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-mute" />
            <Input
              aria-label="搜索转发"
              placeholder="名称 / ID / 端口 / 目标 / 用户"
              className="pl-9"
              value={query}
              onChange={(event) => void setState({ q: event.target.value, focus: undefined })}
            />
          </div>
          <Select
            value={tunnelFilter}
            onValueChange={(tunnel) =>
              void setState({ tunnel: tunnel === "all" ? "all" : Number(tunnel), focus: undefined })
            }
          >
            <SelectTrigger aria-label="筛选隧道" className="w-40">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">全部隧道</SelectItem>
              {tunnels.map((tunnel) => (
                <SelectItem key={tunnel.id} value={String(tunnel.id)}>
                  {tunnel.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Select
            value={statusFilter}
            onValueChange={(status) => void setState({ status, focus: undefined })}
          >
            <SelectTrigger aria-label="筛选状态" className="w-32">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">全部状态</SelectItem>
              <SelectItem value="1">运行中</SelectItem>
              <SelectItem value="0">已暂停</SelectItem>
              <SelectItem value="error">异常</SelectItem>
            </SelectContent>
          </Select>
          <Button
            type="button"
            variant="outline"
            disabled={!forwards.length || Boolean(batchProgress)}
            onClick={() => {
              clearFilters();
              setSelection({});
              setOrderError(null);
              setOrderDraft(forwards.map((row) => ({ ...row })));
            }}
          >
            调整顺序
          </Button>
          <div className="ml-auto flex items-center gap-2 text-caption text-mute">
            <span>{filtered.length} 条</span>
            <span>·</span>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => void setState({ status: "error", focus: undefined })}
            >
              异常 {errorCount}
            </Button>
          </div>
        </div>
      )}
      {sorting.length > 0 && (
        <p className="flex flex-wrap items-center gap-2 text-caption text-mute">
          已按{SORT_LABELS[sortID]}排序
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => void setState({ sort: "" })}
          >
            恢复自定义顺序
          </Button>
        </p>
      )}
      {batchProgress && (
        <p role="status" aria-live="polite" className="text-body">
          正在{batchProgress.action} {batchProgress.done}/{batchProgress.total}
        </p>
      )}
      <DataTable
        columns={columns}
        data={orderDraft ?? filtered}
        getRowId={(row) => String(row.id)}
        loading={forwardsQuery.isPending}
        error={forwardsQuery.error}
        onRetry={() => void forwardsQuery.refetch()}
        filtered={hasFilters}
        onClearFilters={clearFilters}
        emptyState={
          <EmptyState
            filtered={hasFilters}
            onClearFilters={clearFilters}
            title="还没有转发规则"
            description="新建转发，开始管理入口与目标地址"
            action={
              <Button type="button" onClick={openCreate}>
                新建转发
              </Button>
            }
          />
        }
        enableSelection={!orderDraft}
        rowSelection={selection}
        onRowSelectionChange={setSelection}
        focusRowId={String(state.focus ?? "")}
        sorting={sorting}
        onSortingChange={(updater) => {
          const next = typeof updater === "function" ? updater(sorting) : updater;
          void setState({
            sort: next[0] ? `${next[0].id}.${next[0].desc ? "desc" : "asc"}` : "",
            focus: undefined,
          });
        }}
        ariaLabel="转发规则"
        bulkActions={(rows) => (
          <>
            {(["暂停", "启动", "删除"] as const).map((action) => (
              <Button
                key={action}
                type="button"
                variant={action === "删除" ? "danger" : "outline"}
                disabled={Boolean(batchProgress)}
                onClick={() => void batch(rows, action)}
              >
                {action}
              </Button>
            ))}
          </>
        )}
        renderMobileCard={(row) => (
          <div className="min-w-0 space-y-3">
            <div className="flex min-w-0 items-center gap-2">
              <div className="min-w-0 flex-1">
                <h2 className="break-all font-semibold text-ink">{row.name}</h2>
                <span className="font-mono text-caption text-mute">#{row.id}</span>
              </div>
              <StatusPill {...forwardStatus(row)} />
              {!orderDraft && actions(row)}
            </div>
            <div className="flex min-w-0 items-center gap-2">
              <p className="min-w-0 flex-1 break-all text-caption text-body">
                {row.tunnelName || tunnelMap.get(row.tunnelId)?.name || `#${row.tunnelId}`} ·{" "}
                <span className="font-mono">{entry(row)}</span>
              </p>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                aria-label={`复制入口：${row.name}`}
                onClick={() => void copy(entry(row))}
              >
                <Copy className="size-4" />
              </Button>
            </div>
            <div className="flex gap-2">
              <span aria-hidden="true">→</span>
              {targets(row)}
            </div>
            <p className="font-mono text-caption text-mute">
              ↓ {formatBytes(row.inFlow)} ↑ {formatBytes(row.outFlow)}
              {isAdmin && <span className="font-sans"> · {row.userName || "—"}</span>}
            </p>
            {orderDraft && orderControls(row)}
          </div>
        )}
      />
      <ForwardFormDialog
        mode={isAdmin ? "admin" : "user"}
        open={formOpen}
        onOpenChange={setFormOpen}
        editing={editing}
        tunnels={tunnels}
        nodes={nodes}
        onSaved={() =>
          void invalidateFor(queryClient, editing ? "forward.update" : "forward.create")
        }
      />
      <DiagnosisSheet
        open={Boolean(diagnosing)}
        onOpenChange={(open) => {
          if (!open) setDiagnosing(null);
        }}
        title={`转发诊断${diagnosing ? ` · ${diagnosing.name}` : ""}`}
        data={diagnosis.data}
        loading={diagnosis.isPending}
        error={diagnosis.error}
        onRetry={() => {
          if (diagnosing) diagnosis.mutate(diagnosing.id);
        }}
      />
      <Dialog
        open={Boolean(batchSummary)}
        onOpenChange={(open) => {
          if (!open) setBatchSummary(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>批量{batchSummary?.action}完成</DialogTitle>
            <DialogDescription>
              成功 {batchSummary?.results.filter((result) => result.success).length ?? 0} 条，失败{" "}
              {batchSummary?.results.filter((result) => !result.success).length ?? 0} 条
            </DialogDescription>
          </DialogHeader>
          <ul className="max-h-72 space-y-2 overflow-y-auto">
            {batchSummary?.results
              .filter((result) => !result.success)
              .map((result) => (
                <li key={result.item.id} className="break-words text-sm text-status-critical-text">
                  {result.name}：{result.reason}
                </li>
              ))}
          </ul>
          <Button type="button" variant="outline" onClick={() => setBatchSummary(null)}>
            关闭摘要
          </Button>
        </DialogContent>
      </Dialog>
      {isAdmin && (
        <NftDetectDialog
          open={nftOpen}
          onOpenChange={setNftOpen}
          nodes={nodes}
          onComplete={() => void invalidateFor(queryClient, "forward.create")}
        />
      )}
    </div>
  );
}
