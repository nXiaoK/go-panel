/**
 * Centralized session lifecycle. A protected 401 clears the stored session and
 * navigates to /login exactly once, even when several in-flight protected
 * requests fail concurrently. Public requests must never call this.
 */

export type SessionSnapshot = {
  token: string | null;
  roleId: number | null;
  mustChangePassword: boolean;
  name: string | null;
};

type StorageLike = {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
};

type SessionDeps = {
  storage: StorageLike | null;
  navigate: (to: string) => void;
  pathname: () => string;
};

function browserDeps(): SessionDeps {
  return {
    storage: typeof window === "undefined" ? null : window.localStorage,
    navigate: (to: string) => {
      if (typeof window !== "undefined") window.location.assign(to);
    },
    pathname: () => (typeof window === "undefined" ? "/" : window.location.pathname),
  };
}

let deps: SessionDeps = browserDeps();
let invalidating = false;
let forcingPassword = false;
const listeners = new Set<() => void>();
const EMPTY_SESSION: SessionSnapshot = Object.freeze({
  token: null,
  roleId: null,
  name: null,
  mustChangePassword: false,
});
let snapshot: SessionSnapshot = EMPTY_SESSION;

function loadStoredSession(): SessionSnapshot {
  const storage = deps.storage;
  if (!storage) return EMPTY_SESSION;
  const rawRole = storage.getItem("role_id");
  const role = rawRole === null || rawRole === "" ? null : Number(rawRole);
  return {
    token: storage.getItem("token"),
    roleId: role !== null && Number.isFinite(role) ? role : null,
    name: storage.getItem("name"),
    mustChangePassword: storage.getItem("must_change_pwd") === "1",
  };
}

function refreshSnapshot(): boolean {
  const next = loadStoredSession();
  if (
    Object.keys(next).every(
      (key) => next[key as keyof SessionSnapshot] === snapshot[key as keyof SessionSnapshot],
    )
  )
    return false;
  snapshot = Object.freeze(next);
  return true;
}

function publishSession(): void {
  refreshSnapshot();
  listeners.forEach((listener) => listener());
}

export function subscribeSession(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

export function serverSession(): SessionSnapshot {
  return EMPTY_SESSION;
}

// 兼容旧版本分散保存的四个键；跨标签页登录、退出及改密要求也必须同步 UI 与查询缓存。
if (typeof window !== "undefined") {
  window.addEventListener("storage", (event) => {
    if (event.storageArea && event.storageArea !== deps.storage) return;
    if (event.key !== null && !["token", "role_id", "name", "must_change_pwd"].includes(event.key))
      return;
    if (!refreshSnapshot()) return;
    invalidating = !snapshot.token;
    forcingPassword = false;
    publishSession();
    dispatchSessionEvent(
      snapshot.token ? SESSION_CHANGED_EVENT : SESSION_INVALIDATED_EVENT,
      "storage",
    );
  });
}

export const SESSION_INVALIDATED_EVENT = "session-invalidated";
export const SESSION_CHANGED_EVENT = "session-changed";

function dispatchSessionEvent(name: string, reason: string): void {
  if (typeof window !== "undefined" && typeof CustomEvent === "function") {
    window.dispatchEvent(new CustomEvent(name, { detail: { reason } }));
  }
}

function removeStoredSession(): void {
  const storage = deps.storage;
  if (!storage) return;
  storage.removeItem("token");
  storage.removeItem("role_id");
  storage.removeItem("name");
  storage.removeItem("must_change_pwd");
}

export function configureSessionForTest(overrides: {
  storage: StorageLike;
  navigate: (to: string) => void;
  pathname?: string;
}): void {
  deps = {
    storage: overrides.storage,
    navigate: overrides.navigate,
    pathname: () => overrides.pathname ?? "/",
  };
  invalidating = false;
  forcingPassword = false;
  refreshSnapshot();
}

export function readSession(): SessionSnapshot {
  // 同步读取支持首次加载及旧版调用方迁移；内容不变时始终返回同一引用，供 useSyncExternalStore 使用。
  refreshSnapshot();
  return snapshot;
}

export function getRoleID(): number | null {
  return readSession().roleId;
}

/** markSessionActive re-arms invalidation after a successful (re)login. */
export function markSessionActive(): void {
  invalidating = false;
  forcingPassword = false;
  publishSession();
  dispatchSessionEvent(SESSION_CHANGED_EVENT, "login");
}

/** Clear credentials and notify UI caches during an explicit logout. */
export function clearSession(reason = "logout"): void {
  invalidating = true;
  removeStoredSession();
  publishSession();
  dispatchSessionEvent(SESSION_INVALIDATED_EVENT, reason);
}

export function invalidateSession(reason: string): void {
  if (invalidating) return;
  clearSession(reason);
  if (deps.pathname() !== "/login") {
    deps.navigate(`/login?reason=${reason === "disabled" ? "disabled" : "expired"}`);
  }
}

// 强制改密标记跨刷新保存；登录未要求改密时必须删除，避免沿用上一账户的限制。
export function setPasswordChangeRequired(required: boolean): void {
  if (required) deps.storage?.setItem("must_change_pwd", "1");
  else deps.storage?.removeItem("must_change_pwd");
  publishSession();
}

export function isPasswordChangeRequired(): boolean {
  return readSession().mustChangePassword;
}

export function passwordChangeRedirect(pathname: string): string | null {
  return isPasswordChangeRequired() && pathname !== "/profile" ? "/profile?forcePassword=1" : null;
}

export function requirePasswordChange(): void {
  if (invalidating) return;
  setPasswordChangeRequired(true);
  if (forcingPassword) return;
  forcingPassword = true;
  // 即使当前已在资料页也重新进入强制模式，确保停止套餐及外壳中的业务请求。
  deps.navigate("/profile?forcePassword=1");
}

/** 登录原子更新身份，避免订阅者观察到上一个账户的角色或强制改密标记。 */
export function setSession(session: SessionSnapshot): void {
  const storage = deps.storage;
  if (storage) {
    for (const [key, value] of [
      ["token", session.token],
      ["role_id", session.roleId],
      ["name", session.name],
    ] as const) {
      if (value === null) storage.removeItem(key);
      else storage.setItem(key, String(value));
    }
    if (session.mustChangePassword) storage.setItem("must_change_pwd", "1");
    else storage.removeItem("must_change_pwd");
  }
  markSessionActive();
}
