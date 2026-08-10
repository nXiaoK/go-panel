/**
 * Shared local preferences (browser-only). Values live in localStorage under
 * pref_notify / pref_auto_refresh with a typed change event so React trees and
 * plain modules observe the same state.
 */

export type Preferences = {
  /** Show success/info operation toasts. Errors/warnings are never muted. */
  notify: boolean;
  /** Enable periodic list refresh (30s) on node/forward pages. */
  autoRefresh: boolean;
};

const KEYS: Record<keyof Preferences, string> = {
  notify: "pref_notify",
  autoRefresh: "pref_auto_refresh",
};

export const PREFERENCES_EVENT = "preferences-changed";

function storage(): Storage | null {
  return typeof window === "undefined" ? null : window.localStorage;
}

export function readPreferences(): Preferences {
  const s = storage();
  return {
    notify: s ? s.getItem(KEYS.notify) !== "0" : true,
    autoRefresh: s ? s.getItem(KEYS.autoRefresh) !== "0" : true,
  };
}

export function setPreference<K extends keyof Preferences>(key: K, value: boolean): void {
  const s = storage();
  if (!s) return;
  s.setItem(KEYS[key], value ? "1" : "0");
  if (typeof CustomEvent === "function") {
    window.dispatchEvent(new CustomEvent(PREFERENCES_EVENT, { detail: { key, value } }));
  }
}

export function subscribePreferences(onChange: () => void): () => void {
  if (typeof window === "undefined") return () => {};
  window.addEventListener(PREFERENCES_EVENT, onChange);
  // Another tab may change localStorage directly.
  window.addEventListener("storage", onChange);
  return () => {
    window.removeEventListener(PREFERENCES_EVENT, onChange);
    window.removeEventListener("storage", onChange);
  };
}
