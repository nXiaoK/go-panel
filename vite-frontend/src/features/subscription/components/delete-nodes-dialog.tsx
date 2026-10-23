import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { FormField } from "@/components/patterns/form-field";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "@/components/ui/dialog";
import type { ProxyNode } from "@/lib/types";

export function SubscriptionDeleteNodesDialog({
  nodes,
  onClose,
  onDelete,
}: {
  nodes: ProxyNode[];
  onClose: () => void;
  onDelete: (deleteForward: boolean) => Promise<void>;
}) {
  const [deleteForward, setDeleteForward] = useState(false);
  const [pending, setPending] = useState(false);
  const [confirmation, setConfirmation] = useState("");
  const nameMatches = nodes.length !== 1 || confirmation === nodes[0].name;
  const associated = nodes.some((node) => node.forwardId || node.relayChildCount);
  const sourceWithChildren = nodes.some((node) => (node.relayChildCount || 0) > 0);
  const count = nodes.reduce(
    (total, node) => total + Number(Boolean(node.forwardId)) + (node.relayChildCount || 0),
    0,
  );
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !pending) onClose();
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {nodes.length === 1
              ? `删除订阅节点「${nodes[0].name}」`
              : `删除 ${nodes.length} 个协议节点`}
          </DialogTitle>
          <DialogDescription>删除后，节点将从关联订阅中移除，且无法恢复。</DialogDescription>
        </DialogHeader>
        {associated && (
          <div className="space-y-3">
            <label className="flex min-h-10 items-start gap-2 text-sm text-body">
              <Checkbox
                className="mt-1"
                checked={deleteForward}
                onCheckedChange={(value) => setDeleteForward(value === true)}
                disabled={pending}
              />
              同时删除关联的中转规则和中转节点（{count} 个）
            </label>
            <p className="text-caption text-mute">
              {sourceWithChildren
                ? "来源节点仍有关联中转节点，未勾选时后端会拒绝删除。请先关闭中转，或勾选以一并删除。"
                : "未勾选时只删除订阅节点，关联转发规则将保留并继续使用原目标地址，不再绑定该订阅节点。"}
            </p>
          </div>
        )}
        {nodes.length === 1 && (
          <FormField
            label="输入节点名称以确认删除"
            htmlFor="subscription-delete-node-name"
            required
            hint={`请输入「${nodes[0].name}」`}
          >
            <Input
              id="subscription-delete-node-name"
              value={confirmation}
              onChange={(event) => setConfirmation(event.target.value)}
              autoComplete="off"
              disabled={pending}
            />
          </FormField>
        )}
        <DialogFooter>
          <Button variant="outline" disabled={pending} onClick={onClose}>
            取消
          </Button>
          <Button
            variant="danger-solid"
            loading={pending}
            disabled={!nameMatches || (sourceWithChildren && !deleteForward)}
            onClick={async () => {
              if (pending) return;
              setPending(true);
              try {
                await onDelete(deleteForward);
              } finally {
                setPending(false);
              }
            }}
          >
            删除节点
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
