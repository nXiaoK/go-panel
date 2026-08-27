import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import {
  Activity,
  Cable,
  Loader2,
  MoreHorizontal,
  Pencil,
  Plus,
  RefreshCw,
  Search,
  Stethoscope,
  Trash2,
  Waypoints,
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
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  createTunnel,
  deleteTunnel,
  diagnoseTunnel,
  getNodeList,
  getTunnelList,
  updateTunnel,
} from "@/lib/api";
import { listData, queries, unwrap } from "@/lib/api/query";
import type { DiagnosisResult, Node, Tunnel, TunnelForm } from "@/lib/types";
import { invalidateGlobalSearch } from "@/lib/search-cache";
import { FormField as Field, KpiCard, PageHeader, QueryErrorNotice } from "@/components/page";

export const Route = createFileRoute("/_app/tunnel")({
  validateSearch: (search: Record<string, unknown>) => ({
    action: search.action === "create" ? "create" : undefined,
  }),
  head: () => ({ meta: [{ title: "隧道管理 · Flux Panel" }] }),
  component: TunnelPage,
});

const TUNNEL_TYPE_LABEL: Record<number, string> = { 1: "端口转发", 2: "隧道转发" };
const FLOW_LABEL: Record<number, string> = { 1: "单向计费", 2: "双向计费" };

const emptyForm = (): TunnelForm => ({
  name: "",
  type: 1,
  inNodeId: null,
  relayNodeId: null,
  outNodeId: null,
  protocol: "tcp",
  tcpListenAddr: "0.0.0.0",
  udpListenAddr: "0.0.0.0",
  interfaceName: "",
  flow: 2,
  trafficRatio: 1,
  status: 1,
});

function TunnelPage() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const search = Route.useSearch();
  const [query, setQuery] = useState("");
  const [typeFilter, setTypeFilter] = useState<string>("all");
  const [statusFilter, setStatusFilter] = useState<string>("all");

  const [formOpen, setFormOpen] = useState(false);
  const [editingId, setEditingId] = useState<number | null>(null);
  const [form, setForm] = useState<TunnelForm>(emptyForm());

  const [diagOpen, setDiagOpen] = useState(false);
  const [diagLoading, setDiagLoading] = useState(false);
  const [diagResult, setDiagResult] = useState<DiagnosisResult | null>(null);

  const tunnelsQuery = useQuery({
    queryKey: queries.tunnel.list(),
    queryFn: () => getTunnelList().then(listData<Tunnel>),
  });
  const nodesQuery = useQuery({
    queryKey: queries.node.rawList(),
    queryFn: () => getNodeList().then(listData<Node>),
  });
  const tunnels = useMemo(() => tunnelsQuery.data ?? [], [tunnelsQuery.data]);
  const nodes = useMemo(() => nodesQuery.data ?? [], [nodesQuery.data]);
  const loading = tunnelsQuery.isPending || nodesQuery.isPending;

  const nodeMap = useMemo(() => {
    const m = new Map<number, Node>();
    nodes.forEach((n) => m.set(n.id, n));
    return m;
  }, [nodes]);

  const nftModeFor = useCallback(
    (t: Tunnel) => {
      const a = nodeMap.get(t.inNodeId)?.forwardMode;
      const relay = t.relayNodeId ? nodeMap.get(t.relayNodeId)?.forwardMode : undefined;
      const b = t.outNodeId ? nodeMap.get(t.outNodeId)?.forwardMode : undefined;
      return (
        a === "nftables" &&
        (t.type === 1 || (b === "nftables" && (!t.relayNodeId || relay === "nftables")))
      );
    },
    [nodeMap],
  );

  const refresh = useCallback(() => {
    void queryClient.invalidateQueries({ queryKey: queries.tunnel.list() });
    void queryClient.invalidateQueries({ queryKey: queries.node.list() });
  }, [queryClient]);

  const saveMutation = useMutation({
    mutationFn: ({ payload, isEdit }: { payload: TunnelForm; isEdit: boolean }) =>
      (isEdit ? updateTunnel(payload) : createTunnel(payload)).then(unwrap),
    onSuccess: (_data, { isEdit }) => {
      invalidateGlobalSearch("tunnel");
      toast.success(isEdit ? "隧道已更新" : "隧道已创建");
      setFormOpen(false);
      void queryClient.invalidateQueries({ queryKey: queries.tunnel.list() });
      void queryClient.invalidateQueries({ queryKey: queries.forward.all });
      void queryClient.invalidateQueries({ queryKey: queries.dashboard.all });
    },
    onError: (error: Error) => toast.error(error.message || "操作失败"),
  });

  const deleteMutation = useMutation({
    mutationFn: (id: number) => deleteTunnel(id).then(unwrap),
    onSuccess: () => {
      invalidateGlobalSearch("tunnel");
      toast.success("已删除");
      void queryClient.invalidateQueries({ queryKey: queries.tunnel.list() });
      void queryClient.invalidateQueries({ queryKey: queries.forward.all });
      void queryClient.invalidateQueries({ queryKey: queries.dashboard.all });
    },
    onError: (error: Error) => toast.error(error.message || "删除失败"),
  });

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    return tunnels.filter((t) => {
      if (typeFilter !== "all" && String(t.type) !== typeFilter) return false;
      if (statusFilter !== "all" && String(t.status ?? 1) !== statusFilter) return false;
      if (!q) return true;
      return (
        t.name.toLowerCase().includes(q) ||
        String(t.id).includes(q) ||
        (t.protocol || "").toLowerCase().includes(q)
      );
    });
  }, [tunnels, query, typeFilter, statusFilter]);

  const stats = useMemo(() => {
    const total = tunnels.length;
    const running = tunnels.filter((t) => (t.status ?? 1) === 1).length;
    const tunnelType = tunnels.filter((t) => t.type === 2).length;
    const portType = tunnels.filter((t) => t.type === 1).length;
    return { total, running, tunnelType, portType };
  }, [tunnels]);

  const openCreate = useCallback(() => {
    setEditingId(null);
    setForm(emptyForm());
    setFormOpen(true);
  }, []);

  useEffect(() => {
    if (search.action !== "create") return;
    openCreate();
    void navigate({ to: "/tunnel", search: {}, replace: true });
  }, [navigate, openCreate, search.action]);

  const openEdit = (t: Tunnel) => {
    setEditingId(t.id);
    setForm({
      id: t.id,
      name: t.name,
      type: t.type,
      inNodeId: t.inNodeId,
      relayNodeId: t.relayNodeId ?? null,
      outNodeId: t.outNodeId ?? null,
      protocol: t.protocol || "tcp",
      tcpListenAddr: t.tcpListenAddr || "0.0.0.0",
      udpListenAddr: t.udpListenAddr || "0.0.0.0",
      interfaceName: t.interfaceName || "",
      flow: t.flow ?? 2,
      trafficRatio: t.trafficRatio ?? 1,
      status: t.status ?? 1,
    });
    setFormOpen(true);
  };

  const isNftForm = useMemo(() => {
    const a = nodeMap.get(form.inNodeId || 0)?.forwardMode;
    const relay = nodeMap.get(form.relayNodeId || 0)?.forwardMode;
    const b = nodeMap.get(form.outNodeId || 0)?.forwardMode;
    return (
      a === "nftables" &&
      (form.type === 1 || (b === "nftables" && (!form.relayNodeId || relay === "nftables")))
    );
  }, [form.inNodeId, form.relayNodeId, form.outNodeId, form.type, nodeMap]);

  const submit = async () => {
    if (!form.name.trim()) return toast.error("请输入隧道名称");
    if (!form.inNodeId) return toast.error("请选择入口节点");
    if (form.type === 2 && !form.outNodeId) return toast.error("隧道转发需要选择出口节点");
    if (
      form.type === 2 &&
      form.relayNodeId &&
      (form.relayNodeId === form.inNodeId || form.relayNodeId === form.outNodeId)
    ) {
      return toast.error("入口、中继和出口节点不能重复");
    }
    if (form.type === 2 && form.relayNodeId && !isNftForm) {
      return toast.error("三节点串联要求入口、中继和出口均为 nftables 模式");
    }
    const payload = {
      ...form,
      // nftables 模式下强制 tcp+udp（原逻辑）
      protocol: isNftForm ? "tcp+udp" : form.protocol,
    };
    saveMutation.mutate({ payload, isEdit: Boolean(editingId) });
  };

  const remove = async (t: Tunnel) => {
    if (!confirm(`确认删除隧道「${t.name}」吗？`)) return;
    deleteMutation.mutate(t.id);
  };

  const runDiagnose = async (t: Tunnel) => {
    setDiagOpen(true);
    setDiagLoading(true);
    setDiagResult(null);
    try {
      const res = await diagnoseTunnel(t.id);
      if (res.code === 0) setDiagResult(res.data as DiagnosisResult);
      else toast.error(res.msg || "诊断失败");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "诊断失败");
    } finally {
      setDiagLoading(false);
    }
  };

  return (
    <div className="space-y-6">
      <PageHeader
        eyebrow={
          <>
            <Waypoints className="h-3.5 w-3.5" /> tunnels
          </>
        }
        title="隧道管理"
        description="支持两节点及 A → B → C 三节点 IPv4 nftables 串联，nftables 模式协议自动为 TCP+UDP"
        actions={
          <>
            <Button
              variant="outline"
              size="sm"
              onClick={refresh}
              disabled={tunnelsQuery.isFetching || nodesQuery.isFetching}
            >
              <RefreshCw
                className={`mr-1.5 h-3.5 w-3.5 ${tunnelsQuery.isFetching || nodesQuery.isFetching ? "animate-spin" : ""}`}
              />{" "}
              刷新
            </Button>
            <Button size="sm" className="shadow-glow" onClick={openCreate}>
              <Plus className="mr-1.5 h-3.5 w-3.5" /> 新建隧道
            </Button>
          </>
        }
      />

      <QueryErrorNotice
        error={tunnelsQuery.error ?? nodesQuery.error}
        onRetry={() => {
          void tunnelsQuery.refetch();
          void nodesQuery.refetch();
        }}
      />

      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <KpiCard label="隧道总数" value={stats.total} icon={<Cable className="h-4 w-4" />} />
        <KpiCard
          label="运行中"
          value={stats.running}
          tone="ok"
          icon={<Activity className="h-4 w-4" />}
        />
        <KpiCard label="端口转发" value={stats.portType} icon={<Waypoints className="h-4 w-4" />} />
        <KpiCard
          label="隧道转发"
          value={stats.tunnelType}
          icon={<Waypoints className="h-4 w-4" />}
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
                placeholder="按名称 / ID / 协议搜索"
                className="pl-9"
              />
            </div>
            <Select value={typeFilter} onValueChange={setTypeFilter}>
              <SelectTrigger className="w-[140px]">
                <SelectValue placeholder="类型" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部类型</SelectItem>
                <SelectItem value="1">端口转发</SelectItem>
                <SelectItem value="2">隧道转发</SelectItem>
              </SelectContent>
            </Select>
            <Select value={statusFilter} onValueChange={setStatusFilter}>
              <SelectTrigger className="w-[140px]">
                <SelectValue placeholder="状态" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部状态</SelectItem>
                <SelectItem value="1">启用</SelectItem>
                <SelectItem value="0">禁用</SelectItem>
              </SelectContent>
            </Select>
          </div>
        </CardContent>
      </Card>

      <Card className="border-border/60 bg-card/60">
        <div className="overflow-x-auto">
          <Table className="min-w-[1180px]">
            <TableHeader>
              <TableRow className="border-border/60 hover:bg-transparent">
                <TableHead className="w-[60px]">ID</TableHead>
                <TableHead>名称</TableHead>
                <TableHead>类型</TableHead>
                <TableHead>入口节点</TableHead>
                <TableHead>中继节点</TableHead>
                <TableHead>出口节点</TableHead>
                <TableHead>协议</TableHead>
                <TableHead>监听</TableHead>
                <TableHead>计费</TableHead>
                <TableHead>倍率</TableHead>
                <TableHead>状态</TableHead>
                <TableHead className="w-[70px] text-right">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {loading && tunnels.length === 0 && (
                <TableRow>
                  <TableCell
                    colSpan={12}
                    className="h-32 text-left text-muted-foreground lg:text-center"
                  >
                    <Loader2 className="mx-auto h-5 w-5 animate-spin" />
                  </TableCell>
                </TableRow>
              )}
              {!loading && filtered.length === 0 && (
                <TableRow>
                  <TableCell
                    colSpan={12}
                    className="h-32 text-left text-muted-foreground lg:text-center"
                  >
                    暂无数据
                  </TableCell>
                </TableRow>
              )}
              {filtered.map((t) => {
                const inNode = nodeMap.get(t.inNodeId);
                const relayNode = t.relayNodeId ? nodeMap.get(t.relayNodeId) : undefined;
                const outNode = t.outNodeId ? nodeMap.get(t.outNodeId) : undefined;
                const nft = nftModeFor(t);
                const proto = nft ? "tcp+udp" : t.protocol || "tcp";
                return (
                  <TableRow key={t.id} className="border-border/60">
                    <TableCell className="font-mono text-xs text-muted-foreground">
                      #{t.id}
                    </TableCell>
                    <TableCell className="font-medium">{t.name}</TableCell>
                    <TableCell>
                      <Badge variant="outline" className="border-border/60 font-mono text-[10px]">
                        {TUNNEL_TYPE_LABEL[t.type] || "-"}
                      </Badge>
                    </TableCell>
                    <TableCell className="font-mono text-xs">
                      {inNode?.name || `#${t.inNodeId}`}
                    </TableCell>
                    <TableCell className="font-mono text-xs">
                      {t.relayNodeId ? relayNode?.name || `#${t.relayNodeId}` : "-"}
                    </TableCell>
                    <TableCell className="font-mono text-xs">
                      {t.type === 2
                        ? outNode?.name || (t.outNodeId ? `#${t.outNodeId}` : "-")
                        : "-"}
                    </TableCell>
                    <TableCell>
                      <Badge variant="secondary" className="font-mono text-[10px] uppercase">
                        {proto}
                      </Badge>
                    </TableCell>
                    <TableCell className="font-mono text-[11px] text-muted-foreground">
                      <div>{t.tcpListenAddr || "-"}</div>
                      {(proto === "udp" || proto === "tcp+udp") && (
                        <div>{t.udpListenAddr || "-"}</div>
                      )}
                    </TableCell>
                    <TableCell className="text-xs">{FLOW_LABEL[t.flow ?? 2]}</TableCell>
                    <TableCell className="font-mono text-xs">×{t.trafficRatio ?? 1}</TableCell>
                    <TableCell>
                      {(t.status ?? 1) === 1 ? (
                        <span className="inline-flex items-center gap-1.5 text-xs">
                          <span className="h-1.5 w-1.5 rounded-full bg-emerald-400 shadow-[0_0_6px_rgba(52,211,153,0.8)]" />
                          启用
                        </span>
                      ) : (
                        <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
                          <span className="h-1.5 w-1.5 rounded-full bg-muted-foreground/60" />
                          禁用
                        </span>
                      )}
                    </TableCell>
                    <TableCell className="text-right">
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button
                            variant="ghost"
                            size="icon"
                            className="h-8 w-8"
                            aria-label={`隧道操作：${t.name}`}
                          >
                            <MoreHorizontal className="h-4 w-4" />
                          </Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end" className="w-40">
                          <DropdownMenuLabel>操作</DropdownMenuLabel>
                          <DropdownMenuSeparator />
                          <DropdownMenuItem onClick={() => openEdit(t)}>
                            <Pencil className="mr-2 h-3.5 w-3.5" /> 编辑
                          </DropdownMenuItem>
                          <DropdownMenuItem onClick={() => runDiagnose(t)}>
                            <Stethoscope className="mr-2 h-3.5 w-3.5" /> 诊断
                          </DropdownMenuItem>
                          <DropdownMenuSeparator />
                          <DropdownMenuItem
                            onClick={() => remove(t)}
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

      <Dialog open={formOpen} onOpenChange={setFormOpen}>
        <DialogContent className="max-w-lg">
          <DialogHeader>
            <DialogTitle>{editingId ? "编辑隧道" : "新建隧道"}</DialogTitle>
          </DialogHeader>
          <div className="grid gap-3">
            <Field label="名称">
              <Input
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
              />
            </Field>
            <div className="grid grid-cols-2 gap-3">
              <Field label="类型">
                <Select
                  disabled={Boolean(editingId)}
                  value={String(form.type)}
                  onValueChange={(v) =>
                    setForm({
                      ...form,
                      type: Number(v),
                      relayNodeId: Number(v) === 2 ? form.relayNodeId : null,
                      outNodeId: Number(v) === 2 ? form.outNodeId : null,
                    })
                  }
                >
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="1">端口转发</SelectItem>
                    <SelectItem value="2">隧道转发</SelectItem>
                  </SelectContent>
                </Select>
              </Field>
              <Field label="计费">
                <Select
                  value={String(form.flow)}
                  onValueChange={(v) => setForm({ ...form, flow: Number(v) })}
                >
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="1">单向</SelectItem>
                    <SelectItem value="2">双向</SelectItem>
                  </SelectContent>
                </Select>
              </Field>
            </div>
            <div className="grid gap-3 sm:grid-cols-3">
              <Field label="入口节点">
                <Select
                  disabled={Boolean(editingId)}
                  value={form.inNodeId ? String(form.inNodeId) : ""}
                  onValueChange={(v) => setForm({ ...form, inNodeId: Number(v) })}
                >
                  <SelectTrigger>
                    <SelectValue placeholder="选择节点" />
                  </SelectTrigger>
                  <SelectContent>
                    {nodes.map((n) => (
                      <SelectItem key={n.id} value={String(n.id)}>
                        {n.name} · {n.forwardMode || "gost"}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
              <Field label="中继节点（可选）">
                <Select
                  disabled={form.type !== 2 || Boolean(editingId)}
                  value={form.relayNodeId ? String(form.relayNodeId) : "none"}
                  onValueChange={(v) =>
                    setForm({ ...form, relayNodeId: v === "none" ? null : Number(v) })
                  }
                >
                  <SelectTrigger>
                    <SelectValue placeholder="不使用中继" />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="none">不使用中继</SelectItem>
                    {nodes.map((n) => (
                      <SelectItem key={n.id} value={String(n.id)}>
                        {n.name} · {n.forwardMode || "gost"}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
              <Field label="出口节点">
                <Select
                  disabled={form.type !== 2 || Boolean(editingId)}
                  value={form.outNodeId ? String(form.outNodeId) : ""}
                  onValueChange={(v) => setForm({ ...form, outNodeId: Number(v) })}
                >
                  <SelectTrigger>
                    <SelectValue placeholder={form.type === 2 ? "选择节点" : "端口转发无需"} />
                  </SelectTrigger>
                  <SelectContent>
                    {nodes.map((n) => (
                      <SelectItem key={n.id} value={String(n.id)}>
                        {n.name} · {n.forwardMode || "gost"}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
            </div>
            <Field label={`协议${isNftForm ? "（nftables 模式强制 TCP+UDP）" : ""}`}>
              <Select
                disabled={isNftForm}
                value={isNftForm ? "tcp+udp" : form.protocol}
                onValueChange={(v) => setForm({ ...form, protocol: v })}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="tcp">TCP</SelectItem>
                  <SelectItem value="udp">UDP</SelectItem>
                  <SelectItem value="tcp+udp">TCP + UDP</SelectItem>
                </SelectContent>
              </Select>
            </Field>
            <div className="grid grid-cols-2 gap-3">
              <Field label="TCP 监听地址">
                <Input
                  value={form.tcpListenAddr}
                  onChange={(e) => setForm({ ...form, tcpListenAddr: e.target.value })}
                />
              </Field>
              <Field label="UDP 监听地址">
                <Input
                  value={form.udpListenAddr}
                  onChange={(e) => setForm({ ...form, udpListenAddr: e.target.value })}
                />
              </Field>
            </div>
            <div className="grid grid-cols-2 gap-3">
              <Field label="流量倍率">
                <Input
                  type="number"
                  min={0}
                  step={0.1}
                  value={form.trafficRatio}
                  onChange={(e) => setForm({ ...form, trafficRatio: Number(e.target.value) })}
                />
              </Field>
              <Field label="状态">
                <Select
                  value={String(form.status)}
                  onValueChange={(v) => setForm({ ...form, status: Number(v) })}
                >
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="1">启用</SelectItem>
                    <SelectItem value="0">禁用</SelectItem>
                  </SelectContent>
                </Select>
              </Field>
            </div>
          </div>
          <DialogFooter>
            <Button variant="ghost" onClick={() => setFormOpen(false)}>
              取消
            </Button>
            <Button onClick={submit} disabled={saveMutation.isPending} className="shadow-glow">
              {saveMutation.isPending && <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />}
              保存
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={diagOpen} onOpenChange={setDiagOpen}>
        <DialogContent className="max-w-2xl">
          <DialogHeader>
            <DialogTitle>隧道诊断</DialogTitle>
          </DialogHeader>
          {diagLoading && (
            <div className="flex h-40 items-center justify-center text-muted-foreground">
              <Loader2 className="mr-2 h-4 w-4 animate-spin" /> 正在诊断，请稍候…
            </div>
          )}
          {!diagLoading && diagResult && (
            <div className="space-y-3">
              <div className="text-sm text-muted-foreground">
                <span className="font-mono">{diagResult.tunnelName}</span> · {diagResult.tunnelType}
              </div>
              <div className="space-y-2">
                {diagResult.results?.map((r, i) => (
                  <div
                    key={i}
                    className={`rounded-md border p-3 text-sm ${
                      r.success
                        ? "border-emerald-500/30 bg-emerald-500/5"
                        : "border-destructive/40 bg-destructive/5"
                    }`}
                  >
                    <div className="flex items-center justify-between">
                      <div className="font-medium">{r.description}</div>
                      <Badge variant={r.success ? "default" : "destructive"}>
                        {r.success ? "通过" : "失败"}
                      </Badge>
                    </div>
                    <div className="mt-1 font-mono text-[11px] text-muted-foreground">
                      {r.nodeName} → {r.targetIp}
                      {r.targetPort ? `:${r.targetPort}` : ""}
                      {typeof r.averageTime === "number" && ` · ${r.averageTime}ms`}
                      {typeof r.packetLoss === "number" && ` · 丢包 ${r.packetLoss}%`}
                    </div>
                    {r.message && (
                      <div className="mt-1 text-[12px] text-muted-foreground">{r.message}</div>
                    )}
                  </div>
                ))}
              </div>
            </div>
          )}
        </DialogContent>
      </Dialog>
    </div>
  );
}
