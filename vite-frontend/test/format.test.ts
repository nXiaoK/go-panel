import { describe, expect, it } from "vitest";
import {
  bytesToGb,
  formatBytes,
  formatRate,
  formatMbps,
  formatGbQuota,
  formatDuration,
  formatDate,
  formatDateTime,
  formatRelative,
} from "@/lib/format";
describe("统一格式化", () => {
  it.each([
    [0, "0 B"],
    [1023, "1023 B"],
    [1024, "1.00 KB"],
    [1.5 * 1024 ** 3, "1.50 GB"],
    [-1, "0 B"],
    [NaN, "0 B"],
    [Infinity, "0 B"],
    [12.345 * 1024, "12.3 KB"],
    [123.45 * 1024, "123 KB"],
    [1024 ** 4, "1.00 TB"],
  ])("字节 %s → %s", (value, output) => expect(formatBytes(value)).toBe(output));
  it("精度可指定且有安全边界，速率统一加/s", () => {
    expect(formatBytes(1536, { precision: 0 })).toBe("2 KB");
    expect(formatBytes(1024, { precision: -1 })).toBe("1 KB");
    expect(formatBytes(0.5)).toBe("0.50 B");
    expect(formatRate(0.5)).toBe("0.50 B/s");
    expect(formatRate(1536)).toBe("1.50 KB/s");
    expect(formatRate(NaN)).toBe("0 B/s");
    expect(formatGbQuota(12.3 * 1024 ** 3, 100)).toBe("12.3 GB / 100 GB");
    expect(bytesToGb(-2)).toBe(0);
    expect(formatMbps(800)).toEqual({ value: "100.00", unit: "MB/s" });
    expect(formatMbps(8192)).toEqual({ value: "1.00", unit: "GB/s" });
  });
  it("中文运行时长及异常值", () => {
    expect(formatDuration(3 * 86400 + 4 * 3600)).toBe("3天4小时");
    expect(formatDuration(720)).toBe("12分钟");
    expect(formatDuration(3660)).toBe("1小时1分钟");
    expect(formatDuration(-1)).toBe("0分钟");
    expect(formatDuration(NaN)).toBe("0分钟");
  });
  it("绝对日期格式固定且相对时间为中文，正确处理过去/未来与非法日期", () => {
    const now = new Date(2026, 9, 23, 14, 5, 6).getTime();
    expect(formatDate(now)).toBe("2026-10-23");
    expect(formatDateTime(now)).toBe("2026-10-23 14:05:06");
    expect(formatDateTime(NaN)).toBe("—");
    expect(formatDate(null)).toBe("—");
    expect(formatRelative(now - 180000, now)).toBe("3 分钟前");
    expect(formatRelative(now + 2 * 86400000, now)).toBe("2 天后过期");
    expect(formatRelative(now - 10, now)).toBe("刚刚");
    expect(formatRelative(undefined, now)).toBe("—");
  });
});
