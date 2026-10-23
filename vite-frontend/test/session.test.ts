import { describe, expect, it, vi, beforeEach } from "vitest";

import {
  clearSession,
  configureSessionForTest,
  invalidateSession,
  markSessionActive,
  readSession,
  getRoleID,
  isPasswordChangeRequired,
  setPasswordChangeRequired,
  requirePasswordChange,
  passwordChangeRedirect,
} from "../src/lib/session";

function createMemoryStorage(seed: Record<string, string> = {}) {
  const map = new Map<string, string>(Object.entries(seed));
  return {
    getItem: (k: string) => (map.has(k) ? map.get(k)! : null),
    setItem: (k: string, v: string) => void map.set(k, v),
    removeItem: (k: string) => void map.delete(k),
  };
}

describe("session", () => {
  beforeEach(() => {
    markSessionActive();
  });

  it("invalidates once for concurrent protected 401 responses", () => {
    const storage = createMemoryStorage({
      token: "t",
      role_id: "1",
      name: "alice",
      must_change_pwd: "1",
    });
    const navigate = vi.fn();
    configureSessionForTest({ storage, navigate, pathname: "/forward" });
    invalidateSession("expired");
    invalidateSession("deleted");
    expect(storage.getItem("token")).toBeNull();
    expect(storage.getItem("role_id")).toBeNull();
    expect(navigate).toHaveBeenCalledTimes(1);
    expect(navigate).toHaveBeenCalledWith("/login?reason=expired");
  });

  it("does not navigate when already on the login page", () => {
    const storage = createMemoryStorage({ token: "t" });
    const navigate = vi.fn();
    configureSessionForTest({ storage, navigate, pathname: "/login" });
    invalidateSession("expired");
    expect(navigate).not.toHaveBeenCalled();
    expect(storage.getItem("token")).toBeNull();
  });

  it("clears the complete stored identity during an explicit logout", () => {
    const storage = createMemoryStorage({
      token: "t",
      role_id: "1",
      name: "alice",
      must_change_pwd: "1",
    });
    const navigate = vi.fn();
    configureSessionForTest({ storage, navigate, pathname: "/profile" });

    clearSession();

    expect(readSession()).toEqual({
      token: null,
      roleId: null,
      name: null,
      mustChangePassword: false,
    });
    expect(storage.getItem("must_change_pwd")).toBeNull();
    expect(navigate).not.toHaveBeenCalled();
  });

  it("re-arms after markSessionActive so a later logout works", () => {
    const storage = createMemoryStorage({ token: "t" });
    const navigate = vi.fn();
    configureSessionForTest({ storage, navigate, pathname: "/forward" });
    invalidateSession("first");
    markSessionActive();
    storage.setItem("token", "t2");
    invalidateSession("second");
    expect(navigate).toHaveBeenCalledTimes(2);
  });

  it("reads the current session snapshot and role", () => {
    const storage = createMemoryStorage({ token: "t", role_id: "0", name: "root" });
    configureSessionForTest({ storage, navigate: vi.fn(), pathname: "/" });
    expect(readSession()).toEqual({
      token: "t",
      roleId: 0,
      name: "root",
      mustChangePassword: false,
    });
    expect(getRoleID()).toBe(0);
  });
});

describe("forced password change", () => {
  it("persists the requirement and redirects other pages while preserving credentials", () => {
    const storage = createMemoryStorage({ token: "t", role_id: "1" });
    const navigate = vi.fn();
    configureSessionForTest({ storage, navigate, pathname: "/forward" });
    requirePasswordChange();
    requirePasswordChange();
    expect(storage.getItem("must_change_pwd")).toBe("1");
    expect(storage.getItem("token")).toBe("t");
    expect(navigate).toHaveBeenCalledExactlyOnceWith("/profile?forcePassword=1");
    // 重建依赖模拟刷新，守卫仍从持久存储读取要求。
    configureSessionForTest({ storage, navigate, pathname: "/forward" });
    expect(passwordChangeRedirect("/forward")).toBe("/profile?forcePassword=1");
    expect(passwordChangeRedirect("/profile")).toBeNull();
    setPasswordChangeRequired(false);
    expect(isPasswordChangeRequired()).toBe(false);
    expect(passwordChangeRedirect("/forward")).toBeNull();
  });
});
