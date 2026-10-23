import { apiPost, type RequestOptions } from "./client";
import type {
  SystemStatus,
  BuildInfo,
  PanelUpdateStatus,
  PanelUpdateTriggerResult,
} from "@/lib/types";
export const getSystemStatus = (opts?: RequestOptions): Promise<SystemStatus> =>
  apiPost("/system/status", {}, opts);
export const getSystemVersion = (opts?: RequestOptions): Promise<BuildInfo> =>
  apiPost("/system/version", {}, opts);
// 升级检查只等待 15 秒；触发升级沿用普通 30 秒，避免占住交互页面。
export const checkPanelUpdate = (opts?: RequestOptions): Promise<PanelUpdateStatus> =>
  apiPost("/system/update/check", {}, { ...opts, timeoutMs: 15_000 });
export const applyPanelUpdate = (opts?: RequestOptions): Promise<PanelUpdateTriggerResult> =>
  apiPost("/system/update/apply", {}, opts);
