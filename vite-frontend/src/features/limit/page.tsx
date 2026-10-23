import { PageHeader } from "@/components/patterns/page-header";
import { DataTable } from "@/components/patterns/data-table";
import { EmptyState, ErrorState } from "@/components/patterns/feedback";
import { TableToolbar } from "@/components/patterns/table-toolbar";
import { FilterSelect } from "@/components/patterns/filter-select";
import { FormField } from "@/components/patterns/form-field";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { useTunnels } from "@/features/tunnel/queries";
import { useConfirm } from "@/hooks/use-confirm";
import { createSpeedLimit, deleteSpeedLimit, updateSpeedLimit } from "@/lib/api";
import { notify } from "@/lib/notify";
import type { SpeedLimit, SpeedLimitWrite } from "@/lib/types";
import type { ColumnDef } from "@tanstack/react-table";
import { Pencil, Plus, RefreshCw, Trash2 } from "lucide-react";
import { useMemo, useRef, useState } from "react";
import { useLimitMutation, useLimits } from "./queries";

type FormState = { id?: number; name: string; tunnelId: string; speed: string };
type Errors = Partial<Record<"name" | "tunnelId" | "speed", string>>;
// 新建草稿默认 100 Mbps，仅在保存后生效；后端要求整数≥1，未定义额外最大速率。
const emptyForm = (): FormState => ({ name: "", tunnelId: "", speed: "100" });

export function LimitPage() {
  const confirm = useConfirm();
  const [search, setSearch] = useState("");
  const [tunnelFilter, setTunnelFilter] = useState("");
  const [dialogOpen, setDialogOpen] = useState(false);
  const [form, setForm] = useState<FormState>(emptyForm);
  const [errors, setErrors] = useState<Errors>({});
  const [saveError, setSaveError] = useState<unknown>();
  const submitting = useRef(false);
  const limitsQuery = useLimits();
  const tunnelsQuery = useTunnels();
  const limits = useMemo(() => limitsQuery.data ?? [], [limitsQuery.data]);
  const tunnels = useMemo(() => tunnelsQuery.data ?? [], [tunnelsQuery.data]);
  const refresh = () => {
    void limitsQuery.refetch();
    void tunnelsQuery.refetch();
  };
  const saveMutation = useLimitMutation("limit.update", {
    mutationFn: (payload: SpeedLimitWrite) =>
      payload.id ? updateSpeedLimit(payload) : createSpeedLimit(payload),
    onSuccess: (_, payload) => {
      notify.success(payload.id ? "限速策略已更新" : "限速策略已创建");
      setDialogOpen(false);
    },
    onError: setSaveError,
    onSettled: () => {
      submitting.current = false;
    },
  });
  const deleteMutation = useLimitMutation("limit.delete", {
    mutationFn: (id: number) => deleteSpeedLimit(id),
    onSuccess: () => notify.success("限速策略已删除"),
    onError: (error: Error) => notify.error(error.message || "删除失败"),
  });
  const tunnelName = (limit: SpeedLimit) =>
    tunnels.find((t) => t.id === limit.tunnelId)?.name || limit.tunnelName || `#${limit.tunnelId}`;
  const filtered = useMemo(
    () =>
      limits.filter(
        (limit) =>
          (!tunnelFilter || String(limit.tunnelId) === tunnelFilter) &&
          limit.name.toLowerCase().includes(search.trim().toLowerCase()),
      ),
    [limits, search, tunnelFilter],
  );
  function openForm(limit?: SpeedLimit) {
    setForm(
      limit
        ? {
            id: limit.id,
            name: limit.name,
            tunnelId: String(limit.tunnelId),
            speed: String(limit.speed),
          }
        : emptyForm(),
    );
    setErrors({});
    setSaveError(undefined);
    setDialogOpen(true);
  }
  function save() {
    if (submitting.current) return;
    const selected = tunnels.find((t) => String(t.id) === form.tunnelId);
    const next: Errors = {};
    if (!form.name.trim()) next.name = "请输入策略名称";
    if (!selected) next.tunnelId = "请选择有效的关联隧道";
    // 后端 SpeedLimitDto 使用 int 且 min=1，不额外设置未经后端定义的最大速率。
    const speed = Number(form.speed);
    if (!form.speed.trim() || !Number.isSafeInteger(speed) || speed < 1)
      next.speed = "速率必须是大于或等于 1 的整数";
    setErrors(next);
    setSaveError(undefined);
    if (Object.keys(next).length || !selected) return;
    submitting.current = true;
    saveMutation.mutate({
      id: form.id,
      name: form.name.trim(),
      tunnelId: selected.id,
      tunnelName: selected.name,
      speed,
    });
  }
  async function remove(limit: SpeedLimit) {
    if (deleteMutation.isPending) return;
    if (
      await confirm({
        title: `删除限速策略「${limit.name}」`,
        description: "删除后无法恢复。仍被用户授权引用的策略无法删除，请先取消分配。",
        confirmText: "删除限速策略",
        tone: "danger",
      })
    )
      deleteMutation.mutate(limit.id);
  }
  const actions = (limit: SpeedLimit) => (
    <div className="flex justify-end gap-1">
      <Button
        type="button"
        variant="ghost"
        size="icon-sm"
        aria-label="编辑限速策略"
        onClick={() => openForm(limit)}
      >
        <Pencil />
      </Button>
      <Button
        type="button"
        variant="ghost"
        size="icon-sm"
        aria-label="删除限速策略"
        className="text-status-critical-text"
        disabled={deleteMutation.isPending}
        onClick={() => void remove(limit)}
      >
        <Trash2 />
      </Button>
    </div>
  );
  const columns: ColumnDef<SpeedLimit>[] = [
    {
      accessorKey: "name",
      header: "策略名称",
      cell: ({ row }) => <span className="font-medium text-ink">{row.original.name}</span>,
    },
    {
      id: "tunnel",
      header: "关联隧道",
      enableSorting: false,
      cell: ({ row }) => tunnelName(row.original),
    },
    { accessorKey: "speed", header: "速率 (Mbps)", meta: { align: "right", numeric: true } },
    {
      id: "actions",
      header: "操作",
      enableSorting: false,
      meta: { actions: true, align: "right" },
      cell: ({ row }) => actions(row.original),
    },
  ];
  const hasFilters = Boolean(search || tunnelFilter);
  return (
    <div className="min-w-0 space-y-6">
      <PageHeader
        title="限速策略"
        description="为隧道配置带宽上限，可在用户授权中引用"
        actions={
          <>
            <Button
              type="button"
              variant="outline"
              onClick={refresh}
              loading={limitsQuery.isFetching || tunnelsQuery.isFetching}
            >
              <RefreshCw />
              刷新
            </Button>
            <Button type="button" onClick={() => openForm()}>
              <Plus />
              新建策略
            </Button>
          </>
        }
      />
      <TableToolbar
        searchValue={search}
        onSearchChange={setSearch}
        searchPlaceholder="搜索策略名称"
        count={`共 ${filtered.length} 条`}
        filters={
          <FilterSelect
            label="筛选隧道"
            value={tunnelFilter}
            onChange={setTunnelFilter}
            options={[
              { value: "", label: "全部隧道" },
              ...tunnels.map((t) => ({ value: String(t.id), label: t.name })),
            ]}
          />
        }
      />
      <DataTable
        columns={columns}
        data={filtered}
        getRowId={(row) => String(row.id)}
        loading={limitsQuery.isPending || tunnelsQuery.isPending}
        error={limitsQuery.error ?? tunnelsQuery.error}
        onRetry={refresh}
        ariaLabel="限速策略列表"
        emptyState={
          <EmptyState
            title="暂无限速策略"
            description="新建策略，为隧道配置带宽上限"
            filtered={hasFilters}
            onClearFilters={() => {
              setSearch("");
              setTunnelFilter("");
            }}
            action={
              <Button type="button" variant="outline" onClick={() => openForm()}>
                新建策略
              </Button>
            }
          />
        }
        renderMobileCard={(limit) => (
          <div className="space-y-3">
            <div className="flex items-start justify-between gap-2">
              <h2 className="min-w-0 break-words text-body-strong text-ink">{limit.name}</h2>
              {actions(limit)}
            </div>
            <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-2 text-body">
              <dt className="text-mute">关联隧道</dt>
              <dd className="break-words text-right">{tunnelName(limit)}</dd>
              <dt className="text-mute">速率</dt>
              <dd className="text-right font-mono tabular">{limit.speed} Mbps</dd>
            </dl>
          </div>
        )}
      />
      <Dialog
        open={dialogOpen}
        onOpenChange={(open) => {
          if (!saveMutation.isPending) setDialogOpen(open);
        }}
      >
        <DialogContent
          onEscapeKeyDown={(event) => {
            if (saveMutation.isPending) event.preventDefault();
          }}
          onPointerDownOutside={(event) => {
            if (saveMutation.isPending) event.preventDefault();
          }}
        >
          <DialogHeader>
            <DialogTitle>{form.id ? "编辑限速策略" : "新建限速策略"}</DialogTitle>
            <DialogDescription>为指定隧道配置双向带宽上限</DialogDescription>
          </DialogHeader>
          <form
            id="limit-form"
            className="space-y-4"
            noValidate
            onSubmit={(event) => {
              event.preventDefault();
              save();
            }}
          >
            {saveError ? <ErrorState error={saveError} /> : null}
            <FormField label="策略名称" htmlFor="limit-form-name" required error={errors.name}>
              <Input
                aria-label="策略名称"
                required
                value={form.name}
                disabled={saveMutation.isPending}
                onChange={(e) => setForm((s) => ({ ...s, name: e.target.value }))}
              />
            </FormField>
            <FormField
              label="关联隧道"
              htmlFor="limit-form-tunnel"
              required
              error={errors.tunnelId}
            >
              <Select
                value={form.tunnelId}
                disabled={saveMutation.isPending}
                onValueChange={(value) => setForm((s) => ({ ...s, tunnelId: value }))}
              >
                <SelectTrigger aria-label="关联隧道">
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
            </FormField>
            <FormField
              label="限速速率 (Mbps)"
              htmlFor="limit-form-speed"
              required
              hint="输入大于或等于 1 的整数"
              error={errors.speed}
            >
              {(control) => (
                <div className="relative">
                  <Input
                    {...control}
                    aria-label="限速速率 (Mbps)"
                    required
                    type="number"
                    min={1}
                    step={1}
                    value={form.speed}
                    disabled={saveMutation.isPending}
                    onChange={(e) => setForm((s) => ({ ...s, speed: e.target.value }))}
                    className="pr-16 font-mono tabular"
                  />
                  <span
                    aria-hidden="true"
                    className="pointer-events-none absolute right-3 top-2 text-sm text-mute"
                  >
                    Mbps
                  </span>
                </div>
              )}
            </FormField>
          </form>
          <DialogFooter>
            <Button
              type="button"
              variant="ghost"
              disabled={saveMutation.isPending}
              onClick={() => setDialogOpen(false)}
            >
              取消
            </Button>
            <Button type="submit" form="limit-form" loading={saveMutation.isPending}>
              保存策略
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
