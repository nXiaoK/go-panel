import { describe, expect, it } from "vitest";
import {
  dateTimestamp,
  extendExpiry,
  filterUsers,
  quotaValues,
  userSearch,
  validateQuota,
} from "./model";
describe("用户表单与列表模型", () => {
  it("昵称也参与本地搜索", () => {
    expect(filterUsers([{ user: "alice", name: "香港用户" }], "香港")).toHaveLength(1);
  });
  it("日历续期截断月底，过期后从今天起算", () => {
    const now = new Date("2028-01-01T10:00:00");
    expect(extendExpiry("2028-01-31", 1, now)).toBe("2028-02-29");
    expect(extendExpiry("2027-01-01", 3, now)).toBe("2028-04-01");
    expect(extendExpiry("2028-02-29", 12, now)).toBe("2029-02-28");
    expect(dateTimestamp("2028-02-31")).toBeNull();
  });
  it("必填日期、非负整数配额和0至31重置日", () => {
    expect(validateQuota({ ...quotaValues(), flow: "", num: "1.5", reset: "32" })).toMatchObject({
      flow: expect.any(String),
      num: expect.any(String),
      reset: expect.any(String),
      expiry: expect.any(String),
    });
    expect(
      validateQuota({ ...quotaValues(), flow: "0", num: "0", reset: "31", expiry: "2030-01-01" }),
    ).toEqual({});
  });
  it("数字状态书签和非法筛选规范化", () => {
    expect(userSearch({ status: 0, expiry: "unknown", sort: "bad", extra: "keep" })).toMatchObject({
      status: "0",
      expiry: "all",
      sort: "user",
      extra: "keep",
    });
  });
});
