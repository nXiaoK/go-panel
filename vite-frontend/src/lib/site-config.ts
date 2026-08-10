const APP_NAME_CACHE_KEY = "site_app_name";
export const DEFAULT_APP_NAME = "Flux Panel";

export function normalizeAppName(value: unknown): string {
  const name = String(value ?? "").trim();
  return name || DEFAULT_APP_NAME;
}

export function appNameFromConfigs(data: unknown): string {
  if (Array.isArray(data)) {
    const found = data.find(
      (item: { name?: unknown; value?: unknown }) => item?.name === "app_name",
    );
    return normalizeAppName(found?.value);
  }
  return normalizeAppName((data as Record<string, unknown> | null | undefined)?.app_name);
}

export function getCachedAppName(): string {
  if (typeof window === "undefined") return DEFAULT_APP_NAME;
  return normalizeAppName(window.localStorage.getItem(APP_NAME_CACHE_KEY));
}

export function setCachedAppName(value: unknown) {
  if (typeof window === "undefined") return;
  const appName = normalizeAppName(value);
  window.localStorage.setItem(APP_NAME_CACHE_KEY, appName);
  window.dispatchEvent(new CustomEvent("site-config-updated", { detail: { appName } }));
  if (document.title.includes("Flux Panel")) {
    document.title = document.title.replaceAll("Flux Panel", appName);
  }
}
