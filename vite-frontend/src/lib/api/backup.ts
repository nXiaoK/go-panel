import { apiDownload, apiUpload, apiPost, type RequestOptions } from "./client";
import type {
  R2BackupSettings,
  R2BackupSettingsUpdate,
  R2BackupRunResult,
  ExtraRulesDetection,
  ExtraRulesHandled,
  ExtraRuleAction,
} from "@/lib/types";
// 备份最大 256 MB，上传和下载都保留服务端 10 分钟传输窗口。
const backupTransferTimeoutMs = 10 * 60 * 1000;
export const downloadSiteBackup = (
  opts?: RequestOptions,
): Promise<{ blob: Blob; filename?: string }> =>
  apiDownload("/backup/download", { ...opts, timeoutMs: backupTransferTimeoutMs });
export const restoreSiteBackup = (
  file: File,
  opts?: RequestOptions,
): Promise<{ restoredAt: number; preRestoreBackup?: string }> => {
  const form = new FormData();
  form.append("file", file);
  return apiUpload("/backup/restore", form, { ...opts, timeoutMs: backupTransferTimeoutMs });
};
export const detectExtraRules = (opts?: RequestOptions): Promise<ExtraRulesDetection> =>
  apiPost("/backup/detect-extra-rules", {}, opts);
export const handleExtraRules = (
  rules: ExtraRuleAction[],
  opts?: RequestOptions,
): Promise<ExtraRulesHandled> => apiPost("/backup/handle-extra-rules", { rules }, opts);
export const getR2BackupSettings = (opts?: RequestOptions): Promise<R2BackupSettings> =>
  apiPost("/backup/r2/settings", {}, opts);
export const updateR2BackupSettings = (
  data: R2BackupSettingsUpdate,
  opts?: RequestOptions,
): Promise<R2BackupSettings> => apiPost("/backup/r2/update", data, opts);
export const testR2BackupConnection = (opts?: RequestOptions): Promise<string> =>
  apiPost("/backup/r2/test", {}, opts);
// 立即备份涉及对象存储上传，单独等待 60 秒，避免改变其他请求默认期限。
export const runR2BackupNow = (opts?: RequestOptions): Promise<R2BackupRunResult> =>
  apiPost("/backup/r2/run", {}, { ...opts, timeoutMs: 60_000 });
