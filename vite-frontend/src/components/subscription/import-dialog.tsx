import { useEffect, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { toast } from "sonner";
import { Download } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { Label } from "@/components/ui/label";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { importProxyNodeLink } from "@/lib/api";
import { unwrap } from "@/lib/api/query";

export function SubscriptionImportDialog({
  open,
  onOpenChange,
  onSaved,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSaved: () => void;
}) {
  const [link, setLink] = useState("");

  useEffect(() => {
    if (open) setLink("");
  }, [open]);

  const importMutation = useMutation({
    mutationFn: (value: string) => importProxyNodeLink(value).then(unwrap),
    onSuccess: () => {
      toast.success("节点已导入");
      onOpenChange(false);
      onSaved();
    },
    onError: (error: Error) => toast.error(error.message || "导入失败"),
  });

  const submit = () => {
    if (!link.trim()) return toast.error("请输入分享链接");
    importMutation.mutate(link.trim());
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>导入分享链接</DialogTitle>
          <DialogDescription>
            支持 vless:// / vmess:// / trojan:// / ss:// / snell:// 等格式
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-1.5">
          <Label htmlFor="subscription-import-link">分享链接</Label>
          <Textarea
            id="subscription-import-link"
            rows={5}
            value={link}
            onChange={(e) => setLink(e.target.value)}
            placeholder="粘贴分享链接"
          />
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
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
