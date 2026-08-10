/**
 * Centralized session lifecycle. A protected 401 clears the stored session and
 * navigates to /login exactly once, even when several in-flight protected
 * requests fail concurrently. Public requests must never call this.
 */

export type SessionSnapshot = {
  token: string | null;
  roleID: number | null;
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
}

export function readSession(): SessionSnapshot {
  const storage = deps.storage;
  if (!storage) return { token: null, roleID: null, name: null };
  const rawRole = storage.getItem("role_id");
  const roleID = rawRole === null || rawRole === "" ? null : Number(rawRole);
  return {
    token: storage.getItem("token"),
    roleID: Number.isNaN(roleID as number) ? null : roleID,
    name: storage.getItem("name"),
  };
}

export function getRoleID(): number | null {
  return readSession().roleID;
}

/** markSessionActive re-arms invalidation after a successful (re)login. */
export function markSessionActive(): void {
  invalidating = false;
  dispatchSessionEvent(SESSION_CHANGED_EVENT, "login");
}

/** Clear credentials and notify UI caches during an explicit logout. */
export function clearSession(reason = "logout"): void {
  invalidating = true;
  removeStoredSession();
  dispatchSessionEvent(SESSION_INVALIDATED_EVENT, reason);
}

export function invalidateSession(reason: string): void {
  if (invalidating) return;
  clearSession(reason);
  if (deps.pathname() !== "/login") {
    deps.navigate("/login");
  }
}
