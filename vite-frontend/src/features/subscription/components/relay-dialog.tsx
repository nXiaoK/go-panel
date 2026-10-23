import { FormField as Field } from "@/components/patterns/form-field";
import { EntitySheet } from "@/components/patterns/entity-sheet";
import { DataTable } from "@/components/patterns/data-table";
import { ErrorState, LoadingRows } from "@/components/patterns/feedback";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { useSubscriptionMutation } from "@/features/subscription/queries";
import { createProxyNodeRelay, previewProxyNodeRelay } from "@/lib/api";
import { notify } from "@/lib/notify";
import { resourceKeys as queries } from "@/lib/query-keys";
import type { ProxyNode, RelayMode, RelayWrite, SubTunnelOption } from "@/lib/types";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";

export function SubscriptionRelayDialog({
  open,
  onOpenChange,
  node,
  tunnels,
  onSaved,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  node: ProxyNode | null;
  tunnels: SubTunnelOption[];
  onSaved?: () => void;
}) {
  // 默认替换原节点；入口留空由后端分配，不改变原隧道端口策略。
  const initial = {
    tunnelId:
      node?.forwardTunnelId && tunnels.some((tunnel) => tunnel.id === node.forwardTunnelId)
        ? String(node.forwardTunnelId)
        : tunnels[0]?.id
          ? String(tunnels[0].id)
          : "",
    inPort: node?.forwardInPort ? String(node.forwardInPort) : "",
    name: node?.forwardName || `订阅节点-${node?.name || ""}`,
    mode: "replace" as RelayMode,
  };
  const [form, setForm] = useState(initial);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const dirty = JSON.stringify(form) !== JSON.stringify(initial);
  const previewNodeId = node?.id ?? 0;
  const previewTunnelId = Number(form.tunnelId) || 0;
  const previewInPort = form.inPort ? Number(form.inPort) : null;
  const previewQuery = useQuery({
    queryKey: queries.subscription.relayPreview(previewNodeId, previewTunnelId, previewInPort),
    queryFn: ({ signal }) =>
      previewProxyNodeRelay(
        {
          nodeId: previewNodeId,
          tunnelId: previewTunnelId,
          inPort: previewInPort,
        },
        { signal },
      ),
    enabled: open && previewNodeId > 0 && previewTunnelId > 0,
    retry: false,
  });
  const preview = previewQuery.data ?? null;

  const relayMutation = useSubscriptionMutation("subscription.relay", {
    mutationFn: (payload: RelayWrite) => createProxyNodeRelay(payload),
    onSuccess: () => {
      notify.success("中转已创建");
      onOpenChange(false);
      onSaved?.();
    },
    onError: (error: Error) => notify.error(error.message || "中转创建失败"),
  });

  const submit = () => {
    const next: Record<string, string> = {};
    if (!form.tunnelId) next.tunnel = "请选择隧道";
    if (
      form.inPort &&
      (!Number.isInteger(Number(form.inPort)) ||
        Number(form.inPort) < 1 ||
        Number(form.inPort) > 65535)
    )
      next.port = "端口必须是 1–65535 的整数";
    setErrors(next);
    const first = Object.keys(next)[0];
    if (first) {
      document.getElementById(`subscription-relay-${first}`)?.focus();
      return;
    }
    if (!node) return;
    return relayMutation
      .mutateAsync({
        nodeId: node.id,
        tunnelId: Number(form.tunnelId),
        mode: form.mode,
        name: form.name,
        inPort: form.inPort ? Number(form.inPort) : null,
      })
      .catch(() => undefined);
  };

  return (
    <EntitySheet
      open={open}
      onOpenChange={onOpenChange}
      title="创建/修改中转"
      description={node?.name}
      dirty={dirty}
      onSubmit={submit}
      submitText="保存中转"
      submitting={relayMutation.isPending}
    >
      <div className="space-y-3">
        <Field label="模式" htmlFor="subscription-relay-mode">
          <Select
            value={form.mode}
            onValueChange={(v) => setForm({ ...form, mode: v as RelayMode })}
          >
            <SelectTrigger id="subscription-relay-mode">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="replace">替换原节点</SelectItem>
              <SelectItem value="append">增加中转节点</SelectItem>
            </SelectContent>
          </Select>
        </Field>
        <Field label="隧道" htmlFor="subscription-relay-tunnel" required error={errors.tunnel}>
          <Select value={form.tunnelId} onValueChange={(v) => setForm({ ...form, tunnelId: v })}>
            <SelectTrigger id="subscription-relay-tunnel">
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
        </Field>
        <Field
          label="入口端口（可选）"
          htmlFor="subscription-relay-port"
          error={errors.port}
          hint="留空由后端分配；填写时须在隧道入口允许的范围内"
        >
          <Input
            id="subscription-relay-port"
            type="number"
            value={form.inPort}
            onChange={(e) => setForm({ ...form, inPort: e.target.value })}
          />
        </Field>
        <Field label="名称" htmlFor="subscription-relay-name">
          <Input
            id="subscription-relay-name"
            value={form.name}
            onChange={(e) => setForm({ ...form, name: e.target.value })}
          />
        </Field>
        {previewQuery.isFetching && <LoadingRows rows={3} />}
        {previewQuery.error && (
          <ErrorState error={previewQuery.error} onRetry={() => void previewQuery.refetch()} />
        )}
        {preview && (
          <section className="space-y-3">
            <h3 className="text-body-strong text-ink">中转预览</h3>
            <p className="text-caption text-mute">
              {preview.tunnelName} · {preview.tunnelTypeName}
            </p>
            <DataTable
              ariaLabel="中转预览"
              columns={[
                { accessorKey: "role", header: "位置" },
                { accessorKey: "name", header: "节点" },
                {
                  accessorKey: "address",
                  header: "地址",
                  cell: ({ row }) => (
                    <span className="break-all font-mono text-code">{row.original.address}</span>
                  ),
                },
              ]}
              data={(
                [
                  ["入口", preview.entry],
                  ["中继", preview.relay],
                  ["出口", preview.exit],
                  ["目标", preview.target],
                ] as const
              )
                .filter((entry) => Boolean(entry[1]))
                .map(([role, endpoint]) => ({
                  role,
                  name: endpoint?.nodeName || "—",
                  address:
                    endpoint?.address ||
                    `${endpoint?.ip || ""}:${endpoint?.port || endpoint?.portText || ""}`,
                }))}
              getRowId={(row) => row.role}
              renderMobileCard={(row) => (
                <div className="space-y-1">
                  <h4 className="text-body-strong">
                    {row.role} · {row.name}
                  </h4>
                  <p className="break-all font-mono text-code text-body">{row.address}</p>
                </div>
              )}
            />
          </section>
        )}
      </div>
    </EntitySheet>
  );
}
