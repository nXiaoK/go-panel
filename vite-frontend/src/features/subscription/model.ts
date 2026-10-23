import type { SubscriptionFormat } from "@/lib/types";

/** 订阅导出格式的界面名称；Shadowrocket 可直接读取本项目生成的 Clash 结构。 */
export const subscriptionFormatLabels: Record<SubscriptionFormat, string> = {
  surge: "Surge",
  clash: "Clash / Shadowrocket",
  singbox: "Sing-box",
  v2rayn: "v2rayN",
};

export const subscriptionFormats: SubscriptionFormat[] = ["surge", "clash", "singbox", "v2rayn"];
