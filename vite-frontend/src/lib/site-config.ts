const APP_NAME_CACHE_KEY = "site_app_name";
// 未配置或空值时显示 go-panel；已保存的 app_name（包括旧品牌）始终优先，不迁移现有值。
export const DEFAULT_APP_NAME = "go-panel";

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
}
