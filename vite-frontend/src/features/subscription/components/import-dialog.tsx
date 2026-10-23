import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { FormField } from "@/components/patterns/form-field";
import { Textarea } from "@/components/ui/textarea";
import { useSubscriptionMutation } from "@/features/subscription/queries";
import { importProxyNodeLink } from "@/lib/api";
import { notify } from "@/lib/notify";
import { Download } from "lucide-react";
import { useEffect, useState } from "react";

export function SubscriptionImportDialog({
  open,
  onOpenChange,
  onSaved,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSaved?: () => void;
}) {
  const [link, setLink] = useState("");
  const [error, setError] = useState("");

  useEffect(() => {
    if (open) {
      setLink("");
      setError("");
    }
  }, [open]);

  const importMutation = useSubscriptionMutation("subscription.update", {
    mutationFn: (value: string) => importProxyNodeLink(value),
    onSuccess: () => {
      notify.success("节点已导入");
      onOpenChange(false);
      onSaved?.();
    },
    onError: (error: Error) => setError(error.message || "导入失败"),
  });

  const submit = () => {
    if (!link.trim()) {
      setError("请输入分享链接");
      document.getElementById("subscription-import-link")?.focus();
      return;
    }
    setError("");
    importMutation.mutate(link.trim());
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!importMutation.isPending) onOpenChange(next);
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>导入分享链接</DialogTitle>
          <DialogDescription>
            支持 vless:// / vmess:// / trojan:// / ss:// / snell:// 等格式
          </DialogDescription>
        </DialogHeader>
        <FormField label="分享链接" htmlFor="subscription-import-link" required error={error}>
          <Textarea
            id="subscription-import-link"
            rows={5}
            value={link}
            onChange={(e) => setLink(e.target.value)}
            placeholder="粘贴分享链接"
          />
        </FormField>
        <DialogFooter>
          <Button
            variant="outline"
            disabled={importMutation.isPending}
            onClick={() => onOpenChange(false)}
          >
            取消
          </Button>
          <Button onClick={submit} disabled={importMutation.isPending}>
            <Download className="mr-1" />
            {importMutation.isPending ? "导入中…" : "导入"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
