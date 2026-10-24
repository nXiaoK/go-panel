import { apiPost, type RequestOptions } from "@/lib/api/client";
import type {
  BuildInfo,
  PanelRollbackResult,
  PanelRollbackTarget,
  PanelUpdateStatus,
  PanelUpdateTriggerResult,
  SystemStatus,
} from "@/lib/types";
export const getSystemStatus = (opts?: RequestOptions): Promise<SystemStatus> =>
  apiPost("/system/status", {}, opts);
export const getSystemVersion = (opts?: RequestOptions): Promise<BuildInfo> =>
  apiPost("/system/version", {}, opts);
// 检查会访问 GitHub 并验签更新清单，后端最长约 20 秒；前端等待 25 秒，避免先于后端放弃。
export const checkPanelUpdate = (opts?: RequestOptions): Promise<PanelUpdateStatus> =>
  apiPost("/system/update/check", {}, { ...opts, timeoutMs: 25_000 });
// 只读取检查缓存、运行信息与作业进度，不访问网络；作业进行中由前端每 1.5 秒轮询。
export const getPanelUpdateStatus = (opts?: RequestOptions): Promise<PanelUpdateStatus> =>
  apiPost("/system/update/status", {}, opts);
// targetVersion 必须等于最近一次检查结果，否则后端拒绝；force 仅用于重试已隔离的失败版本。
// 软更新的同步预检约 25 秒内完成，沿用普通 30 秒期限；侧车策略仍由后端走原有流程。
export const applyPanelUpdate = (
  targetVersion: string,
  force = false,
  opts?: RequestOptions,
): Promise<PanelUpdateTriggerResult> =>
  apiPost("/system/update/apply", { targetVersion, force }, opts);
// 只能取消尚未进入“备份数据库”阶段的作业；之后的阶段由后端拒绝取消。
export const cancelPanelUpdate = (jobId: string, opts?: RequestOptions): Promise<null> =>
  apiPost("/system/update/cancel", { jobId }, opts);
// 回滚到上一版本或恢复镜像内置版本：后端写入切换请求后优雅重启，数据库保持当前内容。
export const rollbackPanel = (
  target: PanelRollbackTarget,
  opts?: RequestOptions,
): Promise<PanelRollbackResult> => apiPost("/system/update/rollback", { target }, opts);
