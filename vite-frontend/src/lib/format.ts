const BYTE_UNITS = ["B", "KB", "MB", "GB", "TB"] as const;
function safeNumber(value: number): number {
  return Number.isFinite(value) && value > 0 ? value : 0;
}
function decimals(value: number) {
  return value < 10 ? 2 : value < 100 ? 1 : 0;
}

export function bytesToGb(bytes: number): number {
  return safeNumber(bytes) / 1024 ** 3;
}
export function formatByteParts(bytes: number, options: { precision?: number } = {}) {
  const safe = safeNumber(bytes);
  const index =
    safe > 0
      ? Math.max(0, Math.min(Math.floor(Math.log(safe) / Math.log(1024)), BYTE_UNITS.length - 1))
      : 0;
  const value = safe / 1024 ** index;
  const precision =
    options.precision === undefined || !Number.isFinite(options.precision)
      ? safe === 0
        ? 0
        : decimals(value)
      : Math.max(0, Math.min(20, Math.trunc(options.precision)));
  return { value: value.toFixed(precision), suffix: BYTE_UNITS[index] };
}
export function formatBytes(bytes: number = 0, options: { precision?: number } = {}): string {
  const { value, suffix } = formatByteParts(bytes, options);
  return `${value} ${suffix}`;
}
export function formatRate(bytesPerSec: number): string {
  return `${formatBytes(bytesPerSec)}/s`;
}

export type FormattedSpeedRate = { value: string; unit: "MB/s" | "GB/s" };
/** 沿用测速界面的 Mbps → MB/s 转换与两位小数，保持已有 iperf 展示口径。 */
export function formatMbps(mbps: unknown): FormattedSpeedRate {
  const mbPerSecond = safeNumber(Number(mbps)) / 8;
  return mbPerSecond >= 1024
    ? { value: (mbPerSecond / 1024).toFixed(2), unit: "GB/s" }
    : { value: mbPerSecond.toFixed(2), unit: "MB/s" };
}
export function formatGbQuota(usedBytes: number, quotaGb: number): string {
  const used = bytesToGb(usedBytes);
  return `${used.toFixed(used === 0 ? 0 : decimals(used))} GB / ${safeNumber(quotaGb)} GB`;
}
export function formatDuration(seconds: number): string {
  const safe = Math.floor(safeNumber(seconds));
  const days = Math.floor(safe / 86400),
    hours = Math.floor((safe % 86400) / 3600),
    minutes = Math.floor((safe % 3600) / 60);
  if (days) return `${days}天${hours}小时`;
  if (hours) return `${hours}小时${minutes}分钟`;
  return `${minutes}分钟`;
}
function validDate(ms: number | null | undefined): Date | null {
  if (ms === null || ms === undefined || !Number.isFinite(ms)) return null;
  const date = new Date(ms);
  return Number.isNaN(date.getTime()) ? null : date;
}
export function formatDate(ms: number | null | undefined): string {
  const date = validDate(ms);
  return date
    ? new Intl.DateTimeFormat("zh-CN", { year: "numeric", month: "2-digit", day: "2-digit" })
        .format(date)
        .replaceAll("/", "-")
    : "—";
}
export function formatDateTime(ms: number | null | undefined): string {
  const date = validDate(ms);
  return date
    ? `${formatDate(ms)} ${new Intl.DateTimeFormat("zh-CN", { hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23" }).format(date)}`
    : "—";
}
export function formatRelative(ms: number | null | undefined, now = Date.now()): string {
  const date = validDate(ms);
  if (!date) return "—";
  const seconds = (date.getTime() - now) / 1000;
  const delta = Math.abs(seconds);
  if (delta < 60) return seconds > 0 ? "即将过期" : "刚刚";
  const [amount, unit] =
    delta < 3600
      ? [Math.floor(delta / 60), "分钟"]
      : delta < 86400
        ? [Math.floor(delta / 3600), "小时"]
        : [Math.floor(delta / 86400), "天"];
  return seconds > 0 ? `${amount} ${unit}后过期` : `${amount} ${unit}前`;
}
