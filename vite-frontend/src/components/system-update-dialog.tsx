import { useState } from "react";
import { Check, Copy, ExternalLink, Loader2, RefreshCw } from "lucide-react";

import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { applyPanelUpdate, getSystemVersion } from "@/lib/api";
import { isAdministrator } from "@/lib/capabilities";
import { notify } from "@/lib/notify";
import { getRoleID } from "@/lib/session";
import type { PanelUpdateStatus } from "@/lib/types";
import { dismissUpdate, MANUAL_UPDATE_COMMAND } from "@/lib/update";

interface SystemUpdateDialogProps {
  open: boolean;
  status: PanelUpdateStatus | null;
  onOpenChange: (open: boolean) => void;
}

function delay(milliseconds: number) {
  return new Promise((resolve) => window.setTimeout(resolve, milliseconds));
}

async function waitForUpdatedPanel(targetVersion: string) {
  // 更新期间连接失败属于预期；最多等待三分钟，恢复后刷新静态前端资源。
  for (let attempt = 0; attempt < 60; attempt += 1) {
    await delay(3_000);
    const response = await getSystemVersion().catch(() => null);
    if (response?.code === 0 && response.data?.version === targetVersion) {
      window.location.reload();
      return;
    }
  }
  notify.warning("更新请求已发送，但暂未确认新版本启动；请检查容器日志");
}

export function SystemUpdateDialog({ open, status, onOpenChange }: SystemUpdateDialogProps) {
  const [applying, setApplying] = useState(false);
  const [copied, setCopied] = useState(false);
  const administrator = isAdministrator(getRoleID());
  const latestVersion = status?.latestVersion || "新版本";

  const dismiss = () => {
    dismissUpdate(status?.latestVersion);
    onOpenChange(false);
  };

  const copyManualCommand = async () => {
    try {
      await navigator.clipboard.writeText(MANUAL_UPDATE_COMMAND);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2_000);
    } catch {
      notify.error("复制失败，请手动选择升级命令");
    }
  };

  const applyUpdate = async () => {
    if (!status?.latestVersion || applying) return;
    setApplying(true);
    const response = await applyPanelUpdate().catch(() => null);
    if (!response || response.code !== 0 || !response.data?.started) {
      setApplying(false);
      notify.error(response?.msg || "更新请求失败，请检查更新侧车日志");
      return;
    }
    notify.success(`已备份数据库并开始更新到 ${response.data.targetVersion}`);
    onOpenChange(false);
    void waitForUpdatedPanel(response.data.targetVersion);
  };

  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle className="flex items-center gap-2">
            <RefreshCw className="h-5 w-5 text-primary" />
            发现新版本 {latestVersion}
          </AlertDialogTitle>
          <AlertDialogDescription>
            当前版本 {status?.current.version || "未知"}
            。更新前请查看发布说明，确认没有需要手动调整的 Compose 或环境变量。
          </AlertDialogDescription>
        </AlertDialogHeader>

        {status?.releaseNotes && (
          <div className="max-h-48 overflow-auto whitespace-pre-wrap rounded-md border border-border bg-muted/40 p-3 text-xs leading-relaxed text-muted-foreground">
            {status.releaseNotes}
          </div>
        )}

        {status && (!status.autoUpdateConfigured || !administrator) && (
          <div className="space-y-2 rounded-md border border-border bg-muted/30 p-3">
            <p className="text-xs text-muted-foreground">
              {administrator
                ? "当前未启用安全更新侧车，请在部署目录执行："
                : "自动更新仅允许管理员执行，也可以由服务器管理员运行："}
            </p>
            <div className="flex items-center gap-2">
              <code className="min-w-0 flex-1 overflow-x-auto rounded bg-background px-2 py-1.5 text-[11px]">
                {MANUAL_UPDATE_COMMAND}
              </code>
              <Button type="button" variant="outline" size="icon" onClick={copyManualCommand}>
                {copied ? <Check className="h-4 w-4" /> : <Copy className="h-4 w-4" />}
                <span className="sr-only">复制升级命令</span>
              </Button>
            </div>
          </div>
        )}

        <AlertDialogFooter>
          <AlertDialogCancel onClick={dismiss}>稍后提醒</AlertDialogCancel>
          {status?.releaseUrl && (
            <Button variant="outline" asChild>
              <a href={status.releaseUrl} target="_blank" rel="noreferrer">
                发布说明
                <ExternalLink className="ml-1.5 h-3.5 w-3.5" />
              </a>
            </Button>
          )}
          {administrator && status?.autoUpdateConfigured && (
            <Button type="button" onClick={() => void applyUpdate()} disabled={applying}>
              {applying && <Loader2 className="mr-1.5 h-4 w-4 animate-spin" />}
              {applying ? "准备更新…" : "立即更新"}
            </Button>
          )}
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
