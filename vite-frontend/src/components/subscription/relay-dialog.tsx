import { useEffect, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { createProxyNodeRelay, previewProxyNodeRelay } from "@/lib/api";
import { queries, unwrap } from "@/lib/api/query";
import type { ProxyNode, RelayMode, SubTunnelOption } from "@/lib/types";
import { FormField as Field } from "@/components/page";

type RelayPreview = {
  tunnelName?: string;
  tunnelTypeName?: string;
  entry?: { address?: string; ip?: string; port?: number };
  target?: { address?: string; ip?: string; port?: number };
} | null;

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
  onSaved: () => void;
}) {
  const [form, setForm] = useState<{
    tunnelId: string;
    inPort: string;
    name: string;
    mode: RelayMode;
  }>({ tunnelId: "", inPort: "", name: "", mode: "replace" });

  useEffect(() => {
    if (!open || !node) return;
    const currentTunnelId =
      node.forwardTunnelId && tunnels.some((t) => t.id === node.forwardTunnelId)
        ? String(node.forwardTunnelId)
        : tunnels[0]?.id
          ? String(tunnels[0].id)
          : "";
    setForm({
      tunnelId: currentTunnelId,
      inPort: node.forwardInPort ? String(node.forwardInPort) : "",
      name: node.forwardName || `订阅节点-${node.name}`,
      mode: "replace",
    });
  }, [open, node, tunnels]);

  const previewNodeId = node?.id ?? 0;
  const previewTunnelId = Number(form.tunnelId) || 0;
  const previewInPort = form.inPort ? Number(form.inPort) : null;
  const previewQuery = useQuery({
    queryKey: queries.subscription.relayPreview(previewNodeId, previewTunnelId, previewInPort),
    queryFn: () =>
      previewProxyNodeRelay({
        nodeId: previewNodeId,
        tunnelId: previewTunnelId,
        inPort: previewInPort,
      }).then(unwrap) as Promise<RelayPreview>,
    enabled: open && previewNodeId > 0 && previewTunnelId > 0,
    retry: false,
  });
  const preview = previewQuery.data ?? null;

  const relayMutation = useMutation({
    mutationFn: (payload: Record<string, unknown>) => createProxyNodeRelay(payload).then(unwrap),
    onSuccess: () => {
      toast.success("中转已创建");
      onOpenChange(false);
      onSaved();
    },
    onError: (error: Error) => toast.error(error.message || "中转创建失败"),
  });

  const submit = () => {
    if (!node || !form.tunnelId) return toast.error("请选择隧道");
    relayMutation.mutate({
      nodeId: node.id,
      tunnelId: Number(form.tunnelId),
      mode: form.mode,
      name: form.name,
      inPort: form.inPort ? Number(form.inPort) : null,
    });
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>创建/修改中转</DialogTitle>
          <DialogDescription>{node?.name}</DialogDescription>
        </DialogHeader>
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
          <Field label="隧道" htmlFor="subscription-relay-tunnel">
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
          <Field label="入口端口（可选）" htmlFor="subscription-relay-port">
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
          {previewQuery.isFetching ? (
            <div className="rounded-md border bg-muted/40 p-3 text-xs text-muted-foreground">
              正在生成中转预览…
            </div>
          ) : null}
          {previewQuery.error ? (
            <div
              role="alert"
              className="rounded-md border border-destructive/30 bg-destructive/10 p-3 text-xs text-destructive"
            >
              {previewQuery.error instanceof Error
                ? previewQuery.error.message
                : "中转预览加载失败"}
            </div>
          ) : null}
          {preview && (
            <div className="rounded-md border bg-muted/40 p-3 text-xs">
              <div className="mb-1 font-medium">预览</div>
              <div>
                隧道：{preview.tunnelName} ({preview.tunnelTypeName})
              </div>
              <div>
                入口：
                {preview.entry?.address ||
                  `${preview.entry?.ip || ""}:${preview.entry?.port || ""}`}
              </div>
              <div>
                目标：
                {preview.target?.address ||
                  `${preview.target?.ip || ""}:${preview.target?.port || ""}`}
              </div>
            </div>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            取消
          </Button>
          <Button onClick={submit} disabled={relayMutation.isPending}>
            {relayMutation.isPending ? "创建中…" : "创建"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
