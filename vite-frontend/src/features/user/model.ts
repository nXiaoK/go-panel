import type { User, UserTunnel } from "@/lib/types";

export const userSearchDefaults = {
  q: "",
  status: "all",
  expiry: "all",
  sort: "user",
  focus: undefined as number | undefined,
  create: undefined as number | undefined,
};
/** 用户列表只使用已有数据过滤；不为转发用量额外请求转发列表。 */
export function filterUsers<T extends { user?: string | null; name?: string | null }>(
  users: T[],
  query: string,
): T[] {
  const needle = query.trim().toLowerCase();
  return needle
    ? users.filter((u) => `${u.user ?? ""} ${u.name ?? ""}`.toLowerCase().includes(needle))
    : users;
}
export function userSearch(search: Record<string, unknown>) {
  const status = String(search.status ?? "all");
  const expiry = String(search.expiry ?? "all");
  return {
    ...search,
    status: ["all", "0", "1"].includes(status) ? status : "all",
    expiry: ["all", "soon", "expired"].includes(expiry) ? expiry : "all",
    sort: ["user", "user-desc", "flow", "flow-desc", "expiry", "expiry-desc"].includes(
      String(search.sort),
    )
      ? String(search.sort)
      : "user",
  };
}
export function toDateInput(timestamp?: number | null) {
  if (!timestamp) return "";
  const date = new Date(timestamp);
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")}`;
}
export function dateTimestamp(value: string) {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) return null;
  const date = new Date(`${value}T23:59:59`);
  return Number.isFinite(date.getTime()) && toDateInput(date.getTime()) === value
    ? date.getTime()
    : null;
}
/** 按日历月顺延，月底截到目标月份最后一天；已过期时从今天续期。 */
export function extendExpiry(value: string, months: number, now = new Date()) {
  const current = dateTimestamp(value);
  const base = current !== null && current > now.getTime() ? new Date(current) : new Date(now);
  const day = base.getDate();
  base.setDate(1);
  base.setMonth(base.getMonth() + months);
  const last = new Date(base.getFullYear(), base.getMonth() + 1, 0).getDate();
  base.setDate(Math.min(day, last));
  return toDateInput(base.getTime());
}
export function expiryState(timestamp?: number | null, now = Date.now()) {
  if (!timestamp) return { tone: "neutral" as const, label: "未设置" };
  if (timestamp <= now) return { tone: "critical" as const, label: "已到期" };
  const days = Math.ceil((timestamp - now) / 86400000);
  return days <= 7
    ? { tone: "warning" as const, label: `${days} 天后到期` }
    : { tone: "neutral" as const, label: "有效" };
}
export type QuotaValues = {
  flow: string;
  num: string;
  expiry: string;
  reset: string;
  speedId: string;
};
export type UserValues = QuotaValues & { user: string; pwd: string; status: boolean };
export type FieldErrors = Partial<Record<keyof UserValues | "tunnelId", string>>;
export function quotaValues(row?: User | UserTunnel): QuotaValues {
  return {
    flow: String(row?.flow ?? 100),
    num: String(row?.num ?? 10),
    expiry: toDateInput(row?.expTime),
    reset: String(row?.flowResetTime ?? 0),
    speedId: row && "speedId" in row && row.speedId ? String(row.speedId) : "none",
  };
}
export function validateQuota(values: QuotaValues): FieldErrors {
  const errors: FieldErrors = {};
  for (const key of ["flow", "num", "reset"] as const) {
    const value = Number(values[key]);
    if (
      !values[key].trim() ||
      !Number.isSafeInteger(value) ||
      value < 0 ||
      (key === "reset" && value > 31)
    ) {
      errors[key] = key === "reset" ? "请输入 0–31 的整数" : "请输入大于或等于 0 的整数";
    }
  }
  if (dateTimestamp(values.expiry) === null) errors.expiry = "请选择到期日期";
  return errors;
}
export function userPayload(user: User, status = user.status) {
  return {
    id: user.id,
    user: user.user,
    flow: user.flow,
    num: user.num,
    expTime: user.expTime ?? 0,
    flowResetTime: user.flowResetTime ?? 0,
    status,
  };
}

export function focusFirstError(form: HTMLFormElement) {
  requestAnimationFrame(() => form.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus());
}
