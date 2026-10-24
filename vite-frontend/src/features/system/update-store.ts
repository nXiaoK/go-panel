import { useSyncExternalStore } from "react";
import {
  applyPanelUpdate,
  cancelPanelUpdate,
  checkPanelUpdate,
  getPanelUpdateStatus,
  getSystemVersion,
  rollbackPanel,
} from "@/lib/api";
import { errorMessage } from "@/lib/api/client";
import { notify } from "@/lib/notify";
import type {
  PanelRollbackTarget,
  PanelUpdateJob,
  PanelUpdateResult,
  PanelUpdateStatus,
  UpdateJobStage,
} from "@/lib/types";
import {
  canCancelUpdateJob,
  isUpdateJobActive,
  matchesUpdateResult,
  normalizePanelUpdateStatus,
  restartWaitLimitMs,
} from "@/lib/update";

// 作业进行中每 1.5 秒读取一次状态（后端不访问网络）；重启期间每 3 秒探测一次。
export const JOB_POLL_INTERVAL_MS = 1_500;
export const RESTART_POLL_INTERVAL_MS = 3_000;
// 单次轮询最多等待 5 秒，避免重启中的半开连接占满默认 30 秒期限、拖慢下一次探测。
const POLL_REQUEST_TIMEOUT_MS = 5_000;
// 作业创建后若连续 10 次（约 15 秒）读不到该作业，判定为已中断，不再无限等待。
const MISSING_JOB_POLL_LIMIT = 10;
// 进入以下阶段后进程随时可能为切换版本而退出；连续两次读取失败即按重启等待，避免错过 restarting。
const RESTART_PRONE_STAGES: readonly UpdateJobStage[] = [
  "backing_up",
  "migrate_check",
  "restarting",
];
// 刷新页面前写入当前标签页的 sessionStorage，刷新后据此展示本次结果；超过 15 分钟视为过期。
const RESULT_MARKER_KEY = "go-panel:update-result";
const RESULT_MARKER_TTL_MS = 15 * 60_000;

export type UpdateFlowKind = "upgrade" | "sidecar" | "rollback";

export interface RestartTarget {
  kind: UpdateFlowKind;
  action: PanelUpdateResult["action"];
  targetVersion: string;
  requestId?: string;
  // 服务端毫秒时间（作业开始时间），缺少 requestId 时用于排除更早的同版本结果。
  since?: number;
  // 浏览器毫秒时间：进入等待的时刻与最长等待截止时刻。
  startedAt: number;
  deadline: number;
  // 页面刷新后才发现的试运行：无法确认请求编号，只在离开 trial 后结束。
  resumed?: boolean;
}

export type UpdateFlow =
  | { phase: "idle" }
  | { phase: "job"; jobId: string; targetVersion: string }
  | { phase: "restarting"; target: RestartTarget }
  | { phase: "confirming"; target: RestartTarget }
  | { phase: "reloading"; target: RestartTarget }
  | { phase: "result"; result: PanelUpdateResult }
  | { phase: "timeout"; target: RestartTarget };

export interface PanelUpdateSnapshot {
  status: PanelUpdateStatus | null;
  // 读取 /system/update/status 的进度与错误。
  loading: boolean;
  error: string | null;
  // 读取 /system/update/check（可能访问 GitHub）的进度与错误。
  checking: boolean;
  checkError: string | null;
  // retry 表示带 force 重试已隔离的版本，按钮各自显示加载状态。
  busy: "apply" | "retry" | "cancel" | "rollback" | null;
  flow: UpdateFlow;
  dialogOpen: boolean;
}

type MarkerStorage = Pick<Storage, "getItem" | "setItem" | "removeItem">;
interface ResultMarker {
  targetVersion: string;
  requestId?: string;
  action: PanelUpdateResult["action"];
  savedAt: number;
}

const idle: UpdateFlow = { phase: "idle" };
const initialSnapshot = (): PanelUpdateSnapshot => ({
  status: null,
  loading: false,
  error: null,
  checking: false,
  checkError: null,
  busy: null,
  flow: idle,
  dialogOpen: false,
});

function sessionMarkerStorage(): MarkerStorage | null {
  try {
    return typeof window === "undefined" ? null : window.sessionStorage;
  } catch {
    return null;
  }
}

/** 管理员外壳内唯一的更新流程：侧栏徽标、更新弹窗与“版本与更新”分区共享同一份状态与轮询。 */
export function createPanelUpdateStore(
  options: { reload?: () => void; storage?: () => MarkerStorage | null } = {},
) {
  const reload = options.reload ?? (() => window.location.reload());
  const storage = options.storage ?? sessionMarkerStorage;
  let snapshot = initialSnapshot();
  const listeners = new Set<() => void>();
  let timer: ReturnType<typeof setTimeout> | undefined;
  // 轮询代次：切换阶段或重置时递增，过期的异步结果直接丢弃。
  let generation = 0;
  // 重置次数：退出登录后，仍在途的请求结果不能写回新会话。
  let resets = 0;
  let loop: "job" | "restart" | null = null;
  let jobFailures = 0;
  let missingJobPolls = 0;
  let lastJob: PanelUpdateJob | undefined;
  let markerPending = true;
  let statusRequests = 0;
  let checkRequests = 0;

  const publish = (patch: Partial<PanelUpdateSnapshot>) => {
    snapshot = { ...snapshot, ...patch };
    listeners.forEach((listener) => listener());
  };

  function stopPolling() {
    clearTimeout(timer);
    timer = undefined;
    loop = null;
    generation += 1;
  }
  function schedule(delay: number, tick: (token: number) => Promise<void>) {
    clearTimeout(timer);
    const token = generation;
    timer = setTimeout(() => void tick(token), delay);
  }

  function writeMarker(marker: ResultMarker) {
    try {
      storage()?.setItem(RESULT_MARKER_KEY, JSON.stringify(marker));
    } catch {
      // 存储不可用时刷新后不再弹出结果，“版本与更新”分区仍会显示上次结果。
    }
  }
  function takeMarker(): ResultMarker | null {
    try {
      const store = storage();
      const raw = store?.getItem(RESULT_MARKER_KEY);
      store?.removeItem(RESULT_MARKER_KEY);
      const value = raw ? (JSON.parse(raw) as Partial<ResultMarker>) : null;
      if (!value || typeof value.targetVersion !== "string" || typeof value.savedAt !== "number")
        return null;
      return {
        targetVersion: value.targetVersion,
        requestId: typeof value.requestId === "string" ? value.requestId : undefined,
        action:
          value.action === "rollback" || value.action === "use-builtin" ? value.action : "upgrade",
        savedAt: value.savedAt,
      };
    } catch {
      return null;
    }
  }

  // 页面因更新刷新后，用服务端的最新结果展示“已更新/已回滚”；仍在试运行时等待下一次状态。
  function consumeResultMarker(status: PanelUpdateStatus) {
    if (!markerPending || status.runtime.mode === "trial") return;
    markerPending = false;
    const marker = takeMarker();
    if (!marker || Math.abs(Date.now() - marker.savedAt) > RESULT_MARKER_TTL_MS) return;
    const result = status.runtime.lastResult;
    if (matchesUpdateResult(result, marker)) {
      publish({ flow: { phase: "result", result }, dialogOpen: true });
    } else if (status.current.version === marker.targetVersion) {
      // 侧车更新没有切换记录：运行版本已是目标版本即视为完成。
      publish({
        flow: {
          phase: "result",
          result: {
            requestId: marker.requestId ?? "",
            action: marker.action,
            version: marker.targetVersion,
            outcome: "committed",
            at: marker.savedAt,
          },
        },
        dialogOpen: true,
      });
    }
  }

  function receiveStatus(raw: PanelUpdateStatus): PanelUpdateStatus {
    const status = normalizePanelUpdateStatus(raw);
    publish({ status });
    consumeResultMarker(status);
    reconcile(status);
    return status;
  }

  // 根据服务端状态推进作业阶段；重启与确认阶段由各自的轮询负责，这里不干预。
  function reconcile(status: PanelUpdateStatus) {
    const { flow } = snapshot;
    const settled = flow.phase === "idle" || flow.phase === "result" || flow.phase === "timeout";
    if (!settled && flow.phase !== "job") return;
    const job = status.job;
    if (job && isUpdateJobActive(job)) {
      lastJob = job;
      missingJobPolls = 0;
      if (job.stage === "restarting") {
        enterWait("restarting", {
          kind: "upgrade",
          action: "upgrade",
          targetVersion: job.targetVersion,
          requestId: job.requestId,
          since: job.startedAt,
          readyTimeoutSec: job.readyTimeoutSec,
        });
        return;
      }
      if (flow.phase !== "job" || flow.jobId !== job.id)
        publish({ flow: { phase: "job", jobId: job.id, targetVersion: job.targetVersion } });
      startJobPolling();
      return;
    }
    if (flow.phase === "job") {
      // 只认本次作业；状态里可能仍是上一次已结束的作业。
      const ours = job && (!flow.jobId || job.id === flow.jobId) ? job : undefined;
      if (!ours && lastJob?.id !== flow.jobId && ++missingJobPolls < MISSING_JOB_POLL_LIMIT) return;
      stopPolling();
      publish({ flow: idle });
      // 弹窗打开时由弹窗展示失败原因；关闭时用通知提醒，避免结果被忽略。
      if (!snapshot.dialogOpen) {
        if (ours?.stage === "failed")
          notify.error(`面板更新失败：${ours.error || "请查看容器日志"}`);
        else if (!ours) notify.warning("更新作业已中断，请重新检查后再试");
      }
      return;
    }
    if (status.runtime.mode === "trial" && status.runtime.trialVersion) {
      // 试运行期间刷新了页面：继续等待结论，结束后刷新页面并展示结果。
      enterWait("confirming", {
        kind: "upgrade",
        action: "upgrade",
        targetVersion: status.runtime.trialVersion,
        resumed: true,
      });
    }
  }

  function startJobPolling(delay = JOB_POLL_INTERVAL_MS) {
    if (loop === "job") return;
    stopPolling();
    loop = "job";
    jobFailures = 0;
    schedule(delay, jobTick);
  }

  async function jobTick(token: number) {
    let raw: PanelUpdateStatus | null = null;
    try {
      raw = await getPanelUpdateStatus({ timeoutMs: POLL_REQUEST_TIMEOUT_MS });
    } catch {
      raw = null;
    }
    if (token !== generation) return;
    if (raw) {
      jobFailures = 0;
      receiveStatus(raw);
    } else if (++jobFailures >= 2 && lastJob && RESTART_PRONE_STAGES.includes(lastJob.stage)) {
      enterWait("restarting", {
        kind: "upgrade",
        action: "upgrade",
        targetVersion: lastJob.targetVersion,
        requestId: lastJob.requestId,
        since: lastJob.startedAt,
        readyTimeoutSec: lastJob.readyTimeoutSec,
      });
      return;
    }
    if (token === generation && loop === "job") schedule(JOB_POLL_INTERVAL_MS, jobTick);
  }

  function enterWait(
    phase: "restarting" | "confirming",
    input: Omit<RestartTarget, "startedAt" | "deadline"> & { readyTimeoutSec?: number },
  ) {
    const { readyTimeoutSec, ...rest } = input;
    const startedAt = Date.now();
    const target: RestartTarget = {
      ...rest,
      startedAt,
      deadline: startedAt + restartWaitLimitMs(readyTimeoutSec),
    };
    stopPolling();
    loop = "restart";
    publish({ flow: { phase, target } });
    schedule(RESTART_POLL_INTERVAL_MS, restartTick);
  }

  async function readStatus(token: number): Promise<PanelUpdateStatus | null> {
    try {
      const raw = await getPanelUpdateStatus({ timeoutMs: POLL_REQUEST_TIMEOUT_MS });
      return token === generation ? receiveStatus(raw) : null;
    } catch {
      return null;
    }
  }

  // 有请求编号或作业开始时间时才能在新版本就绪前认定结果，否则只能等试运行结束。
  const canMatch = (target: RestartTarget) =>
    Boolean(target.requestId) || target.since !== undefined;

  // README §4.9：先等 /system/version 变为目标版本，再等试运行结束；旧版本带回滚结果恢复时直接展示。
  async function restartTick(token: number) {
    const flow = snapshot.flow;
    if (flow.phase !== "restarting" && flow.phase !== "confirming") return;
    const { target } = flow;
    if (Date.now() >= target.deadline) return finish({ phase: "timeout", target });
    if (flow.phase === "restarting") {
      // 重启期间请求失败属于预期，静默等待下一次探测。
      const build = await getSystemVersion({ timeoutMs: POLL_REQUEST_TIMEOUT_MS }).catch(
        () => null,
      );
      if (token !== generation) return;
      if (build?.version === target.targetVersion) {
        // 侧车替换镜像没有试运行阶段，版本一致即可刷新。
        if (target.kind === "sidecar") return reloadForResult(target);
        publish({ flow: { phase: "confirming", target } });
      } else if (build && target.kind !== "sidecar" && canMatch(target)) {
        const result = (await readStatus(token))?.runtime.lastResult;
        if (token !== generation) return;
        if (matchesUpdateResult(result, target) && result.outcome !== "committed")
          return finish({ phase: "result", result });
      }
    }
    if (snapshot.flow.phase === "confirming") {
      const status = await readStatus(token);
      if (token !== generation) return;
      const result = status?.runtime.lastResult;
      if (status && canMatch(target) && matchesUpdateResult(result, target)) {
        // 当前页面仍是旧版前端：回滚后版本一致无需刷新，直接展示结果。
        if (result.outcome !== "committed" && !target.resumed)
          return finish({ phase: "result", result });
        return reloadForResult(target);
      }
      if (status && status.runtime.mode !== "trial") return reloadForResult(target);
    }
    schedule(RESTART_POLL_INTERVAL_MS, restartTick);
  }

  function finish(flow: UpdateFlow) {
    stopPolling();
    publish({ flow, dialogOpen: true });
  }

  function reloadForResult(target: RestartTarget) {
    stopPolling();
    writeMarker({
      targetVersion: target.targetVersion,
      requestId: target.requestId,
      action: target.action,
      savedAt: Date.now(),
    });
    publish({ flow: { phase: "reloading", target } });
    // 新版本内嵌的前端资源哈希已变化，必须整页刷新才能加载。
    reload();
  }

  async function refresh(request: { signal?: AbortSignal } = {}) {
    const epoch = resets;
    statusRequests += 1;
    publish({ loading: true });
    try {
      const raw = await getPanelUpdateStatus({ signal: request.signal });
      if (epoch !== resets) return null;
      const status = receiveStatus(raw);
      publish({ error: null });
      return status;
    } catch (error) {
      if (epoch === resets && !request.signal?.aborted) publish({ error: errorMessage(error) });
      return null;
    } finally {
      if (epoch === resets) {
        statusRequests -= 1;
        publish({ loading: statusRequests > 0 });
      }
    }
  }

  async function check(request: { signal?: AbortSignal } = {}) {
    const epoch = resets;
    checkRequests += 1;
    publish({ checking: true });
    try {
      const raw = await checkPanelUpdate({ signal: request.signal });
      if (epoch !== resets) return null;
      const status = receiveStatus(raw);
      publish({ checkError: null, error: null });
      return status;
    } catch (error) {
      if (epoch === resets && !request.signal?.aborted)
        publish({ checkError: errorMessage(error) });
      return null;
    } finally {
      if (epoch === resets) {
        checkRequests -= 1;
        publish({ checking: checkRequests > 0 });
      }
    }
  }

  async function apply(force = false) {
    const targetVersion = snapshot.status?.latestVersion;
    if (!targetVersion || snapshot.busy || isBusy()) return;
    const epoch = resets;
    publish({ busy: force ? "retry" : "apply" });
    try {
      const result = await notify.promise(
        async () => {
          const response = await applyPanelUpdate(targetVersion, force);
          if (!response.started)
            throw new Error(
              response.strategy === "soft"
                ? "更新作业未能启动，请稍后重试"
                : "更新请求失败，请检查更新侧车日志",
            );
          return response;
        },
        {
          loading: "正在准备面板更新…",
          success: (response) =>
            response.strategy === "soft"
              ? `已开始更新到 ${response.targetVersion}`
              : `已备份数据库并开始更新到 ${response.targetVersion}`,
          error: errorMessage,
        },
      );
      if (epoch !== resets) return;
      publish({ dialogOpen: true });
      if (result.strategy === "soft") {
        lastJob = undefined;
        missingJobPolls = 0;
        publish({
          flow: { phase: "job", jobId: result.jobId ?? "", targetVersion: result.targetVersion },
        });
        // 立即读取一次阶段，随后按 1.5 秒间隔轮询。
        startJobPolling(0);
      } else {
        // 旧版后端不返回 strategy，只支持侧车：沿用“等待版本变化后刷新”的流程。
        enterWait("restarting", {
          kind: "sidecar",
          action: "upgrade",
          targetVersion: result.targetVersion,
        });
      }
    } catch {
      // notify.promise 已展示失败原因；刷新状态以反映版本变化或隔离记录，保留弹窗允许重试。
      if (epoch === resets) void refresh();
    } finally {
      if (epoch === resets) publish({ busy: null });
    }
  }

  async function cancel() {
    const job = snapshot.status?.job;
    if (!job || !canCancelUpdateJob(job) || snapshot.busy) return;
    const epoch = resets;
    publish({ busy: "cancel" });
    try {
      await cancelPanelUpdate(job.id);
      notify.success("已取消面板更新");
      if (epoch === resets) await refresh();
    } catch (error) {
      notify.error(errorMessage(error));
    } finally {
      if (epoch === resets) publish({ busy: null });
    }
  }

  async function rollback(target: PanelRollbackTarget) {
    if (snapshot.busy || isBusy()) return;
    const epoch = resets;
    publish({ busy: "rollback" });
    try {
      const result = await rollbackPanel(target);
      if (!result.started) throw new Error("切换请求未被接受，请稍后重试");
      if (epoch !== resets) return;
      publish({ dialogOpen: true });
      enterWait("restarting", {
        kind: "rollback",
        action: target === "builtin" ? "use-builtin" : "rollback",
        targetVersion: result.targetVersion,
        requestId: result.requestId,
      });
    } catch (error) {
      notify.error(errorMessage(error));
    } finally {
      if (epoch === resets) publish({ busy: null });
    }
  }

  function isBusy() {
    return isPanelUpdateBusy(snapshot);
  }

  return {
    subscribe(listener: () => void) {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    getSnapshot: () => snapshot,
    receiveStatus,
    refresh,
    check,
    apply,
    cancel,
    rollback,
    openDialog() {
      if (snapshot.dialogOpen) return;
      // 已读的结果在下次打开时清除，避免关闭动画期间内容跳变。
      const settled = snapshot.flow.phase === "result" || snapshot.flow.phase === "timeout";
      publish(settled ? { dialogOpen: true, flow: idle } : { dialogOpen: true });
    },
    closeDialog() {
      publish({ dialogOpen: false });
    },
    reloadPage() {
      reload();
    },
    /** 退出管理员外壳（退出登录、强制改密）时停止全部轮询并清空状态。 */
    reset() {
      stopPolling();
      resets += 1;
      jobFailures = 0;
      missingJobPolls = 0;
      lastJob = undefined;
      markerPending = true;
      statusRequests = 0;
      checkRequests = 0;
      snapshot = initialSnapshot();
      listeners.forEach((listener) => listener());
    },
  };
}

export type PanelUpdateStore = ReturnType<typeof createPanelUpdateStore>;

/** 作业、重启、试运行或请求提交中都视为“更新中”，此时不再提供新的更新或回滚入口。 */
export function isPanelUpdateBusy(snapshot: PanelUpdateSnapshot): boolean {
  const { phase } = snapshot.flow;
  return (
    snapshot.busy === "apply" ||
    snapshot.busy === "retry" ||
    snapshot.busy === "rollback" ||
    phase === "job" ||
    phase === "restarting" ||
    phase === "confirming" ||
    phase === "reloading" ||
    isUpdateJobActive(snapshot.status?.job) ||
    snapshot.status?.runtime.mode === "trial"
  );
}

export const panelUpdateStore = createPanelUpdateStore();

export function usePanelUpdate(): PanelUpdateSnapshot {
  return useSyncExternalStore(
    panelUpdateStore.subscribe,
    panelUpdateStore.getSnapshot,
    panelUpdateStore.getSnapshot,
  );
}
