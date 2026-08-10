import { Copy, Loader2 } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { useSubscriptionQrDataUrl } from "@/lib/subscription-qr";

export function SubscriptionQrDialog({
  open,
  title,
  value,
  onOpenChange,
}: {
  open: boolean;
  title: string;
  value: string;
  onOpenChange: (open: boolean) => void;
}) {
  const { dataUrl, error } = useSubscriptionQrDataUrl(open, value);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      toast.success("链接已复制");
    } catch {
      toast.error("复制失败");
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
        </DialogHeader>
        <div className="flex flex-col items-center gap-3">
          {dataUrl ? (
            <img src={dataUrl} alt="订阅二维码" width={256} height={256} />
          ) : error ? (
            <p role="alert" className="py-24 text-sm text-destructive">
              {error}
            </p>
          ) : (
            <div className="flex h-64 w-64 items-center justify-center text-sm text-muted-foreground">
              <Loader2 className="mr-2 animate-spin" />
              正在生成二维码…
            </div>
          )}
          <div className="w-full break-all rounded-md border bg-muted/40 p-2 text-center font-mono text-xs">
            {value}
          </div>
          <Button size="sm" variant="outline" onClick={copy}>
            <Copy className="mr-1" />
            复制
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
