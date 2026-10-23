import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useSession } from "./use-session";
import {
  clearSession,
  configureSessionForTest,
  readSession,
  setSession,
  setPasswordChangeRequired,
  SESSION_CHANGED_EVENT,
  SESSION_INVALIDATED_EVENT,
} from "@/lib/session";
import { roleLabel } from "@/lib/capabilities";

beforeEach(() => {
  localStorage.clear();
  configureSessionForTest({ storage: localStorage, navigate: vi.fn() });
});
afterEach(cleanup);
describe("会话订阅", () => {
  it("旧存储身份在首帧即可正确显示，未变化的快照引用稳定", () => {
    localStorage.setItem("token", "legacy");
    localStorage.setItem("role_id", "0");
    localStorage.setItem("name", "管理者");
    localStorage.setItem("must_change_pwd", "1");
    const frames: boolean[] = [];
    const { result } = renderHook(() => {
      const value = useSession();
      frames.push(value.isAdmin);
      return value;
    });
    expect(frames[0]).toBe(true);
    expect(result.current).toMatchObject({
      isAdmin: true,
      roleId: 0,
      name: "管理者",
      mustChangePassword: true,
    });
    expect(readSession()).toBe(readSession());
  });
  it("原子登录、改密和退出会即时通知订阅者，下一账户不会继承管理员权限", () => {
    const { result } = renderHook(useSession);
    act(() => setSession({ token: "admin", roleId: 0, name: "root", mustChangePassword: true }));
    expect(result.current.isAdmin).toBe(true);
    act(() => setPasswordChangeRequired(false));
    expect(result.current.mustChangePassword).toBe(false);
    act(() => clearSession());
    expect(result.current).toMatchObject({ token: null, roleId: null, name: null, isAdmin: false });
    act(() => setSession({ token: "user", roleId: 1, name: "alice", mustChangePassword: false }));
    expect(result.current).toMatchObject({
      isAdmin: false,
      name: "alice",
      mustChangePassword: false,
    });
    expect(localStorage.getItem("must_change_pwd")).toBeNull();
  });
  it("storage登录/退出同步快照并广播清缓存事件，其他键不改变会话", () => {
    const changed = vi.fn(),
      invalidated = vi.fn();
    window.addEventListener(SESSION_CHANGED_EVENT, changed);
    window.addEventListener(SESSION_INVALIDATED_EVENT, invalidated);
    const { result } = renderHook(useSession);
    act(() => {
      localStorage.setItem("token", "other-tab");
      localStorage.setItem("role_id", "0");
      window.dispatchEvent(
        new StorageEvent("storage", { key: "token", storageArea: localStorage }),
      );
    });
    expect(result.current.isAdmin).toBe(true);
    expect(changed).toHaveBeenCalledOnce();
    const previous = readSession();
    act(() => window.dispatchEvent(new StorageEvent("storage", { key: "pref_notify" })));
    expect(readSession()).toBe(previous);
    expect(changed).toHaveBeenCalledOnce();
    act(() => {
      localStorage.clear();
      window.dispatchEvent(new StorageEvent("storage", { key: null }));
    });
    expect(result.current.token).toBeNull();
    expect(invalidated).toHaveBeenCalledOnce();
    window.removeEventListener(SESSION_CHANGED_EVENT, changed);
    window.removeEventListener(SESSION_INVALIDATED_EVENT, invalidated);
  });
  it.each([null, "", "NaN", "Infinity"])("非法角色 %s 安全回退普通用户", (role) => {
    if (role !== null) localStorage.setItem("role_id", role);
    const { result } = renderHook(useSession);
    expect(result.current.isAdmin).toBe(false);
    expect(result.current.roleId).toBeNull();
    expect(roleLabel(2)).toBe("普通用户");
    expect(roleLabel(0)).toBe("管理员");
  });
});
