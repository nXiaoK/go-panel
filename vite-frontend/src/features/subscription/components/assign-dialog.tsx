import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { DataTable } from "@/components/patterns/data-table";
import type { RowSelectionState } from "@tanstack/react-table";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { subscriptionFormatLabels } from "@/features/subscription/model";
import { useSubscriptionMutation } from "@/features/subscription/queries";
import { assignProxyNodeProfiles } from "@/lib/api";
import { notify } from "@/lib/notify";
import type { ProxyNode, SubscriptionProfile } from "@/lib/types";
import { useState } from "react";

export function SubscriptionAssignDialog({
  open,
  onOpenChange,
  node,
  profiles,
  initialProfileIds,
  onSaved,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  node: ProxyNode | null;
  profiles: SubscriptionProfile[];
  /** Profile ids to preselect when the dialog opens. */
  initialProfileIds: number[];
  onSaved?: () => void;
}) {
  // 仅在打开时预选，避免父级刷新覆盖用户在 Dialog 中尚未保存的选择。
  const [selection, setSelection] = useState<RowSelectionState>(() =>
    Object.fromEntries(initialProfileIds.map((id) => [String(id), true])),
  );
  const saveMutation = useSubscriptionMutation("subscription.update", {
    mutationFn: ({ nodeId, ids }: { nodeId: number; ids: number[] }) =>
      assignProxyNodeProfiles(nodeId, ids),
    onSuccess: () => {
      notify.success("绑定已更新");
      onOpenChange(false);
      onSaved?.();
    },
    onError: (error: Error) => notify.error(error.message || "绑定失败"),
  });

  const save = () => {
    if (!node) return;
    saveMutation.mutate({
      nodeId: node.id,
      ids: profiles.filter((profile) => selection[String(profile.id)]).map((profile) => profile.id),
    });
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!saveMutation.isPending) onOpenChange(next);
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>绑定订阅配置</DialogTitle>
          <DialogDescription>{node?.name}</DialogDescription>
        </DialogHeader>
        <DataTable
          ariaLabel="选择订阅配置"
          columns={[
            { accessorKey: "name", header: "名称" },
            {
              accessorKey: "defaultFormat",
              header: "格式",
              cell: ({ row }) => (
                <Badge variant="outline">
                  {subscriptionFormatLabels[row.original.defaultFormat]}
                </Badge>
              ),
            },
          ]}
          data={profiles}
          getRowId={(profile) => String(profile.id)}
          enableSelection
          rowSelection={selection}
          onRowSelectionChange={setSelection}
          renderMobileCard={(profile) => (
            <div className="flex flex-wrap items-center justify-between gap-2">
              <span className="text-body">{profile.name}</span>
              <Badge variant="outline">{subscriptionFormatLabels[profile.defaultFormat]}</Badge>
            </div>
          )}
        />
        <DialogFooter>
          <Button
            variant="outline"
            disabled={saveMutation.isPending}
            onClick={() => onOpenChange(false)}
          >
            取消
          </Button>
          <Button onClick={save} disabled={saveMutation.isPending || !node}>
            {saveMutation.isPending ? "保存中…" : "保存"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
