import { useEffect, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { toast } from "sonner";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { ScrollArea } from "@/components/ui/scroll-area";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { assignProxyNodeProfiles } from "@/lib/api";
import { unwrap } from "@/lib/api/query";
import type { ProxyNode, SubscriptionProfile } from "@/lib/types";
import { subscriptionFormatLabels } from "./formats";

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
  onSaved: () => void;
}) {
  const [profileIds, setProfileIds] = useState<number[]>([]);

  useEffect(() => {
    if (!open) return;
    setProfileIds(initialProfileIds);
  }, [open, initialProfileIds]);

  const saveMutation = useMutation({
    mutationFn: ({ nodeId, ids }: { nodeId: number; ids: number[] }) =>
      assignProxyNodeProfiles(nodeId, ids).then(unwrap),
    onSuccess: () => {
      toast.success("绑定已更新");
      onOpenChange(false);
      onSaved();
    },
    onError: (error: Error) => toast.error(error.message || "绑定失败"),
  });

  const save = () => {
    if (!node) return;
    saveMutation.mutate({ nodeId: node.id, ids: profileIds });
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>绑定订阅配置</DialogTitle>
          <DialogDescription>{node?.name}</DialogDescription>
        </DialogHeader>
        <ScrollArea className="h-64 rounded-md border p-2">
          {profiles.length === 0 && (
            <div className="p-4 text-center text-sm text-muted-foreground">暂无订阅配置</div>
          )}
          {profiles.map((p) => {
            const checked = profileIds.includes(p.id);
            return (
              <label
                key={p.id}
                className="flex cursor-pointer items-center gap-2 rounded px-2 py-1 hover:bg-muted"
              >
                <Checkbox
                  checked={checked}
                  onCheckedChange={(c) => {
                    setProfileIds(c ? [...profileIds, p.id] : profileIds.filter((x) => x !== p.id));
                  }}
                />
                <span className="flex-1 text-sm">{p.name}</span>
                <Badge variant="outline">{subscriptionFormatLabels[p.defaultFormat]}</Badge>
              </label>
            );
          })}
        </ScrollArea>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
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
