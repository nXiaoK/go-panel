import {
  AlertCircle,
  AlertTriangle,
  ArrowUpCircle,
  CircleCheck,
  ExternalLink,
  RefreshCw,
} from "lucide-react";
import type { ReactNode } from "react";

import { CodeBlock } from "@/components/patterns/code-block";
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
import { formatDate } from "@/lib/format";
import type { PanelUpdateJob, PanelUpdateResult, PanelUpdateStatus } from "@/lib/types";
import {
  canCancelUpdateJob,
  dismissUpdate,
  displayVersion,
  PANEL_LOGS_COMMAND,
  resultStageLabel,
} from "@/lib/update";
import {
  panelUpdateStore,
  usePanelUpdate,
  type PanelUpdateSnapshot,
  type RestartTarget,
} from "../update-store";
import {
  RestartProgress,
  UpdateActionButtons,
  UpdateEligibility,
  UpdateJobProgress,
  UpdateNotice,
  UpdateResultDetails,
  UpdateResultPill,
} from "./update-parts";

interface SystemUpdateDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

type View =
  | { kind: "eligibility"; status: PanelUpdateStatus }
  | { kind: "job"; job?: PanelUpdateJob; targetVersion: string }
  | { kind: "wait"; phase: "restarting" | "confirming" | "reloading"; target: RestartTarget }
  | { kind: "result"; result: PanelUpdateResult }
  | { kind: "timeout"; target: RestartTarget }
  | { kind: "empty" };

function dialogView({ status, flow }: PanelUpdateSnapshot): View {
  switch (flow.phase) {
    case "job": {
      // 状态里可能仍是上一次已结束的作业，只展示本次作业的阶段。
      const job = !flow.jobId || status?.job?.id === flow.jobId ? status?.job : undefined;
      return { kind: "job", job, targetVersion: flow.targetVersion };
    }
    case "restarting":
    case "confirming":
    case "reloading":
      return { kind: "wait", phase: flow.phase, target: flow.target };
    case "result":
      return { kind: "result", result: flow.result };
    case "timeout":
      return { kind: "timeout", target: flow.target };
    default:
      return status ? { kind: "eligibility", status } : { kind: "empty" };
  }
}

function waitTitle(phase: "restarting" | "confirming" | "reloading", target: RestartTarget) {
  if (phase === "reloading") return "正在刷新页面";
  if (phase === "confirming") return `${target.targetVersion} 试运行中`;
  if (target.action === "use-builtin") return `正在恢复内置版本 ${target.targetVersion}`;
  if (target.action === "rollback") return `正在回滚到 ${target.targetVersion}`;
  return "面板正在重启";
}

function waitDescription(phase: "restarting" | "confirming" | "reloading", target: RestartTarget) {
  if (phase === "reloading") return `面板已切换到 ${target.targetVersion}，正在加载新版界面`;
  if (phase === "confirming")
    return "新版本已启动，正在进行约 1 分钟的稳定性检查；检查未通过会自动回滚";
  if (target.kind === "sidecar")
    return "更新侧车正在拉取新镜像并重建容器，新版本就绪后页面会自动刷新";
  return "面板约中断 1 分钟，节点转发不受影响；新版本就绪后页面会自动刷新";
}

function resultTitle(result: PanelUpdateResult) {
  if (result.outcome === "rolled_back") return "更新失败，已自动回滚";
  if (result.outcome === "rejected") return "切换请求被拒绝";
  if (result.action === "rollback") return `已回滚到 ${result.version}`;
  if (result.action === "use-builtin") return `已恢复内置版本 ${result.version}`;
  return `已更新到 ${result.version}`;
}

function resultDescription(result: PanelUpdateResult) {
  if (result.outcome === "rolled_back") {
    const stage = resultStageLabel(result.stage);
    return `${result.version} 试运行失败${stage ? `（${stage}）` : ""}，面板已恢复到原版本`;
  }
  if (result.outcome === "rejected")
    return `切换到 ${result.version} 的请求未通过启动前校验，面板仍在运行原版本`;
  return `面板正在运行 ${result.version}`;
}

const titleIcon = "size-5 shrink-0";

export function SystemUpdateDialog({ open, onOpenChange }: SystemUpdateDialogProps) {
  const snapshot = usePanelUpdate();
  const view = dialogView(snapshot);
  const close = <AlertDialogCancel>关闭</AlertDialogCancel>;

  let icon: ReactNode = <RefreshCw aria-hidden="true" className={`${titleIcon} text-mute`} />;
  let title = "面板版本";
  let description = "正在读取更新状态";
  let body: ReactNode = null;
  let footer: ReactNode = close;

  if (view.kind === "eligibility") {
    const { status } = view;
    const published = status.publishedAt ? Date.parse(status.publishedAt) : NaN;
    if (status.updateAvailable) {
      icon = <ArrowUpCircle aria-hidden="true" className={`${titleIcon} text-status-warning`} />;
      title = `发现新版本 ${status.latestVersion || ""}`.trim();
    }
    description = `当前版本 ${displayVersion(status.current.version, status.current.commit)}${
      Number.isFinite(published) ? `，新版本发布于 ${formatDate(published)}` : ""
    }；更新前请先查看发布说明`;
    body = (
      <div className="space-y-4">
        {status.releaseNotes && (
          <div className="max-h-48 overflow-y-auto whitespace-pre-wrap break-words rounded-md border border-hairline bg-canvas-soft p-3 text-caption leading-relaxed text-body">
            {status.releaseNotes}
          </div>
        )}
        {status.updateAvailable ? (
          <UpdateEligibility status={status} />
        ) : (
          <p className="text-sm text-body">当前已是最新稳定版</p>
        )}
      </div>
    );
    footer = (
      <>
        {status.updateAvailable ? (
          // “稍后提醒”只记住当前版本，下一个新版本仍会提示。
          <AlertDialogCancel onClick={() => dismissUpdate(status.latestVersion)}>
            稍后提醒
          </AlertDialogCancel>
        ) : (
          close
        )}
        {status.releaseUrl && (
          <Button variant="outline" asChild>
            <a href={status.releaseUrl} target="_blank" rel="noreferrer">
              发布说明
              <ExternalLink aria-hidden="true" />
            </a>
          </Button>
        )}
        <UpdateActionButtons status={status} />
      </>
    );
  } else if (view.kind === "job") {
    title = `正在更新到 ${view.targetVersion}`;
    description = "关闭弹窗不会中断更新，可随时从侧栏的“更新中”重新打开";
    body = <UpdateJobProgress job={view.job} />;
    footer = (
      <>
        {close}
        {canCancelUpdateJob(view.job) && (
          <Button
            type="button"
            variant="outline"
            loading={snapshot.busy === "cancel"}
            disabled={snapshot.busy !== null}
            onClick={() => void panelUpdateStore.cancel()}
          >
            取消更新
          </Button>
        )}
      </>
    );
  } else if (view.kind === "wait") {
    title = waitTitle(view.phase, view.target);
    description = waitDescription(view.phase, view.target);
    body = <RestartProgress phase={view.phase} target={view.target} />;
  } else if (view.kind === "result") {
    const { result } = view;
    icon =
      result.outcome === "committed" ? (
        <CircleCheck aria-hidden="true" className={`${titleIcon} text-status-good`} />
      ) : result.outcome === "rolled_back" ? (
        <AlertCircle aria-hidden="true" className={`${titleIcon} text-status-critical`} />
      ) : (
        <AlertTriangle aria-hidden="true" className={`${titleIcon} text-status-warning`} />
      );
    title = resultTitle(result);
    description = resultDescription(result);
    body = (
      <div className="space-y-3">
        <div className="flex flex-wrap items-center gap-2">
          <UpdateResultPill result={result} />
          <span className="font-mono text-sm text-body">{result.version}</span>
        </div>
        <UpdateResultDetails result={result} />
      </div>
    );
  } else if (view.kind === "timeout") {
    const minutes = Math.round((view.target.deadline - view.target.startedAt) / 60_000);
    icon = <AlertTriangle aria-hidden="true" className={`${titleIcon} text-status-warning`} />;
    title = "暂未确认更新结果";
    description = `已等待约 ${minutes} 分钟，仍未确认 ${view.target.targetVersion} 的运行状态`;
    body = (
      <div className="space-y-3">
        <UpdateNotice tone="warning">
          面板可能仍在启动，或已经回滚到原版本。请在部署目录查看容器日志：
        </UpdateNotice>
        <CodeBlock code={PANEL_LOGS_COMMAND} label="复制日志命令" />
      </div>
    );
    footer = (
      <>
        {close}
        <Button type="button" variant="outline" onClick={() => panelUpdateStore.reloadPage()}>
          刷新页面
        </Button>
      </>
    );
  }

  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle className="flex items-center gap-2">
            {icon}
            {title}
          </AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
        </AlertDialogHeader>
        {body}
        <AlertDialogFooter>{footer}</AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
