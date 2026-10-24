import { afterEach, describe, expect, it, vi } from "vitest";

import {
  handlePreloadError,
  installPreloadRecovery,
  PRELOAD_RELOAD_GUARD_MS,
  PRELOAD_RELOAD_KEY,
} from "../src/lib/preload-recovery";

function memoryStorage() {
  const values = new Map<string, string>();
  return {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => void values.set(key, value),
  };
}
const preloadError = () => new Event("vite:preloadError", { cancelable: true });

afterEach(() => {
  window.sessionStorage.clear();
});

describe("vite:preloadError 自动刷新", () => {
  it("首次失败刷新一次并记录标记，短时间内再次失败交给错误页", () => {
    const storage = memoryStorage();
    const reload = vi.fn();
    let now = 1_000_000;
    const options = { storage: () => storage, reload, now: () => now };
    const first = preloadError();
    expect(handlePreloadError(first, options)).toBe(true);
    expect(first.defaultPrevented).toBe(true);
    expect(reload).toHaveBeenCalledOnce();
    expect(storage.getItem(PRELOAD_RELOAD_KEY)).toBe("1000000");

    now += 2_000;
    const second = preloadError();
    expect(handlePreloadError(second, options)).toBe(false);
    expect(second.defaultPrevented).toBe(false);
    expect(reload).toHaveBeenCalledOnce();

    // 防循环窗口过后的新故障（例如下一次更新）仍可自动恢复一次。
    now += PRELOAD_RELOAD_GUARD_MS;
    expect(handlePreloadError(preloadError(), options)).toBe(true);
    expect(reload).toHaveBeenCalledTimes(2);
  });

  it("存储不可用时无法防止循环，不自动刷新", () => {
    const reload = vi.fn();
    const throwing = {
      getItem: () => null,
      setItem: () => {
        throw new Error("blocked");
      },
    };
    expect(handlePreloadError(preloadError(), { storage: () => throwing, reload })).toBe(false);
    expect(handlePreloadError(preloadError(), { storage: () => null, reload })).toBe(false);
    expect(reload).not.toHaveBeenCalled();
  });

  it("在窗口上注册监听，卸载后不再处理", () => {
    const reload = vi.fn();
    const uninstall = installPreloadRecovery(window, { reload });
    const event = preloadError();
    window.dispatchEvent(event);
    expect(event.defaultPrevented).toBe(true);
    expect(reload).toHaveBeenCalledOnce();
    expect(window.sessionStorage.getItem(PRELOAD_RELOAD_KEY)).not.toBeNull();
    uninstall();
    window.sessionStorage.clear();
    window.dispatchEvent(preloadError());
    expect(reload).toHaveBeenCalledOnce();
  });
});
