import { ExternalLink, History, RefreshCcw, Undo2 } from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";
import { CodeBlock } from "@/components/patterns/code-block";
import { EmptyState, ErrorState, LoadingRows } from "@/components/patterns/feedback";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { formatDate } from "@/lib/format";
import type { PanelRollbackTarget } from "@/lib/types";
import {
  displayVersion,
  MANUAL_UPDATE_COMMAND,
  runModeLabel,
  updateStrategyLabel,
} from "@/lib/update";
import {
  isPanelUpdateBusy,
  panelUpdateStore,
  usePanelUpdate,
  type PanelUpdateSnapshot,
} from "../update-store";
import { ROLLBACK_IMPACT, useUpdateActions } from "../use-update-actions";
import {
  RelativeTime,
  UpdateActionButtons,
  UpdateEligibility,
  UpdateProgressSummary,
  UpdateResultDetails,
  UpdateResultPill,
} from "./update-parts";

function UpdateSection({
  title,
  description,
  action,
  children,
}: {
  title: string;
  description: ReactNode;
  action?: ReactNode;
  children: ReactNode;
}) {
  return (
    <Card>
      <CardHeader className="flex-row flex-wrap items-start justify-between gap-3 space-y-0">
        <div className="min-w-0 space-y-1">
          <CardTitle>{title}</CardTitle>
          <CardDescription>{description}</CardDescription>
        </div>
        {action}
      </CardHeader>
      <CardContent className="space-y-4">{children}</CardContent>
    </Card>
  );
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="min-w-0 space-y-1">
      <dt className="text-caption text-mute">{label}</dt>
      <dd className="break-words text-sm text-body">{children}</dd>
    </div>
  );
}

// 区块共用的数据状态：首次读取前为加载；读取失败且没有任何缓存时显示错误与重试。
interface BlockState {
  snapshot: PanelUpdateSnapshot;
  pending: boolean;
  failure: string | null;
  retry: () => void;
}

function CurrentRuntime({ snapshot: { status }, pending, failure, retry }: BlockState) {
  const result = status?.runtime.lastResult;
  return (
    <UpdateSection title="当前运行" description="面板进程实际运行的版本与最近一次切换结果">
      {pending ? (
        <LoadingRows rows={2} />
      ) : !status ? (
        <ErrorState error={failure} onRetry={retry} />
      ) : (
        <>
          <dl className="grid gap-4 sm:grid-cols-2">
            <Field label="运行版本">
              <span
                className="font-mono text-ink"
                title={status.current.commit ? `提交 ${status.current.commit}` : undefined}
              >
                {displayVersion(status.current.version, status.current.commit)}
              </span>
            </Field>
            <Field label="镜像内置版本">
              <span className="font-mono">{status.runtime.builtinVersion || "—"}</span>
            </Field>
            <Field label="运行方式">
              {runModeLabel(status.runtime.mode)}
              {status.runtime.mode === "trial" && status.runtime.trialVersion && (
                <span className="font-mono">（{status.runtime.trialVersion}）</span>
              )}
            </Field>
            <Field label="上次结果">
              {result ? (
                <span className="flex flex-wrap items-center gap-2">
                  <UpdateResultPill result={result} />
                  <span className="font-mono">{result.version}</span>
                  <RelativeTime at={result.at} />
                </span>
              ) : (
                <span className="text-mute">暂无更新记录</span>
              )}
            </Field>
          </dl>
          {result && <UpdateResultDetails result={result} />}
        </>
      )}
    </UpdateSection>
  );
}

function AvailableUpdate({ snapshot, pending, failure, retry }: BlockState) {
  const { status, checking, checkError } = snapshot;
  const busy = isPanelUpdateBusy(snapshot);
  const published = status?.publishedAt ? Date.parse(status.publishedAt) : NaN;
  // 手动检查绕过后端缓存；侧栏的定时检查仍使用缓存，避免触发 GitHub 限流。
  const check = () => void panelUpdateStore.check({ force: true });
  return (
    <UpdateSection
      title="可用更新"
      description={
        status?.checkedAt ? (
          <>
            上次检查 <RelativeTime at={status.checkedAt} />
          </>
        ) : (
          "从 GitHub Releases 检查稳定版"
        )
      }
      action={
        <Button
          type="button"
          variant="outline"
          loading={checking}
          disabled={status ? !status.enabled || busy : pending}
          onClick={check}
        >
          <RefreshCcw aria-hidden="true" />
          检查更新
        </Button>
      }
    >
      {pending ? (
        <LoadingRows rows={2} />
      ) : !status ? (
        <ErrorState error={failure} onRetry={retry} />
      ) : (
        <>
          {checkError && <ErrorState error={checkError} onRetry={check} />}
          {busy ? (
            <UpdateProgressSummary onOpen={() => panelUpdateStore.openDialog()} />
          ) : !status.enabled ? (
            <EmptyState
              title="未启用版本检查"
              description="部署环境变量 UPDATE_CHECK_ENABLED 为 false，面板不会检查新版本"
            />
          ) : !status.updateAvailable ? (
            <EmptyState
              title="已是最新版本"
              description={`当前运行 ${displayVersion(status.current.version, status.current.commit)}`}
            />
          ) : (
            <>
              <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
                <span className="font-mono text-metric-md text-ink">{status.latestVersion}</span>
                {Number.isFinite(published) && (
                  <span className="text-caption text-mute">发布于 {formatDate(published)}</span>
                )}
                {status.releaseUrl && (
                  <a
                    href={status.releaseUrl}
                    target="_blank"
                    rel="noreferrer"
                    className="inline-flex items-center gap-1 text-sm text-primary-deep underline-offset-4 hover:underline max-md:min-h-10"
                  >
                    发布说明
                    <ExternalLink aria-hidden="true" className="size-3.5" />
                  </a>
                )}
              </div>
              <UpdateEligibility status={status} />
              <div className="flex flex-wrap gap-2">
                <UpdateActionButtons status={status} />
              </div>
            </>
          )}
        </>
      )}
    </UpdateSection>
  );
}

function RollbackOption({
  id,
  title,
  hint,
  label,
  icon,
  disabled,
  loading,
  onClick,
}: {
  id: string;
  title: string;
  hint: string;
  label: string;
  icon: ReactNode;
  disabled: boolean;
  loading: boolean;
  onClick: () => void;
}) {
  return (
    <div className="flex min-w-0 flex-col gap-3 rounded-md border border-hairline p-4">
      <div className="space-y-1">
        <p className="text-sm font-semibold text-ink">{title}</p>
        <p id={`${id}-hint`} className="text-caption text-mute">
          {hint}
        </p>
      </div>
      <Button
        type="button"
        variant="outline"
        className="self-start"
        aria-describedby={`${id}-hint`}
        loading={loading}
        disabled={disabled}
        onClick={onClick}
      >
        {icon}
        {label}
      </Button>
    </div>
  );
}

function RollbackRestore({ snapshot, pending, failure, retry }: BlockState) {
  const actions = useUpdateActions();
  const [requested, setRequested] = useState<PanelRollbackTarget | null>(null);
  const { status } = snapshot;
  const busy = isPanelUpdateBusy(snapshot) || snapshot.busy !== null;
  const run = (target: PanelRollbackTarget) => {
    setRequested(target);
    void actions.rollback(target).finally(() => setRequested(null));
  };
  const runtime = status?.runtime;
  const hintFor = (
    option: { available: boolean; reason?: string },
    ready: string,
    fallback: string,
  ) =>
    busy
      ? "更新或版本切换进行中，请等待完成"
      : option.available
        ? ready
        : option.reason || fallback;
  return (
    <UpdateSection title="回滚与恢复" description={`切换前需确认：${ROLLBACK_IMPACT}`}>
      {pending ? (
        <LoadingRows rows={2} />
      ) : !runtime ? (
        <ErrorState error={failure} onRetry={retry} />
      ) : runtime.mode === "unsupervised" ? (
        <EmptyState
          title="当前部署不支持界面回滚"
          description="镜像未启用软更新进程管理；如需回滚，请把 .env 中的 GO_PANEL_VERSION 改为旧版本标签后重建容器"
        />
      ) : (
        <div className="grid gap-4 md:grid-cols-2">
          <RollbackOption
            id="update-rollback-previous"
            title="回滚到上一版本"
            hint={hintFor(
              runtime.rollback,
              `切换回 ${runtime.previousVersion || "上一版本"}`,
              runtime.previousVersion ? "暂时无法回滚" : "没有可回滚的上一版本",
            )}
            label={runtime.previousVersion ? `回滚到 ${runtime.previousVersion}` : "回滚到上一版本"}
            icon={<Undo2 aria-hidden="true" />}
            disabled={busy || !runtime.rollback.available}
            loading={snapshot.busy === "rollback" && requested === "previous"}
            onClick={() => run("previous")}
          />
          <RollbackOption
            id="update-restore-builtin"
            title="恢复镜像内置版本"
            hint={hintFor(
              runtime.builtinRestore,
              `切换回镜像自带的 ${runtime.builtinVersion || "内置版本"}`,
              runtime.mode === "builtin" ? "当前已在运行镜像内置版本" : "暂时无法恢复内置版本",
            )}
            label="恢复镜像内置版本"
            icon={<History aria-hidden="true" />}
            disabled={busy || !runtime.builtinRestore.available}
            loading={snapshot.busy === "rollback" && requested === "builtin"}
            onClick={() => run("builtin")}
          />
        </div>
      )}
    </UpdateSection>
  );
}

function UpdateMethod({ snapshot: { status }, pending, failure, retry }: BlockState) {
  return (
    <UpdateSection
      title="更新方式说明"
      description="更新策略由部署环境变量 UPDATE_STRATEGY 决定（auto、soft、sidecar、manual），修改后需重建容器"
    >
      {pending ? (
        <LoadingRows rows={1} />
      ) : !status ? (
        <ErrorState error={failure} onRetry={retry} />
      ) : (
        <dl>
          <Field label="当前策略">{updateStrategyLabel(status.strategy)}</Field>
        </dl>
      )}
      <div className="space-y-2">
        <p className="text-sm font-semibold text-ink">手动更新镜像</p>
        <p className="text-caption text-mute">
          在部署目录执行；需要更新镜像的版本只能这样更新，新镜像会自动接管已有的软更新结果
        </p>
        <CodeBlock code={MANUAL_UPDATE_COMMAND} label="复制更新命令" />
      </div>
      {/* PANEL_USE_BUILTIN 默认 false；true 时 supervisor 忽略数据卷中的软更新版本、只运行镜像内置版本，
          不删除已下载版本。忘记改回会一直停留在旧版本；数据库若已被新版本做过不兼容迁移，内置版本会拒绝启动。 */}
      <div className="space-y-1">
        <p className="text-sm font-semibold text-ink">
          应急开关 <span className="font-mono">PANEL_USE_BUILTIN</span>
        </p>
        <p className="text-caption text-body">
          新版本异常且无法从界面回滚时，在 .env 中设置 PANEL_USE_BUILTIN=true 并执行 docker compose
          up -d，面板会忽略软更新版本、直接运行镜像内置版本，已下载的版本不会删除。排障后改回 false
          并重建容器即恢复原来的选择；需要长期使用内置版本时，请使用上方的“恢复镜像内置版本”
        </p>
        <p className="text-caption text-mute">
          若软更新版本已执行不兼容的数据库迁移，内置版本会拒绝启动，请按容器日志提示恢复更新前的数据库快照
        </p>
      </div>
      <p className="text-caption text-mute">
        软更新包从 GitHub Release 下载；已配置“节点与安全”中的 GitHub
        下载代理时经代理下载，完整性由内置公钥验证的发布签名保证
      </p>
    </UpdateSection>
  );
}

/** 系统配置“版本与更新”分区：读取状态不访问网络，“检查更新”才会请求 GitHub。 */
export function UpdatePanel() {
  const snapshot = usePanelUpdate();
  useEffect(() => {
    const controller = new AbortController();
    void panelUpdateStore.refresh({ signal: controller.signal });
    return () => controller.abort();
  }, []);
  const state: BlockState = {
    snapshot,
    pending: !snapshot.status && !snapshot.error,
    failure: snapshot.status ? null : snapshot.error,
    retry: () => void panelUpdateStore.refresh(),
  };
  return (
    <div className="space-y-6">
      <CurrentRuntime {...state} />
      <AvailableUpdate {...state} />
      <RollbackRestore {...state} />
      <UpdateMethod {...state} />
    </div>
  );
}
