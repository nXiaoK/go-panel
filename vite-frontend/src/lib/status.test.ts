import { describe, expect, it } from "vitest";
import {
  diagnosisItemStatus,
  expiryStatus,
  forwardStatus,
  nodeStatus,
  quotaStatus,
  syncStatus,
  tunnelStatus,
  upgradeStatus,
  userStatus,
} from "./status";
describe("统一实体状态", () => {
  it("覆盖在线、运行、启用、正常与暂停禁用", () => {
    expect(forwardStatus({ status: 1 })).toMatchObject({ tone: "good", label: "运行中" });
    expect(nodeStatus({ status: 1 })).toMatchObject({ tone: "good", label: "在线" });
    expect(tunnelStatus({ status: 1 }).label).toBe("启用");
    expect(userStatus({ status: 1 }).label).toBe("正常");
    expect(forwardStatus({ status: 0 })).toMatchObject({
      tone: "neutral",
      label: "已暂停",
      hollow: true,
    });
    expect(tunnelStatus({ status: 0 }).label).toBe("已禁用");
    expect(userStatus({ status: 0 }).label).toBe("已禁用");
  });
  it("覆盖离线、转发异常和同步失败", () => {
    expect(nodeStatus({ status: 0 }).tone).toBe("critical");
    expect(nodeStatus({ connected: true, responsive: false }).label).toBe("离线");
    expect(forwardStatus({ status: -1 })).toMatchObject({ tone: "critical", label: "异常" });
    expect(syncStatus("failed")).toMatchObject({ tone: "critical", label: "同步失败" });
  });
  it("覆盖临期、过期、升级、部分同步和同步落后", () => {
    const now = 1000000000;
    expect(expiryStatus(now + 3 * 86400000, now)).toMatchObject({
      tone: "warning",
      label: "3 天后到期",
    });
    expect(expiryStatus(now, now)).toMatchObject({ tone: "critical", label: "已到期" });
    expect(expiryStatus(0).label).toBe("长期有效");
    expect(upgradeStatus(true)).toMatchObject({ tone: "warning", label: "可升级" });
    expect(syncStatus("partial").tone).toBe("warning");
    expect(syncStatus("pending")).toMatchObject({ tone: "serious", label: "待同步" });
    expect(syncStatus("synced").tone).toBe("good");
    expect(syncStatus("unknown").tone).toBe("neutral");
  });
  it.each([
    [79, "good"],
    [80, "warning"],
    [94, "warning"],
    [95, "serious"],
    [100, "critical"],
  ])("配额%s%%使用%s", (used, tone) => {
    expect(quotaStatus(Number(used), 100).tone).toBe(tone);
  });
  it("缺失配额不能伪造零，无限配额明确展示", () => {
    expect(quotaStatus(undefined, 100).label).toBe("暂无流量数据");
    expect(quotaStatus(100, 0).label).toBe("不限流量");
  });
  it("外网失败仅警告，真实失败标错误", () => {
    expect(diagnosisItemStatus({ success: false, advisory: true })).toMatchObject({
      tone: "warning",
      label: "警告（仅供参考）",
    });
    expect(diagnosisItemStatus({ success: false })).toMatchObject({
      tone: "critical",
      label: "失败",
    });
    expect(diagnosisItemStatus({ success: true }).tone).toBe("good");
  });
});
