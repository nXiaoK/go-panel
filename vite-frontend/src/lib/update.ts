import { formatBytes } from "@/lib/format";
import type { StatusTone } from "@/lib/status";
import type {
  PanelRunMode,
  PanelRuntimeInfo,
  PanelUpdateJob,
  PanelUpdateResult,
  PanelUpdateStatus,
  SoftUpdateBlockReason,
  UpdateJobStage,
  UpdateStrategy,
} from "@/lib/types";

const DISMISSED_UPDATE_KEY = "dismissed_update_version";

type UpdateStorage = Pick<Storage, "getItem" | "setItem">;

function browserStorage(): UpdateStorage | null {
  return typeof window === "undefined" ? null : window.localStorage;
}

export function displayVersion(version?: string, commit?: string): string {
  const normalized = String(version || "dev").trim() || "dev";
  if (normalized !== "dev") return normalized;
  const shortCommit = String(commit || "")
    .trim()
    .slice(0, 7);
  return shortCommit && shortCommit !== "unknown" ? `dev-${shortCommit}` : "dev";
}

export function shouldPromptForUpdate(
  latestVersion: string | undefined,
  storage: UpdateStorage | null = browserStorage(),
): boolean {
  const latest = String(latestVersion || "").trim();
  if (!latest) return false;
  return !storage || storage.getItem(DISMISSED_UPDATE_KEY) !== latest;
}

export function dismissUpdate(
  latestVersion: string | undefined,
  storage: UpdateStorage | null = browserStorage(),
): void {
  const latest = String(latestVersion || "").trim();
  if (latest && storage) storage.setItem(DISMISSED_UPDATE_KEY, latest);
}

// 手动更新镜像：需在 compose.yml 所在的部署目录执行，会拉取镜像并重建容器。
export const MANUAL_UPDATE_COMMAND = "docker compose pull && docker compose up -d";
// Compose 服务名为 panel（见根目录 compose.yml）；等待超时后引导管理员查看最近日志。
export const PANEL_LOGS_COMMAND = "docker compose logs --tail 200 panel";

function ownLabel<T extends string>(labels: Record<T, string>, key: unknown): string | undefined {
  // 只读取自有键，避免 "toString" 等原型属性被当成文案。
  return typeof key === "string" && Object.hasOwn(labels, key) ? labels[key as T] : undefined;
}

const stageLabels: Record<UpdateJobStage, string> = {
  queued: "等待开始",
  downloading: "下载更新包",
  extracting: "校验并解包",
  preflight: "检查新版本",
  backing_up: "备份数据库",
  migrate_check: "演练数据迁移",
  restarting: "正在重启",
  failed: "失败",
  canceled: "已取消",
};

export function updateStageLabel(stage: UpdateJobStage | string | undefined): string {
  return ownLabel(stageLabels, stage) ?? "未知阶段";
}

// 阶段列表按后端作业顺序排列；queued 只作为“等待开始”提示，不占步骤。
export const UPDATE_PROGRESS_STAGES = [
  "downloading",
  "extracting",
  "preflight",
  "backing_up",
  "migrate_check",
  "restarting",
] as const satisfies readonly UpdateJobStage[];

export type UpdateStageState = "done" | "current" | "pending";

export function updateStageStates(
  stage: UpdateJobStage,
): { stage: UpdateJobStage; label: string; state: UpdateStageState }[] {
  const index = (UPDATE_PROGRESS_STAGES as readonly UpdateJobStage[]).indexOf(stage);
  return UPDATE_PROGRESS_STAGES.map((item, position) => ({
    stage: item,
    label: stageLabels[item],
    state: index < 0 || position > index ? "pending" : position === index ? "current" : "done",
  }));
}

// 失败或已取消的作业仍可能留在状态里，只用于展示原因；不能用类型谓词，否则假分支会被误判为没有作业。
export function isUpdateJobActive(job: PanelUpdateJob | null | undefined): boolean {
  return job != null && job.stage !== "failed" && job.stage !== "canceled";
}

const cancelableStages: readonly UpdateJobStage[] = [
  "queued",
  "downloading",
  "extracting",
  "preflight",
];

// 进入“备份数据库”后已开始写快照并准备切换，后端不再接受取消。
export function canCancelUpdateJob(job: PanelUpdateJob | null | undefined): boolean {
  return job != null && cancelableStages.includes(job.stage);
}

// 每个原因码拆成“说明”和“建议”，detail 插在两者之间，保证原因始终先于操作建议。
const reasonTexts: Record<SoftUpdateBlockReason, [summary: string, advice: string]> = {
  disabled: ["已按部署配置 UPDATE_STRATEGY 关闭软更新", "请通过更新侧车或手动更新镜像"],
  not_supervised: ["当前镜像不支持软更新", "请先手动更新一次镜像"],
  development_build: ["开发构建不支持软更新", "请改用正式版本镜像"],
  runtime_dir_unwritable: ["运行时目录不可写，无法保存新版本", "请检查数据卷权限后重试"],
  no_update: ["当前已是最新稳定版", "无需更新"],
  check_failed: ["检查更新失败", "请稍后重试，或确认服务器可以访问 GitHub"],
  manifest_missing: ["该版本没有提供软更新清单", "请更新镜像"],
  signature_invalid: ["更新清单签名校验未通过", "已拒绝软更新，请改为更新镜像"],
  manifest_invalid: ["更新清单无效或与发布版本不一致", "已拒绝软更新，请改为更新镜像"],
  arch_unavailable: ["该版本没有当前 CPU 架构的更新包", "请更新镜像"],
  requires_image_update: ["该版本需要更新镜像", "请在部署目录执行下方命令"],
  upgrade_path_unsupported: ["当前版本过旧，不能直接软更新到该版本", "请先更新镜像"],
  quarantined: ["该版本曾试运行失败", "已暂停软更新，确认问题已解决后可以重试"],
  job_running: ["已有更新作业正在进行", "请等待完成"],
};

export function softUpdateReasonText(
  reason: SoftUpdateBlockReason | string | undefined,
  detail?: string,
): string {
  const [summary, advice] =
    typeof reason === "string" && Object.hasOwn(reasonTexts, reason)
      ? reasonTexts[reason as SoftUpdateBlockReason]
      : ["暂时无法软更新", "请更新镜像"];
  const extra = detail?.trim();
  return `${summary}${extra ? `（${extra}）` : ""}，${advice}`;
}

export type PrimaryUpdateAction = "soft" | "sidecar" | "manual" | "none";

// 每个区域只显示一个主操作：soft 创建软更新作业，sidecar 沿用更新侧车，manual 只给出命令。
export function primaryUpdateAction(
  status: PanelUpdateStatus | null | undefined,
): PrimaryUpdateAction {
  if (!status?.updateAvailable || !status.latestVersion) return "none";
  if (
    isUpdateJobActive(status.job) ||
    status.softUpdate.reason === "job_running" ||
    status.runtime.mode === "trial"
  )
    return "none";
  if (status.strategy === "soft" && status.softUpdate.available) return "soft";
  if (status.strategy === "sidecar") return "sidecar";
  return "manual";
}

const runModeLabels: Record<PanelRunMode, string> = {
  unsupervised: "直接运行（不支持软更新）",
  builtin: "镜像内置版本",
  release: "软更新版本",
  trial: "试运行中",
};

export function runModeLabel(mode: PanelRunMode | string | undefined): string {
  return ownLabel(runModeLabels, mode) ?? "未知";
}

const strategyLabels: Record<UpdateStrategy, string> = {
  soft: "软更新（无需重建容器）",
  sidecar: "更新侧车（拉取镜像并重建容器）",
  manual: "手动更新镜像",
};

export function updateStrategyLabel(strategy: UpdateStrategy | string | undefined): string {
  return ownLabel(strategyLabels, strategy) ?? "未知";
}

export function resultTone(outcome: PanelUpdateResult["outcome"]): StatusTone {
  return outcome === "committed" ? "good" : outcome === "rolled_back" ? "critical" : "warning";
}

export function resultLabel(result: Pick<PanelUpdateResult, "outcome" | "action">): string {
  if (result.outcome === "rolled_back") return "已自动回滚";
  if (result.outcome === "rejected") return "已拒绝切换";
  return result.action === "rollback"
    ? "已回滚"
    : result.action === "use-builtin"
      ? "已恢复内置版本"
      : "已更新";
}

const resultStageLabels: Record<string, string> = {
  verify: "启动复验",
  start: "启动",
  ready_timeout: "就绪超时",
  stability: "稳定期检查",
  healthz: "健康检查",
  schema: "数据库兼容性检查",
};

// 未知阶段不回显英文原文，界面直接省略。
export function resultStageLabel(stage: string | undefined): string | undefined {
  return ownLabel(resultStageLabels, stage);
}

// 新版本试运行：就绪超时默认 300 秒，再加 60 秒稳定期；后端未给出时按 360 秒等待。
export const DEFAULT_READY_TIMEOUT_SEC = 300;
export const STABILITY_WINDOW_SEC = 60;

export function restartWaitLimitMs(readyTimeoutSec?: number): number {
  const ready =
    typeof readyTimeoutSec === "number" && Number.isFinite(readyTimeoutSec) && readyTimeoutSec > 0
      ? readyTimeoutSec
      : DEFAULT_READY_TIMEOUT_SEC;
  return (ready + STABILITY_WINDOW_SEC) * 1000;
}

export interface UpdateResultTarget {
  requestId?: string;
  targetVersion: string;
  // 服务端毫秒时间；只接受此后写入的结果，避免把上一次同版本的结果当成本次结果。
  since?: number;
}

export function matchesUpdateResult(
  result: PanelUpdateResult | null | undefined,
  target: UpdateResultTarget,
): result is PanelUpdateResult {
  if (!result) return false;
  if (target.requestId) return result.requestId === target.requestId;
  if (result.version !== target.targetVersion) return false;
  return target.since === undefined || result.at >= target.since;
}

export function downloadPercent(done?: number, total?: number): number | null {
  if (!total || !Number.isFinite(total) || total <= 0) return null;
  const value = Number.isFinite(done) ? Number(done) : 0;
  return Math.round(Math.min(1, Math.max(0, value / total)) * 100);
}

export function formatDownloadProgress(done?: number, total?: number): string {
  return total && total > 0
    ? `${formatBytes(done ?? 0)} / ${formatBytes(total)}`
    : `已下载 ${formatBytes(done ?? 0)}`;
}

type RawPanelUpdateStatus = Partial<Omit<PanelUpdateStatus, "softUpdate" | "runtime">> & {
  softUpdate?: Partial<PanelUpdateStatus["softUpdate"]>;
  runtime?: Partial<PanelRuntimeInfo>;
};

// 旧版后端与测试桩可能缺少 strategy/softUpdate/runtime；入口处补齐，界面可按契约直接读取。
export function normalizePanelUpdateStatus(raw: RawPanelUpdateStatus): PanelUpdateStatus {
  const runtime = raw.runtime ?? {};
  return {
    ...raw,
    current: raw.current ?? { version: "dev", commit: "", buildTime: "", sourceUrl: "" },
    enabled: Boolean(raw.enabled),
    updateAvailable: Boolean(raw.updateAvailable),
    autoUpdateConfigured: Boolean(raw.autoUpdateConfigured),
    // 旧版后端只有 autoUpdateConfigured：已配置侧车即按 sidecar 处理，否则只能手动更新。
    strategy: raw.strategy ?? (raw.autoUpdateConfigured ? "sidecar" : "manual"),
    softUpdate: { ...raw.softUpdate, available: Boolean(raw.softUpdate?.available) },
    runtime: {
      ...runtime,
      mode: runtime.mode ?? "unsupervised",
      // 未知标签按 latest 处理，只会少显示固定标签提示，不会误报。
      imageTag: runtime.imageTag || "latest",
      rollback: runtime.rollback ?? { available: false },
      builtinRestore: runtime.builtinRestore ?? { available: false },
    },
  };
}
