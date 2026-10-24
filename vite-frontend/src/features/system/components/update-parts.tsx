import {
  AlertCircle,
  AlertTriangle,
  ChevronRight,
  Circle,
  CircleCheck,
  Download,
  Info,
  LoaderCircle,
  RefreshCw,
  RotateCcw,
} from "lucide-react";
import type { ReactNode } from "react";
import { CodeBlock } from "@/components/patterns/code-block";
import { StatusPill } from "@/components/patterns/status-pill";
import { Button } from "@/components/ui/button";
import { formatBytes, formatDateTime, formatRelative } from "@/lib/format";
import type { PanelUpdateJob, PanelUpdateResult, PanelUpdateStatus } from "@/lib/types";
import {
  downloadPercent,
  formatDownloadProgress,
  MANUAL_UPDATE_COMMAND,
  primaryUpdateAction,
  resultLabel,
  resultStageLabel,
  resultTone,
  softUpdateReasonText,
  updateStageLabel,
  updateStageStates,
  type UpdateStageState,
} from "@/lib/update";
import { cn } from "@/lib/utils";
import {
  isPanelUpdateBusy,
  usePanelUpdate,
  type PanelUpdateSnapshot,
  type RestartTarget,
} from "../update-store";
import { useUpdateActions } from "../use-update-actions";

// 只复用规范中已验证的文字/底色组合；边框与图标不单独承载信息。
const noticeStyles = {
  info: { box: "border-hairline bg-canvas-soft text-body", icon: Info, iconColor: "text-mute" },
  warning: {
    box: "border-status-warning bg-status-warning-bg text-status-warning",
    icon: AlertTriangle,
    iconColor: "",
  },
  critical: {
    box: "border-status-critical bg-status-critical-bg text-status-critical-text",
    icon: AlertCircle,
    iconColor: "",
  },
} as const;

export function UpdateNotice({
  tone,
  alert = false,
  children,
}: {
  tone: keyof typeof noticeStyles;
  alert?: boolean;
  children: ReactNode;
}) {
  const style = noticeStyles[tone];
  const Icon = style.icon;
  return (
    <div
      role={alert ? "alert" : undefined}
      className={cn("flex items-start gap-2 rounded-md border p-3 text-sm", style.box)}
    >
      <Icon aria-hidden="true" className={cn("mt-0.5 size-4 shrink-0", style.iconColor)} />
      <div className="min-w-0 break-words">{children}</div>
    </div>
  );
}

/** 资格说明：可软更新时说明影响，否则给出原因码说明与手动命令。 */
export function UpdateEligibility({ status }: { status: PanelUpdateStatus }) {
  const action = primaryUpdateAction(status);
  const { softUpdate, runtime, job } = status;
  // UPDATE_IMAGE_TAG 不是 latest 即固定标签：软更新后运行版本会高于镜像，手动更新需先改标签。
  const pinnedTag = runtime.imageTag !== "latest" ? runtime.imageTag : null;
  const showReason = action !== "soft" && softUpdate.reason && softUpdate.reason !== "no_update";
  return (
    <div className="space-y-3">
      {job?.stage === "failed" && (
        <UpdateNotice tone="critical" alert>
          更新到 {job.targetVersion} 失败：{job.error || "请查看容器日志"}
        </UpdateNotice>
      )}
      {job?.stage === "canceled" && (
        <UpdateNotice tone="info">已取消更新到 {job.targetVersion}</UpdateNotice>
      )}
      {action === "soft" && (
        <>
          <p className="text-sm text-body">
            无需重建容器；更新期间面板约中断 1 分钟，节点转发不受影响；失败会自动回滚
          </p>
          {softUpdate.downloadBytes ? (
            <p className="text-caption text-mute">下载约 {formatBytes(softUpdate.downloadBytes)}</p>
          ) : null}
          {pinnedTag && (
            <UpdateNotice tone="info">
              软更新后运行版本将高于镜像标签 <span className="font-mono">{pinnedTag}</span>
            </UpdateNotice>
          )}
        </>
      )}
      {showReason && (
        <UpdateNotice tone={softUpdate.reason === "quarantined" ? "critical" : "warning"}>
          {softUpdateReasonText(softUpdate.reason, softUpdate.detail)}
        </UpdateNotice>
      )}
      {(action === "sidecar" || action === "manual") && (
        <>
          <p className="text-sm text-body">
            {action === "sidecar"
              ? "可以由更新侧车拉取新镜像并重建容器，更新前会自动备份数据库；也可以在部署目录手动执行："
              : "请在部署目录执行以下命令更新镜像："}
          </p>
          {pinnedTag && (
            <p className="text-caption text-mute">
              镜像标签固定为 <span className="font-mono">{pinnedTag}</span>，请先把 .env 中的
              GO_PANEL_VERSION 改为 <span className="font-mono">{status.latestVersion}</span> 再执行
            </p>
          )}
          <CodeBlock code={MANUAL_UPDATE_COMMAND} label="复制更新命令" />
        </>
      )}
    </div>
  );
}

/** 每个区域最多一个主按钮：软更新或侧车二选一；隔离版本另给危险描边的“仍要重试”。 */
export function UpdateActionButtons({ status }: { status: PanelUpdateStatus }) {
  const snapshot = usePanelUpdate();
  const actions = useUpdateActions();
  const action = primaryUpdateAction(status);
  const blocked = snapshot.busy !== null || isPanelUpdateBusy(snapshot);
  return (
    <>
      {status.softUpdate.reason === "quarantined" && action !== "none" && (
        <Button
          type="button"
          variant="danger"
          loading={snapshot.busy === "retry"}
          disabled={blocked}
          onClick={() => void actions.retryQuarantined()}
        >
          <RotateCcw aria-hidden="true" />
          仍要重试
        </Button>
      )}
      {(action === "soft" || action === "sidecar") && (
        <Button
          type="button"
          loading={snapshot.busy === "apply"}
          disabled={blocked}
          onClick={() => void actions.start()}
        >
          <Download aria-hidden="true" />
          {action === "soft" ? "立即更新" : "通过更新侧车更新"}
        </Button>
      )}
    </>
  );
}

const stageStateText: Record<UpdateStageState, string> = {
  done: "已完成",
  current: "进行中",
  pending: "未开始",
};

// 阶段状态同时用图标形状（勾选/转圈/空心圆）和文字区分，不只依赖颜色。
function StageIcon({ state }: { state: UpdateStageState }) {
  if (state === "done")
    return <CircleCheck aria-hidden="true" className="size-4 shrink-0 text-status-good" />;
  if (state === "current")
    return (
      <LoaderCircle
        aria-hidden="true"
        className="size-4 shrink-0 animate-spin text-ink motion-reduce:animate-none"
      />
    );
  return <Circle aria-hidden="true" className="size-4 shrink-0 text-mute" />;
}

function DownloadProgress({ done, total }: { done?: number; total?: number }) {
  const percent = downloadPercent(done, total);
  const text = formatDownloadProgress(done, total);
  return (
    <div className="space-y-2">
      {/* 填充 status-good、轨道 hairline-subtle：两套主题下填充与轨道、画布均 ≥ 3:1。 */}
      <div
        role="progressbar"
        aria-label="下载进度"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={percent ?? undefined}
        aria-valuetext={percent === null ? text : `${text}（${percent}%）`}
        className="h-1.5 overflow-hidden rounded-pill bg-hairline-subtle"
      >
        {percent !== null && (
          <div
            className="h-full rounded-pill bg-status-good transition-[width] duration-150 motion-reduce:transition-none"
            style={{ width: `${percent}%` }}
          />
        )}
      </div>
      <div className="flex items-center justify-between gap-2 font-mono text-caption tabular text-body">
        <span>{text}</span>
        {percent !== null && <span>{percent}%</span>}
      </div>
    </div>
  );
}

export function UpdateJobProgress({ job }: { job?: PanelUpdateJob }) {
  const stage = job?.stage ?? "queued";
  return (
    <div className="space-y-4">
      {stage === "queued" && (
        <p className="text-sm text-body">{updateStageLabel("queued")}：作业已创建，即将开始下载</p>
      )}
      <ol aria-label="更新阶段" className="space-y-2">
        {updateStageStates(stage).map((step) => (
          <li
            key={step.stage}
            aria-current={step.state === "current" ? "step" : undefined}
            className="flex min-h-6 items-center gap-2 text-sm"
          >
            <StageIcon state={step.state} />
            <span
              className={
                step.state === "current"
                  ? "font-semibold text-ink"
                  : step.state === "done"
                    ? "text-body"
                    : "text-mute"
              }
            >
              {step.label}
            </span>
            <span
              className={step.state === "current" ? "ml-auto text-caption text-mute" : "sr-only"}
            >
              {stageStateText[step.state]}
            </span>
          </li>
        ))}
      </ol>
      {stage === "downloading" && (
        <DownloadProgress done={job?.bytesDone} total={job?.bytesTotal} />
      )}
    </div>
  );
}

export function RestartProgress({
  phase,
  target,
}: {
  phase: "restarting" | "confirming" | "reloading";
  target: RestartTarget;
}) {
  const minutes = Math.max(1, Math.ceil((target.deadline - target.startedAt) / 60_000));
  const text =
    phase === "reloading"
      ? "正在刷新页面以加载新版界面"
      : phase === "confirming"
        ? `正在确认 ${target.targetVersion} 运行稳定`
        : target.kind === "sidecar"
          ? "正在等待新容器启动"
          : "正在等待面板重新上线";
  return (
    <div
      role="status"
      className="flex items-start gap-3 rounded-md border border-hairline bg-canvas-soft p-4"
    >
      <LoaderCircle
        aria-hidden="true"
        className="mt-0.5 size-4 shrink-0 animate-spin text-mute motion-reduce:animate-none"
      />
      <div className="min-w-0 space-y-1">
        <p className="text-sm text-ink">{text}</p>
        {phase !== "reloading" && (
          <p className="text-caption text-mute">
            重启期间请求暂时失败属于正常现象，最长等待约 {minutes} 分钟
          </p>
        )}
      </div>
    </div>
  );
}

export function UpdateResultPill({
  result,
}: {
  result: Pick<PanelUpdateResult, "outcome" | "action">;
}) {
  return (
    <StatusPill
      tone={resultTone(result.outcome)}
      label={resultLabel(result)}
      icon={
        result.outcome === "rolled_back"
          ? AlertCircle
          : result.outcome === "rejected"
            ? AlertTriangle
            : undefined
      }
    />
  );
}

/** 列表式相对时间，悬停显示绝对时间；服务端时钟略快时按“刚刚”显示，不出现“即将过期”。 */
export function RelativeTime({ at }: { at: number }) {
  if (!Number.isFinite(at) || at <= 0) return <span className="text-caption text-mute">—</span>;
  return (
    <time
      dateTime={new Date(at).toISOString()}
      title={formatDateTime(at)}
      className="text-caption text-mute"
    >
      {formatRelative(Math.min(at, Date.now()))}
    </time>
  );
}

/** 回滚详情默认折叠；数据库已恢复快照时必须额外告警，提示试运行期间的写入已丢失。 */
export function UpdateResultDetails({ result }: { result: PanelUpdateResult }) {
  const stage = resultStageLabel(result.stage);
  const hasExitCode = typeof result.exitCode === "number";
  const hasDetail =
    result.outcome !== "committed" && (Boolean(result.detail) || hasExitCode || Boolean(stage));
  if (!hasDetail && !result.dbRestored) return null;
  return (
    <div className="space-y-3">
      {hasDetail && (
        <details className="group rounded-md border border-hairline">
          <summary className="flex min-h-9 cursor-pointer list-none items-center gap-2 px-3 text-sm text-body max-md:min-h-10 [&::-webkit-details-marker]:hidden">
            <ChevronRight
              aria-hidden="true"
              className="size-4 shrink-0 text-mute transition-transform duration-150 group-open:rotate-90 motion-reduce:transition-none"
            />
            错误详情
          </summary>
          <div className="space-y-2 border-t border-hairline p-3 text-caption text-body">
            {stage && <p>失败阶段：{stage}</p>}
            {hasExitCode && (
              <p>
                退出码：<span className="font-mono">{result.exitCode}</span>
              </p>
            )}
            {result.detail && (
              <pre className="max-h-48 overflow-y-auto whitespace-pre-wrap break-all rounded-sm bg-canvas-soft p-2 font-mono text-code text-canvas-text-soft">
                {result.detail}
              </pre>
            )}
          </div>
        </details>
      )}
      {result.dbRestored && (
        <UpdateNotice tone="critical" alert>
          已恢复更新前的数据库快照，试运行期间的修改已丢失；失败时的数据库保存在
          backups/failed-update-*
        </UpdateNotice>
      )}
    </div>
  );
}

function progressText({ flow, status, busy }: PanelUpdateSnapshot): string {
  switch (flow.phase) {
    case "job": {
      const job = !flow.jobId || status?.job?.id === flow.jobId ? status?.job : undefined;
      return `正在更新到 ${flow.targetVersion}：${updateStageLabel(job?.stage ?? "queued")}`;
    }
    case "restarting":
      return `面板正在重启，切换到 ${flow.target.targetVersion}`;
    case "confirming":
      return `${flow.target.targetVersion} 试运行中，正在确认运行稳定`;
    case "reloading":
      return "正在刷新页面";
    default:
      if (busy === "apply" || busy === "retry") return "正在提交更新请求";
      if (busy === "rollback") return "正在提交切换请求";
      if (status?.runtime.mode === "trial")
        return `${status.runtime.trialVersion || "新版本"} 试运行中`;
      if (status?.job)
        return `正在更新到 ${status.job.targetVersion}：${updateStageLabel(status.job.stage)}`;
      return "更新进行中";
  }
}

/** 分区内的进度摘要；完整的阶段、进度条与结果统一在更新弹窗中展示。 */
export function UpdateProgressSummary({ onOpen }: { onOpen: () => void }) {
  const snapshot = usePanelUpdate();
  return (
    <div className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-hairline bg-canvas-soft p-4">
      <div role="status" className="flex min-w-0 flex-wrap items-center gap-2 text-sm text-body">
        <StatusPill tone="warning" label="更新中" icon={RefreshCw} />
        <span>{progressText(snapshot)}</span>
      </div>
      <Button type="button" variant="outline" onClick={onOpen}>
        查看进度
      </Button>
    </div>
  );
}
