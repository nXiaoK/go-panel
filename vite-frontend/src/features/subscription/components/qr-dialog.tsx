import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { useSubscriptionQrDataUrl } from "@/features/subscription/qr";
import { notify } from "@/lib/notify";
import { Copy, Download, Loader2 } from "lucide-react";

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
      notify.success("链接已复制");
    } catch {
      notify.error("复制失败");
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>扫描二维码导入订阅，或下载本地 PNG 图片</DialogDescription>
        </DialogHeader>
        <div className="flex flex-col items-center gap-3">
          {dataUrl ? (
            /* 扫码需要白色衬底，这是 DESIGN.md 明确允许的二维码例外；PNG 自身同样使用白底黑码。 */
            <div className="max-w-full rounded-md p-3" style={{ backgroundColor: "#ffffff" }}>
              <img
                className="h-auto max-w-full"
                src={dataUrl}
                alt="订阅二维码"
                width={256}
                height={256}
              />
            </div>
          ) : error ? (
            <p role="alert" className="py-24 text-sm text-status-critical-text">
              {error}
            </p>
          ) : (
            <div className="flex h-64 w-64 items-center justify-center text-sm text-mute">
              <Loader2 className="mr-2 animate-spin" />
              正在生成二维码…
            </div>
          )}
          <div className="w-full break-all rounded-md border bg-canvas-soft p-2 text-center font-mono text-xs">
            {value}
          </div>
          <p className="text-caption text-body">{title}</p>
          <div className="flex flex-wrap gap-2">
            {dataUrl && (
              <Button size="sm" variant="outline" asChild>
                <a
                  href={dataUrl}
                  download={`${title.replace(/[\\/:*?"<>|]/g, "-") || "subscription"}.png`}
                >
                  <Download />
                  下载 PNG
                </a>
              </Button>
            )}
            <Button size="sm" variant="outline" onClick={copy}>
              <Copy className="mr-1" />
              复制
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
