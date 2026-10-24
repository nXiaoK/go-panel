// 面板更新后，仍打开的旧页面可能请求新版本中已不存在的旧代码分块（Vite 触发 vite:preloadError）。
// 首次失败时整页刷新一次以加载新版资源；时间戳标记写在当前标签页的 sessionStorage，
// 60 秒内再次失败不再自动刷新，交给路由错误页处理，避免资源确实缺失时无限循环刷新。
export const PRELOAD_RELOAD_KEY = "go-panel:preload-reload-at";
export const PRELOAD_RELOAD_GUARD_MS = 60_000;

type GuardStorage = Pick<Storage, "getItem" | "setItem">;

interface PreloadRecoveryOptions {
  storage?: () => GuardStorage | null;
  reload?: () => void;
  now?: () => number;
}

function sessionGuardStorage(): GuardStorage | null {
  try {
    return window.sessionStorage;
  } catch {
    return null;
  }
}

/** 返回 true 表示已接管错误并刷新页面；false 表示让错误继续交给错误边界。 */
export function handlePreloadError(event: Event, options: PreloadRecoveryOptions = {}): boolean {
  const storage = (options.storage ?? sessionGuardStorage)();
  const now = (options.now ?? Date.now)();
  try {
    const last = Number(storage?.getItem(PRELOAD_RELOAD_KEY));
    if (
      Number.isFinite(last) &&
      last > 0 &&
      now - last >= 0 &&
      now - last < PRELOAD_RELOAD_GUARD_MS
    )
      return false;
    // 无法写入标记时就无法防止循环，宁可不自动刷新。
    if (!storage) return false;
    storage.setItem(PRELOAD_RELOAD_KEY, String(now));
  } catch {
    return false;
  }
  event.preventDefault();
  (options.reload ?? (() => window.location.reload()))();
  return true;
}

export function installPreloadRecovery(
  target: Pick<Window, "addEventListener" | "removeEventListener"> = window,
  options: PreloadRecoveryOptions = {},
): () => void {
  const listener = (event: Event) => {
    handlePreloadError(event, options);
  };
  target.addEventListener("vite:preloadError", listener);
  return () => target.removeEventListener("vite:preloadError", listener);
}
