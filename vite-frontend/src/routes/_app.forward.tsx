import { createFileRoute } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import {
  Activity,
  ArrowDown,
  ArrowUp,
  ChevronsUpDown,
  Copy,
  Loader2,
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
  Zap,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { ScrollArea } from "@/components/ui/scroll-area";
import {
  deleteForward,
  diagnoseForward,
  forceDeleteForward,
  getNodeList,
  getTunnelList,
  pauseForwardService,
  resumeForwardService,
  updateForwardOrder,
} from "@/lib/api";
import type { Forward, ForwardExitMember, ForwardExitMode, Node, Tunnel } from "@/lib/types";
import { listData, queries, unwrap } from "@/lib/api/query";
import { loadSortedForwards } from "@/lib/forward-list";
import { usePreferences } from "@/hooks/use-preferences";
import { canReorderForwards, forwardOrderIDs, moveForwardByID } from "@/lib/forward-order";
import { invalidateGlobalSearch } from "@/lib/search-cache";
import { ForwardFormDialog } from "@/components/forward/forward-form-dialog";
import { NftDetectDialog } from "@/components/forward/nft-import-dialog";
import {
  defaultExitMembers,
  exitModeLabel,
  isTunnelForward,
} from "@/components/forward/exit-members";
import { KpiCard, PageHeader, QueryErrorNotice } from "@/components/page";

export const Route = createFileRoute("/_app/forward")({
  head: () => ({ meta: [{ title: "转发管理 · Flux Panel" }] }),
  component: ForwardPage,
});

function formatFlow(bytes?: number): string {
  if (!bytes || bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let v = bytes;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v >= 10 ? 0 : 1)} ${units[i]}`;
}

function ForwardPage() {
  const queryClient = useQueryClient();
  const [query, setQuery] = useState("");
  const [tunnelFilter, setTunnelFilter] = useState("all");
  const [statusFilter, setStatusFilter] = useState("all");

  const [formOpen, setFormOpen] = useState(false);
  const [editingForward, setEditingForward] = useState<Forward | null>(null);

  const [diagOpen, setDiagOpen] = useState(false);
  const [diagLoading, setDiagLoading] = useState(false);
  const [diagResult, setDiagResult] = useState<any>(null);

  const [nftOpen, setNftOpen] = useState(false);

  const [isAdmin, setIsAdmin] = useState(false);
  useEffect(() => {
    const r = window.localStorage.getItem("role_id");
    setIsAdmin(r === "0");
  }, []);

  const { autoRefresh: autoRefreshEnabled } = usePreferences();

  const forwardsQuery = useQuery({
    queryKey: queries.forward.list(),
    queryFn: () => loadSortedForwards(),
    refetchInterval: autoRefreshEnabled ? 30_000 : false,
  });
  const tunnelsQuery = useQuery({
    queryKey: queries.tunnel.list(),
    queryFn: () => getTunnelList().then(listData<Tunnel>),
  });
  const nodesQuery = useQuery({
    queryKey: queries.node.rawList(),
    queryFn: () => getNodeList().then(listData<Node>),
  });

  const forwards = useMemo(() => forwardsQuery.data ?? [], [forwardsQuery.data]);
  const tunnels = useMemo(() => tunnelsQuery.data ?? [], [tunnelsQuery.data]);
  const nodes = useMemo(() => nodesQuery.data ?? [], [nodesQuery.data]);
  const loading = forwardsQuery.isPending;

  const refresh = useCallback(() => {
    void queryClient.invalidateQueries({ queryKey: queries.forward.list() });
    void queryClient.invalidateQueries({ queryKey: queries.tunnel.list() });
    void queryClient.invalidateQueries({ queryKey: queries.node.list() });
    void queryClient.invalidateQueries({ queryKey: queries.dashboard.all });
  }, [queryClient]);

  const tunnelMap = useMemo(() => {
    const m = new Map<number, Tunnel>();
    tunnels.forEach((t) => m.set(t.id, t));
    return m;
  }, [tunnels]);

  const nodeMap = useMemo(() => {
    const m = new Map<number, Node>();
    nodes.forEach((n) => m.set(n.id, n));
    return m;
  }, [nodes]);

  const orderMutation = useMutation({
    mutationFn: (reindexed: Forward[]) =>
      updateForwardOrder({ forwards: reindexed.map((x) => ({ id: x.id, inx: x.inx! })) }).then(
        unwrap,
      ),
    onError: (error: Error) => toast.error(error.message || "保存排序失败"),
  });

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    return forwards.filter((f) => {
      if (tunnelFilter !== "all" && String(f.tunnelId) !== tunnelFilter) return false;
      if (statusFilter !== "all") {
        if (statusFilter === "1" && f.status !== 1) return false;
        if (statusFilter === "0" && f.status !== 0) return false;
        if (statusFilter === "error" && f.status !== -1) return false;
      }
      if (!q) return true;
      return (
        f.name.toLowerCase().includes(q) ||
        String(f.id).includes(q) ||
        String(f.inPort).includes(q) ||
        (f.remoteAddr || "").toLowerCase().includes(q) ||
        (f.tunnelName || "").toLowerCase().includes(q) ||
        (f.userName || "").toLowerCase().includes(q)
      );
    });
  }, [forwards, query, tunnelFilter, statusFilter]);

  const canReorder = useMemo(
    () =>
      canReorderForwards({
        query,
        status: statusFilter,
        tunnelID: tunnelFilter === "all" ? null : tunnelFilter,
      }),
    [query, statusFilter, tunnelFilter],
  );

  const stats = useMemo(() => {
    const total = forwards.length;
    const running = forwards.filter((f) => f.status === 1).length;
    const paused = forwards.filter((f) => f.status === 0).length;
    const error = forwards.filter((f) => f.status === -1).length;
    return { total, running, paused, error };
  }, [forwards]);

  const openCreate = () => {
    setEditingForward(null);
    setFormOpen(true);
  };
  const openEdit = (f: Forward) => {
    setEditingForward(f);
    setFormOpen(true);
  };

  const remove = async (f: Forward) => {
    if (!confirm(`确认删除转发「${f.name}」？`)) return;
    try {
      const res = await deleteForward(f.id);
      if (res.code === 0) {
        invalidateGlobalSearch("forward");
        toast.success("已删除");
        refresh();
        return;
      }
      if (confirm(`删除失败：${res.msg || "未知错误"}。是否强制删除？`)) {
        const forced = await forceDeleteForward(f.id);
        if (forced.code === 0) {
          invalidateGlobalSearch("forward");
          toast.success("已强制删除");
          refresh();
        } else {
          toast.error(forced.msg || "强制删除失败");
        }
      }
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "删除失败");
    }
  };

  const togglePause = async (f: Forward) => {
    // 乐观更新：先翻转状态，失败时回滚
    const nextStatus = f.status === 1 ? 0 : 1;
    const patch = (status: number) =>
      queryClient.setQueryData<Forward[]>(queries.forward.list(), (prev) =>
        (prev ?? []).map((x) => (x.id === f.id ? { ...x, status } : x)),
      );
    patch(nextStatus);
    try {
      const res =
        f.status === 1 ? await pauseForwardService(f.id) : await resumeForwardService(f.id);
      if (res.code !== 0) {
        toast.error(res.msg || "操作失败");
        patch(f.status);
      } else {
        toast.success(f.status === 1 ? "已暂停" : "已恢复");
        void queryClient.invalidateQueries({ queryKey: queries.dashboard.all });
      }
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "操作失败");
      patch(f.status);
    }
  };

  const runDiagnose = async (f: Forward) => {
    setDiagOpen(true);
    setDiagLoading(true);
    setDiagResult(null);
    try {
      const res = await diagnoseForward(f.id);
      if (res.code === 0) setDiagResult(res.data);
      else toast.error(res.msg || "诊断失败");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "诊断失败");
    } finally {
      setDiagLoading(false);
    }
  };

  const move = async (f: Forward, dir: -1 | 1) => {
    if (!canReorder || orderMutation.isPending) return;

    const direction = dir < 0 ? "up" : "down";
    const previousStored =
      typeof window !== "undefined" ? window.localStorage.getItem("forward-order") : null;
    const reindexed = moveForwardByID(forwards, f.id, direction);
    if (
      reindexed === forwards ||
      forwardOrderIDs(reindexed).join() === forwardOrderIDs(forwards).join()
    ) {
      return;
    }

    queryClient.setQueryData<Forward[]>(queries.forward.list(), reindexed);
    orderMutation.mutate(reindexed, {
      onSuccess: () => {
        if (typeof window !== "undefined") {
          window.localStorage.setItem("forward-order", JSON.stringify(forwardOrderIDs(reindexed)));
        }
      },
      onError: () => {
        // 回滚缓存与本地顺序记录
        void queryClient.invalidateQueries({ queryKey: queries.forward.list() });
        if (typeof window !== "undefined") {
          if (previousStored === null) window.localStorage.removeItem("forward-order");
          else window.localStorage.setItem("forward-order", previousStored);
        }
      },
    });
  };

  const copy = async (text: string, hint: string) => {
    try {
      await navigator.clipboard.writeText(text);
      toast.success(`${hint}已复制`);
    } catch {
      toast.error("复制失败");
    }
  };

  return (
    <div className="space-y-6">
      <PageHeader
        eyebrow={
          <>
            <Zap className="h-3.5 w-3.5" /> forwards
          </>
        }
        title="转发规则"
        description="管理端口转发规则，支持排序 / 诊断 / NFT 规则识别与补全"
        actions={
          <>
            <Button
              variant="outline"
              size="sm"
              onClick={refresh}
              disabled={
                forwardsQuery.isFetching || tunnelsQuery.isFetching || nodesQuery.isFetching
              }
            >
              <RefreshCw
                className={`mr-1.5 h-3.5 w-3.5 ${forwardsQuery.isFetching || tunnelsQuery.isFetching || nodesQuery.isFetching ? "animate-spin" : ""}`}
              />{" "}
              刷新
            </Button>
            {isAdmin && (
              <Button variant="outline" size="sm" onClick={() => setNftOpen(true)}>
                <Scan className="mr-1.5 h-3.5 w-3.5" /> 识别 NFT 规则
              </Button>
            )}
            <Button size="sm" className="shadow-glow" onClick={openCreate}>
              <Plus className="mr-1.5 h-3.5 w-3.5" /> 新建转发
            </Button>
          </>
        }
      />

      <QueryErrorNotice
        error={forwardsQuery.error ?? tunnelsQuery.error ?? nodesQuery.error}
        onRetry={() => {
          void forwardsQuery.refetch();
          void tunnelsQuery.refetch();
          void nodesQuery.refetch();
        }}
      />

      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <KpiCard label="转发总数" value={stats.total} icon={<Zap className="h-4 w-4" />} />
        <KpiCard
          label="运行中"
          value={stats.running}
          tone="ok"
          icon={<Activity className="h-4 w-4" />}
        />
        <KpiCard label="已暂停" value={stats.paused} icon={<Pause className="h-4 w-4" />} />
        <KpiCard
          label="异常"
          value={stats.error}
          tone="warn"
          icon={<ChevronsUpDown className="h-4 w-4" />}
        />
      </div>

      <Card className="border-border/60 bg-card/60">
        <CardContent className="p-4">
          <div className="flex flex-wrap items-center gap-2">
            <div className="relative flex-1 min-w-[220px]">
              <Search className="pointer-events-none absolute left-3 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
              <Input
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="按名称 / ID / 端口 / 目标 / 隧道 / 用户搜索"
                className="pl-9"
              />
            </div>
            <Select value={tunnelFilter} onValueChange={setTunnelFilter}>
              <SelectTrigger className="w-[180px]">
                <SelectValue placeholder="隧道" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部隧道</SelectItem>
                {tunnels.map((t) => (
                  <SelectItem key={t.id} value={String(t.id)}>
                    {t.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Select value={statusFilter} onValueChange={setStatusFilter}>
              <SelectTrigger className="w-[140px]">
                <SelectValue placeholder="状态" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部状态</SelectItem>
                <SelectItem value="1">运行中</SelectItem>
                <SelectItem value="0">已暂停</SelectItem>
                <SelectItem value="error">异常</SelectItem>
              </SelectContent>
            </Select>
            {orderMutation.isPending && (
              <div className="ml-2 flex items-center gap-1 text-xs text-muted-foreground">
                <Loader2 className="h-3 w-3 animate-spin" /> 同步排序…
              </div>
            )}
            {!canReorder && (
              <p className="ml-2 text-xs text-muted-foreground">
                筛选生效时不可调整排序；请清空搜索与筛选后操作全表顺序。
              </p>
            )}
          </div>
        </CardContent>
      </Card>

      <Card className="border-border/60 bg-card/60">
        <div className="overflow-x-auto">
          <Table className="min-w-[1000px]">
            <TableHeader>
              <TableRow className="border-border/60 hover:bg-transparent">
                <TableHead className="w-[60px]">序号</TableHead>
                <TableHead>名称</TableHead>
                <TableHead>隧道</TableHead>
                <TableHead>入口</TableHead>
                <TableHead>出口</TableHead>
                <TableHead>目标</TableHead>
                <TableHead>策略</TableHead>
                <TableHead>状态</TableHead>
                <TableHead>流量 (入/出)</TableHead>
                {isAdmin && <TableHead>用户</TableHead>}
                <TableHead className="w-[70px] text-right">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {loading && forwards.length === 0 && (
                <TableRow>
                  <TableCell
                    colSpan={isAdmin ? 11 : 10}
                    className="h-32 text-left text-muted-foreground lg:text-center"
                  >
                    <Loader2 className="mx-auto h-5 w-5 animate-spin" />
                  </TableCell>
                </TableRow>
              )}
              {!loading && filtered.length === 0 && (
                <TableRow>
                  <TableCell
                    colSpan={isAdmin ? 11 : 10}
                    className="h-32 text-left text-muted-foreground lg:text-center"
                  >
                    暂无数据
                  </TableCell>
                </TableRow>
              )}
              {filtered.map((f, idx) => {
                const tunnel = tunnelMap.get(f.tunnelId);
                const fullIndex = forwards.findIndex((row) => row.id === f.id);
                const reorderDisabled = !canReorder || orderMutation.isPending;
                return (
                  <TableRow key={f.id} className="border-border/60">
                    <TableCell className="font-mono text-xs text-muted-foreground">
                      {canReorder ? fullIndex + 1 : idx + 1}
                    </TableCell>
                    <TableCell>
                      <div className="font-medium">{f.name}</div>
                      <div className="font-mono text-[10px] text-muted-foreground">#{f.id}</div>
                    </TableCell>
                    <TableCell className="font-mono text-xs">
                      {f.tunnelName || tunnel?.name || `#${f.tunnelId}`}
                    </TableCell>
                    <TableCell className="font-mono text-xs">
                      {(tunnel?.inIp || "—") + ":" + f.inPort}
                    </TableCell>
                    <TableCell className="text-xs">
                      {renderForwardExit(f, tunnel, nodeMap)}
                    </TableCell>
                    <TableCell
                      className="font-mono text-xs max-w-[220px] truncate"
                      title={f.remoteAddr}
                    >
                      <div>{f.remoteAddr}</div>
                      {f.targetMode === "manual" && f.activeRemoteAddr && (
                        <div
                          className="mt-0.5 truncate text-[10px] text-primary"
                          title={f.activeRemoteAddr}
                        >
                          当前 {f.activeRemoteAddr}
                        </div>
                      )}
                    </TableCell>
                    <TableCell>
                      <div className="flex flex-col items-start gap-1">
                        <Badge variant="outline" className="border-border/60 text-[10px]">
                          {f.targetMode === "manual" ? "手动目标" : "自动目标"}
                        </Badge>
                        <Badge
                          variant="outline"
                          className="border-border/60 font-mono text-[10px] uppercase"
                        >
                          {f.strategy || "fifo"}
                        </Badge>
                      </div>
                    </TableCell>
                    <TableCell>{renderStatus(f.status)}</TableCell>
                    <TableCell className="font-mono text-[11px] text-muted-foreground">
                      <div>↓ {formatFlow(f.inFlow)}</div>
                      <div>↑ {formatFlow(f.outFlow)}</div>
                    </TableCell>
                    {isAdmin && (
                      <TableCell className="text-xs text-muted-foreground">
                        {f.userName || (f.userId ? `#${f.userId}` : "-")}
                      </TableCell>
                    )}
                    <TableCell className="text-right">
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button
                            variant="ghost"
                            size="icon"
                            className="h-8 w-8"
                            aria-label={`转发操作：${f.name}`}
                          >
                            <MoreHorizontal className="h-4 w-4" />
                          </Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end" className="w-44">
                          <DropdownMenuLabel>操作</DropdownMenuLabel>
                          <DropdownMenuSeparator />
                          <DropdownMenuItem onClick={() => openEdit(f)}>
                            <Pencil className="mr-2 h-3.5 w-3.5" /> 编辑
                          </DropdownMenuItem>
                          <DropdownMenuItem onClick={() => togglePause(f)}>
                            {f.status === 1 ? (
                              <>
                                <Pause className="mr-2 h-3.5 w-3.5" /> 暂停
                              </>
                            ) : (
                              <>
                                <Play className="mr-2 h-3.5 w-3.5" /> 启动
                              </>
                            )}
                          </DropdownMenuItem>
                          <DropdownMenuItem onClick={() => runDiagnose(f)}>
                            <Stethoscope className="mr-2 h-3.5 w-3.5" /> 诊断
                          </DropdownMenuItem>
                          <DropdownMenuSeparator />
                          <DropdownMenuItem
                            onClick={() => move(f, -1)}
                            disabled={reorderDisabled || fullIndex <= 0}
                          >
                            <ArrowUp className="mr-2 h-3.5 w-3.5" /> 上移
                          </DropdownMenuItem>
                          <DropdownMenuItem
                            onClick={() => move(f, 1)}
                            disabled={
                              reorderDisabled || fullIndex < 0 || fullIndex >= forwards.length - 1
                            }
                          >
                            <ArrowDown className="mr-2 h-3.5 w-3.5" /> 下移
                          </DropdownMenuItem>
                          <DropdownMenuSeparator />
                          <DropdownMenuItem
                            onClick={() => copy(`${tunnel?.inIp || ""}:${f.inPort}`, "入口地址")}
                          >
                            <Copy className="mr-2 h-3.5 w-3.5" /> 复制入口
                          </DropdownMenuItem>
                          <DropdownMenuItem onClick={() => copy(f.remoteAddr, "目标地址")}>
                            <Copy className="mr-2 h-3.5 w-3.5" /> 复制目标
                          </DropdownMenuItem>
                          <DropdownMenuSeparator />
                          <DropdownMenuItem
                            onClick={() => remove(f)}
                            className="text-destructive focus:text-destructive"
                          >
                            <Trash2 className="mr-2 h-3.5 w-3.5" /> 删除
                          </DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </div>
      </Card>

      <ForwardFormDialog
        open={formOpen}
        onOpenChange={setFormOpen}
        editing={editingForward}
        tunnels={tunnels}
        nodes={nodes}
        onSaved={refresh}
      />

      {/* 诊断弹窗 */}
      <Dialog open={diagOpen} onOpenChange={setDiagOpen}>
        <DialogContent className="max-w-2xl">
          <DialogHeader>
            <DialogTitle>转发诊断</DialogTitle>
          </DialogHeader>
          {diagLoading && (
            <div className="flex h-40 items-center justify-center text-muted-foreground">
              <Loader2 className="mr-2 h-4 w-4 animate-spin" /> 正在诊断，请稍候…
            </div>
          )}
          {!diagLoading && diagResult && (
            <ScrollArea className="max-h-[60vh]">
              <div className="space-y-2">
                {(diagResult.results || diagResult.checks || []).map((r: any, i: number) => (
                  <div
                    key={i}
                    className={`rounded-md border p-3 text-sm ${
                      r.success
                        ? "border-emerald-500/30 bg-emerald-500/5"
                        : "border-destructive/40 bg-destructive/5"
                    }`}
                  >
                    <div className="flex items-center justify-between">
                      <div className="font-medium">{r.description || r.name}</div>
                      <Badge variant={r.success ? "default" : "destructive"}>
                        {r.success ? "通过" : "失败"}
                      </Badge>
                    </div>
                    <div className="mt-1 font-mono text-[11px] text-muted-foreground">
                      {r.nodeName && `${r.nodeName} · `}
                      {r.targetIp}
                      {r.targetPort ? `:${r.targetPort}` : ""}
                      {typeof r.averageTime === "number" && ` · ${r.averageTime}ms`}
                    </div>
                    {r.message && (
                      <div className="mt-1 text-[12px] text-muted-foreground">{r.message}</div>
                    )}
                  </div>
                ))}
                {!(diagResult.results || diagResult.checks) && (
                  <pre className="rounded-md border border-border/60 bg-background/60 p-3 text-[11px]">
                    {JSON.stringify(diagResult, null, 2)}
                  </pre>
                )}
              </div>
            </ScrollArea>
          )}
        </DialogContent>
      </Dialog>

      {/* NFT 识别弹窗 */}
      {isAdmin && (
        <NftDetectDialog
          open={nftOpen}
          onOpenChange={setNftOpen}
          nodes={nodes}
          onComplete={refresh}
        />
      )}
    </div>
  );
}

function renderStatus(status: number) {
  if (status === 1) {
    return (
      <span className="inline-flex items-center gap-1.5 text-xs">
        <span className="h-1.5 w-1.5 rounded-full bg-emerald-400 shadow-[0_0_6px_rgba(52,211,153,0.8)]" />
        运行中
      </span>
    );
  }
  if (status === 0) {
    return (
      <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
        <span className="h-1.5 w-1.5 rounded-full bg-muted-foreground/60" /> 已暂停
      </span>
    );
  }
  return (
    <span className="inline-flex items-center gap-1.5 text-xs text-destructive">
      <span className="h-1.5 w-1.5 rounded-full bg-destructive" /> 异常
    </span>
  );
}

function renderForwardExit(f: Forward, tunnel: Tunnel | undefined, nodeMap: Map<number, Node>) {
  if (!isTunnelForward(tunnel)) return <span className="text-muted-foreground">-</span>;
  const mode = (f.exitMode as ForwardExitMode) || "single";
  const members = f.exitMembers?.length
    ? f.exitMembers
    : defaultExitMembers(tunnel).map((m) => ({ ...m, outPort: f.outPort || undefined }));
  const visible = mode === "balance" ? members : members.filter((m) => m.active);
  const renderedMembers = visible.length ? visible : members;
  if (tunnel?.relayNodeId) {
    const relayName = nodeMap.get(tunnel.relayNodeId)?.name || `#${tunnel.relayNodeId}`;
    const paths = renderedMembers.map((member) => {
      const exitName =
        member.outNodeName || nodeMap.get(member.outNodeId)?.name || `#${member.outNodeId}`;
      return `${relayName}:${member.relayPort || "-"} → ${exitName}:${member.outPort || "-"}`;
    });
    return (
      <div className="min-w-[190px]">
        <Badge variant="outline" className="border-border/60 text-[10px]">
          {exitModeLabel(mode)} · 三节点
        </Badge>
        <div className="mt-1 font-mono text-[10px] text-muted-foreground" title={paths.join(", ")}>
          {paths.join(", ") || "-"}
        </div>
      </div>
    );
  }
  const names = renderedMembers
    .map((m) => m.outNodeName || nodeMap.get(m.outNodeId)?.name || `#${m.outNodeId}`)
    .join(", ");
  const ports = renderedMembers
    .map((m) => m.outPort)
    .filter(Boolean)
    .join(", ");
  return (
    <div className="min-w-[120px]">
      <Badge variant="outline" className="border-border/60 text-[10px]">
        {exitModeLabel(mode)}
      </Badge>
      <div className="mt-1 truncate text-xs" title={names}>
        {names || "-"}
      </div>
      {ports && <div className="font-mono text-[10px] text-muted-foreground">:{ports}</div>}
    </div>
  );
}
