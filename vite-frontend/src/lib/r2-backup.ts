import type { R2BackupSettings, R2BackupSettingsUpdate } from "@/lib/types";

export interface R2BackupForm {
  enabled: boolean;
  accountId: string;
  accessKeyId: string;
  bucket: string;
  objectPrefix: string;
  scheduleTime: string;
  retentionCount: string;
}

// 默认关闭自动上传；其余默认值与后端保持一致，计划时间使用服务器本地时区。
export const defaultR2BackupForm: R2BackupForm = {
  enabled: false,
  accountId: "",
  accessKeyId: "",
  bucket: "",
  objectPrefix: "flux-panel/backups",
  scheduleTime: "03:00",
  retentionCount: "30",
};

export function r2BackupSettingsToForm(settings: R2BackupSettings): R2BackupForm {
  return {
    enabled: settings.enabled,
    accountId: settings.accountId,
    accessKeyId: settings.accessKeyId,
    bucket: settings.bucket,
    objectPrefix: settings.objectPrefix,
    scheduleTime: settings.scheduleTime,
    retentionCount: String(settings.retentionCount),
  };
}

export function isR2BackupFormDirty(
  settings: R2BackupSettings | null,
  form: R2BackupForm,
  secretAccessKey: string,
  clearSecret: boolean,
): boolean {
  if (!settings) return false;
  return (
    JSON.stringify(form) !== JSON.stringify(r2BackupSettingsToForm(settings)) ||
    secretAccessKey !== "" ||
    clearSecret
  );
}

// 进入“清除密钥”状态时必须丢弃尚未保存的新密钥，避免界面提示清除但请求实际轮换密钥。
export function toggleR2BackupSecretClear(
  currentClearSecret: boolean,
  secretAccessKey: string,
): { clearSecret: boolean; secretAccessKey: string } {
  const clearSecret = !currentClearSecret;
  return {
    clearSecret,
    secretAccessKey: clearSecret ? "" : secretAccessKey,
  };
}

// 前端只做即时可用性检查，后端仍会执行完整格式、权限和加密校验。
export function buildR2BackupUpdate(
  form: R2BackupForm,
  secretAccessKey: string,
  clearSecret: boolean,
): { value?: R2BackupSettingsUpdate; error?: string } {
  if (!/^\d+$/.test(form.retentionCount)) {
    return { error: "备份保留数量必须是整数" };
  }
  const retentionCount = Number(form.retentionCount);
  if (!Number.isSafeInteger(retentionCount)) {
    return { error: "备份保留数量必须是整数" };
  }
  if (clearSecret && secretAccessKey !== "") {
    return { error: "不能同时填写新密钥并清除密钥" };
  }
  if (clearSecret && form.enabled) {
    return { error: "清除密钥前请先关闭自动备份" };
  }
  return {
    value: {
      ...form,
      retentionCount,
      secretAccessKey,
      clearSecret,
    },
  };
}

export function formatR2BackupTimestamp(value: number): string {
  if (!value) return "尚无记录";
  return new Date(value).toLocaleString();
}

export function formatR2BackupBytes(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB"];
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  return `${(value / 1024 ** index).toFixed(index === 0 ? 0 : 2)} ${units[index]}`;
}
