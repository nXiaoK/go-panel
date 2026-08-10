import { createFileRoute } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { toast } from "sonner";
import { Gauge, Plus, Pencil, Trash2, Search, RefreshCcw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";
import { Label } from "@/components/ui/label";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  createSpeedLimit,
  deleteSpeedLimit,
  getSpeedLimitList,
  getTunnelList,
  updateSpeedLimit,
} from "@/lib/api";
import { listData, queries, unwrap } from "@/lib/api/query";
import type { SpeedLimit, Tunnel } from "@/lib/types";
import { invalidateGlobalSearch } from "@/lib/search-cache";
import { PageHeader, QueryErrorNotice } from "@/components/page";

export const Route = createFileRoute("/_app/limit")({
  head: () => ({ meta: [{ title: "限速策略 · Flux Panel" }] }),
  component: LimitPage,
});

interface FormState {
  id?: number;
  name: string;
  tunnelId: number | null;
  speed: number;
}
const emptyForm: FormState = { name: "", tunnelId: null, speed: 100 };

function LimitPage() {
  const queryClient = useQueryClient();
  const [search, setSearch] = useState("");
  const [tunnelFilter, setTunnelFilter] = useState<string>("all");
  const [dialogOpen, setDialogOpen] = useState(false);
  const [form, setForm] = useState<FormState>(emptyForm);
  const [deleteTarget, setDeleteTarget] = useState<SpeedLimit | null>(null);

  const limitsQuery = useQuery({
    queryKey: queries.limit.list(),
    queryFn: () => getSpeedLimitList().then(listData<SpeedLimit>),
  });
  const tunnelsQuery = useQuery({
    queryKey: queries.tunnel.list(),
    queryFn: () => getTunnelList().then(listData<Tunnel>),
  });

  const limits = useMemo(() => limitsQuery.data ?? [], [limitsQuery.data]);
  const tunnels = useMemo(() => tunnelsQuery.data ?? [], [tunnelsQuery.data]);
  const loading = limitsQuery.isPending || tunnelsQuery.isPending;

  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: queries.limit.list() });
    void queryClient.invalidateQueries({ queryKey: queries.tunnel.list() });
  };

  const saveMutation = useMutation({
    mutationFn: (payload: Record<string, unknown>) =>
      (form.id ? updateSpeedLimit(payload) : createSpeedLimit(payload)).then(unwrap),
    onSuccess: () => {
      invalidateGlobalSearch("all");
      toast.success(form.id ? "已更新" : "已创建");
      setDialogOpen(false);
      void queryClient.invalidateQueries({ queryKey: queries.limit.list() });
    },
    onError: (error: Error) => toast.error(error.message || "保存失败"),
  });

  const deleteMutation = useMutation({
    mutationFn: (id: number) => deleteSpeedLimit(id).then(unwrap),
    onSuccess: () => {
      invalidateGlobalSearch("all");
      toast.success("已删除");
      void queryClient.invalidateQueries({ queryKey: queries.limit.list() });
    },
    onError: (error: Error) => toast.error(error.message || "删除失败"),
  });

  const tunnelName = (id: number) => tunnels.find((t) => t.id === id)?.name || `#${id}`;

  const filtered = useMemo(() => {
    return limits.filter((l) => {
      if (tunnelFilter !== "all" && String(l.tunnelId) !== tunnelFilter) return false;
      if (search && !l.name.toLowerCase().includes(search.toLowerCase())) return false;
      return true;
    });
  }, [limits, search, tunnelFilter]);

  const openCreate = () => {
    setForm(emptyForm);
    setDialogOpen(true);
  };
  const openEdit = (l: SpeedLimit) => {
    setForm({ id: l.id, name: l.name, tunnelId: l.tunnelId, speed: l.speed });
    setDialogOpen(true);
  };

  const save = async () => {
    if (!form.name.trim()) return toast.error("请输入策略名称");
    if (!form.tunnelId) return toast.error("请选择关联隧道");
    if (form.speed <= 0) return toast.error("速率必须大于 0");
    const selectedTunnel = tunnels.find((t) => t.id === form.tunnelId);
    const payload = { ...form, tunnelId: form.tunnelId, tunnelName: selectedTunnel?.name || "" };
    saveMutation.mutate(payload);
  };

  const confirmDelete = async () => {
    if (!deleteTarget) return;
    deleteMutation.mutate(deleteTarget.id);
    setDeleteTarget(null);
  };

  return (
    <div className="space-y-6">
      <PageHeader
        icon={<Gauge className="h-5 w-5" />}
        title="限速策略"
        description="为隧道配置带宽上限，可在用户权限中引用"
        actions={
          <>
            <Button
              variant="outline"
              size="sm"
              onClick={refresh}
              disabled={limitsQuery.isFetching || tunnelsQuery.isFetching}
            >
              <RefreshCcw
                className={`h-4 w-4 ${limitsQuery.isFetching || tunnelsQuery.isFetching ? "animate-spin" : ""}`}
              />{" "}
              刷新
            </Button>
            <Button size="sm" onClick={openCreate}>
              <Plus className="h-4 w-4" /> 新建策略
            </Button>
          </>
        }
      />

      <QueryErrorNotice
        error={limitsQuery.error ?? tunnelsQuery.error}
        onRetry={() => {
          void limitsQuery.refetch();
          void tunnelsQuery.refetch();
        }}
      />

      <div className="grid gap-4 md:grid-cols-3">
        <Card>
          <CardContent className="p-4">
            <div className="text-xs text-muted-foreground">策略总数</div>
            <div className="mt-1 font-mono text-2xl">{limits.length}</div>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="p-4">
            <div className="text-xs text-muted-foreground">覆盖隧道</div>
            <div className="mt-1 font-mono text-2xl">
              {new Set(limits.map((l) => l.tunnelId)).size}{" "}
              <span className="text-sm text-muted-foreground">/ {tunnels.length}</span>
            </div>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="p-4">
            <div className="text-xs text-muted-foreground">平均限速</div>
            <div className="mt-1 font-mono text-2xl">
              {limits.length
                ? Math.round(limits.reduce((a, b) => a + b.speed, 0) / limits.length)
                : 0}
              <span className="ml-1 text-sm text-muted-foreground">Mbps</span>
            </div>
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardContent className="p-4">
          <div className="flex flex-col gap-3 md:flex-row md:items-center">
            <div className="relative flex-1">
              <Search className="pointer-events-none absolute left-3 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
              <Input
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder="搜索策略名称"
                className="pl-9"
              />
            </div>
            <Select value={tunnelFilter} onValueChange={setTunnelFilter}>
              <SelectTrigger className="w-full md:w-56">
                <SelectValue placeholder="全部隧道" />
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
          </div>

          <div className="mt-4 overflow-x-auto rounded-md border border-border">
            <Table className="min-w-[640px]">
              <TableHeader>
                <TableRow>
                  <TableHead>策略名称</TableHead>
                  <TableHead>关联隧道</TableHead>
                  <TableHead className="text-right">速率 (Mbps)</TableHead>
                  <TableHead className="w-32 text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {filtered.length === 0 ? (
                  <TableRow>
                    <TableCell
                      colSpan={4}
                      className="py-10 text-left text-sm text-muted-foreground lg:text-center"
                    >
                      {loading ? "加载中…" : "暂无限速策略"}
                    </TableCell>
                  </TableRow>
                ) : (
                  filtered.map((l) => (
                    <TableRow key={l.id}>
                      <TableCell className="font-medium">{l.name}</TableCell>
                      <TableCell>
                        <Badge variant="outline" className="font-mono text-xs">
                          {tunnelName(l.tunnelId)}
                        </Badge>
                      </TableCell>
                      <TableCell className="text-right font-mono">{l.speed}</TableCell>
                      <TableCell className="text-right">
                        <Button
                          variant="ghost"
                          size="icon"
                          onClick={() => openEdit(l)}
                          aria-label="编辑限速策略"
                        >
                          <Pencil className="h-4 w-4" />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon"
                          onClick={() => setDeleteTarget(l)}
                          className="text-destructive hover:text-destructive"
                          aria-label="删除限速策略"
                        >
                          <Trash2 className="h-4 w-4" />
                        </Button>
                      </TableCell>
                    </TableRow>
                  ))
                )}
              </TableBody>
            </Table>
          </div>
        </CardContent>
      </Card>

      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{form.id ? "编辑限速策略" : "新建限速策略"}</DialogTitle>
            <DialogDescription>为指定隧道配置双向带宽上限（Mbps）</DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            <div className="space-y-1.5">
              <Label htmlFor="limit-form-name">策略名称</Label>
              <Input
                id="limit-form-name"
                value={form.name}
                onChange={(e) => setForm((s) => ({ ...s, name: e.target.value }))}
                placeholder="如：100Mbps 家宽"
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="limit-form-tunnel">关联隧道</Label>
              <Select
                value={form.tunnelId ? String(form.tunnelId) : ""}
                onValueChange={(v) => setForm((s) => ({ ...s, tunnelId: Number(v) }))}
              >
                <SelectTrigger id="limit-form-tunnel">
                  <SelectValue placeholder="选择隧道" />
                </SelectTrigger>
                <SelectContent>
                  {tunnels.map((t) => (
                    <SelectItem key={t.id} value={String(t.id)}>
                      {t.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="limit-form-speed">限速速率 (Mbps)</Label>
              <Input
                id="limit-form-speed"
                type="number"
                min={1}
                value={form.speed}
                onChange={(e) => setForm((s) => ({ ...s, speed: Number(e.target.value) || 0 }))}
              />
            </div>
          </div>
          <DialogFooter>
            <Button variant="ghost" onClick={() => setDialogOpen(false)}>
              取消
            </Button>
            <Button onClick={save} disabled={saveMutation.isPending}>
              {saveMutation.isPending ? "保存中…" : "保存"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <AlertDialog open={!!deleteTarget} onOpenChange={(o) => !o && setDeleteTarget(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>确认删除限速策略？</AlertDialogTitle>
            <AlertDialogDescription>
              将删除策略 <span className="font-mono">{deleteTarget?.name}</span>，
              引用该策略的用户权限会失去限速。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction
              onClick={confirmDelete}
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            >
              删除
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
