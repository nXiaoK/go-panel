import { useEffect, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { toast } from "sonner";
import { RefreshCcw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Separator } from "@/components/ui/separator";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { updateSubscriptionApiKey } from "@/lib/api";
import { unwrap } from "@/lib/api/query";

export function SubscriptionApiKeyDialog({
  open,
  onOpenChange,
  currentKey,
  onSaved,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  currentKey: string;
  onSaved: () => void;
}) {
  const [draft, setDraft] = useState("");

  useEffect(() => {
    if (open) setDraft(currentKey);
  }, [open, currentKey]);

  const saveMutation = useMutation({
    mutationFn: (value: string) => updateSubscriptionApiKey(value).then(unwrap),
    onSuccess: () => {
      toast.success("API Key 已更新");
      onOpenChange(false);
      onSaved();
    },
    onError: (error: Error) => toast.error(error.message || "更新失败"),
  });

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>节点上报 API Key</DialogTitle>
          <DialogDescription>脚本上报节点信息时使用</DialogDescription>
        </DialogHeader>
        <div className="space-y-2">
          <Label htmlFor="subscription-api-key">API Key</Label>
          <Input
            id="subscription-api-key"
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            className="font-mono"
            autoComplete="off"
            spellCheck={false}
          />
          <Separator />
          <div className="flex justify-between">
            <Button
              variant="outline"
              onClick={() => saveMutation.mutate("")}
              disabled={saveMutation.isPending}
            >
              <RefreshCcw className="mr-1" />
              随机生成
            </Button>
            <div className="flex gap-2">
              <Button variant="outline" onClick={() => onOpenChange(false)}>
                取消
              </Button>
              <Button onClick={() => saveMutation.mutate(draft)} disabled={saveMutation.isPending}>
                {saveMutation.isPending ? "保存中…" : "保存"}
              </Button>
            </div>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
