import {
  AlertCircle,
  AlertTriangle,
  ArrowUpCircle,
  CalendarX,
  Clock,
  Info,
  type LucideIcon,
} from "lucide-react";
export type StatusTone = "good" | "warning" | "serious" | "critical" | "neutral";
export type StatusDescriptor = {
  tone: StatusTone;
  label: string;
  icon?: LucideIcon;
  hollow?: boolean;
};
export function forwardStatus(value: { status: number }): StatusDescriptor {
  return value.status === 1
    ? { tone: "good", label: "运行中" }
    : value.status === 0
      ? { tone: "neutral", label: "已暂停", hollow: true }
      : { tone: "critical", label: "异常", icon: AlertCircle };
}
export function nodeStatus(value: {
  status?: number;
  connected?: boolean;
  responsive?: boolean;
}): StatusDescriptor {
  const online =
    value.connected === undefined
      ? value.status === 1
      : value.connected && value.responsive !== false;
  return { tone: online ? "good" : "critical", label: online ? "在线" : "离线" };
}
export function userStatus(value: { status: number }): StatusDescriptor {
  return value.status === 1
    ? { tone: "good", label: "正常" }
    : { tone: "neutral", label: "已禁用", hollow: true };
}
export function tunnelStatus(value: { status: number }): StatusDescriptor {
  return value.status === 1
    ? { tone: "good", label: "启用" }
    : { tone: "neutral", label: "已禁用", hollow: true };
}
export function expiryStatus(ms: number | null | undefined, now = Date.now()): StatusDescriptor {
  if (!ms || ms < 0) return { tone: "neutral", label: "长期有效" };
  const days = Math.ceil((ms - now) / 86400000);
  if (ms <= now) return { tone: "critical", label: "已到期", icon: CalendarX };
  if (days <= 7) return { tone: "warning", label: `${days} 天后到期`, icon: Clock };
  return { tone: "neutral", label: `${days} 天后到期`, icon: Clock };
}
export function quotaStatus(
  used: number | null | undefined,
  quota: number | null | undefined,
): StatusDescriptor {
  if (used == null || quota == null || !Number.isFinite(used) || !Number.isFinite(quota))
    return { tone: "neutral", label: "暂无流量数据" };
  if (quota <= 0) return { tone: "neutral", label: "不限流量" };
  const percent = (used / quota) * 100;
  if (percent >= 100) return { tone: "critical", label: "流量已用尽", icon: AlertCircle };
  if (percent >= 95) return { tone: "serious", label: "流量即将用尽", icon: AlertTriangle };
  if (percent >= 80) return { tone: "warning", label: "流量使用较高", icon: AlertTriangle };
  return { tone: "good", label: "流量充足" };
}
export function syncStatus(state: string): StatusDescriptor {
  switch (state) {
    case "synced":
      return { tone: "good", label: "已同步" };
    case "partial":
      return { tone: "warning", label: "部分同步", icon: Clock };
    case "pending":
      return { tone: "serious", label: "待同步", icon: AlertTriangle };
    case "failed":
      return { tone: "critical", label: "同步失败", icon: AlertCircle };
    default:
      return { tone: "neutral", label: "同步状态未知", hollow: true };
  }
}
export function upgradeStatus(available: boolean): StatusDescriptor {
  return available
    ? { tone: "warning", label: "可升级", icon: ArrowUpCircle }
    : { tone: "neutral", label: "已是最新版本" };
}
export function diagnosisItemStatus(item: {
  success: boolean;
  advisory?: boolean;
}): StatusDescriptor {
  return item.success
    ? { tone: "good", label: "通过" }
    : item.advisory
      ? { tone: "warning", label: "警告（仅供参考）", icon: Info }
      : { tone: "critical", label: "失败", icon: AlertCircle };
}
