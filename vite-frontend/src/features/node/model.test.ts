import { describe, expect, it, vi } from "vitest";
import {
  emptyNodeForm,
  readNodeView,
  saveNodeView,
  validNodeAddress,
  validateNodeForm,
} from "./model";
describe("节点字段校验", () => {
  it.each(["::1", "2001:db8::1", "203.0.113.10", "node.example.com", "localhost"])(
    "接受合法地址 %s",
    (value) => expect(validNodeAddress(value)).toBe(true),
  );
  it.each(["::: /", ":::", "1::2::3", "999.1.1.1", "http://node.example.com", "", "[::1]"])(
    "拒绝无效地址 %s",
    (value) => expect(validNodeAddress(value)).toBe(false),
  );
  it("按字段返回错误，保留非法端口而不替换为默认值", () => {
    expect(validateNodeForm({ ...emptyNodeForm(), portSta: 1.5, portEnd: NaN })).toMatchObject({
      name: expect.any(String),
      serverIp: expect.any(String),
      ipString: expect.any(String),
      portSta: expect.any(String),
      portEnd: expect.any(String),
    });
    expect(
      validateNodeForm({
        ...emptyNodeForm(),
        name: "香港",
        serverIp: "::1",
        ipString: "203.0.113.10\n2001:db8::1",
      }),
    ).toEqual({});
    expect(
      validateNodeForm({ ...emptyNodeForm(), portSta: 9000, portEnd: 8000 }).portEnd,
    ).toContain("起始端口");
  });
  it("视图偏好正确保存，存储不可用安全回退", () => {
    saveNodeView("list");
    expect(readNodeView()).toBe("list");
    const get = vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    const set = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    expect(readNodeView()).toBe("grid");
    expect(() => saveNodeView("grid")).not.toThrow();
    get.mockRestore();
    set.mockRestore();
  });
});
