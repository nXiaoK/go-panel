export type ThemePreference = "dark" | "light" | "system";
export type ResolvedTheme = Exclude<ThemePreference, "system">;

// 外观仅保存在当前浏览器，不写服务端、不随登出清除；缺省深色以保持既有界面。
// system 会实时跟随系统；存储受限时保留内存偏好，刷新后回到可读取的值或深色。
const STORAGE_KEY = "pref_theme";
const CHANGE_EVENT = "theme-preference-changed";
let memoryPreference: ThemePreference | undefined;

function validPreference(value: string | null): ThemePreference {
  return value === "light" || value === "system" ? value : "dark";
}

export function getThemePreference(): ThemePreference {
  if (memoryPreference) return memoryPreference;
  try {
    return validPreference(window.localStorage.getItem(STORAGE_KEY));
  } catch {
    return "dark";
  }
}

export function getResolvedTheme(): ResolvedTheme {
  const preference = getThemePreference();
  return preference === "system"
    ? window.matchMedia?.("(prefers-color-scheme: dark)").matches
      ? "dark"
      : "light"
    : preference;
}

function applyTheme() {
  const theme = getResolvedTheme();
  document.documentElement.classList.toggle("dark", theme === "dark");
  document.documentElement.classList.toggle("light", theme === "light");
  document.documentElement.style.colorScheme = theme;
}

export function setThemePreference(preference: ThemePreference) {
  memoryPreference = preference;
  try {
    window.localStorage.setItem(STORAGE_KEY, preference);
    memoryPreference = undefined;
  } catch {
    // 存储被禁用不阻止当前页面切换，也不影响登录等其他设置。
  }
  applyTheme();
  window.dispatchEvent(new Event(CHANGE_EVENT));
}

export function subscribeTheme(onChange: () => void) {
  const media = window.matchMedia?.("(prefers-color-scheme: dark)");
  const update = () => {
    applyTheme();
    onChange();
  };
  const onStorage = (event: StorageEvent) => {
    if (event.key !== STORAGE_KEY && event.key !== null) return;
    memoryPreference = undefined;
    update();
  };
  media?.addEventListener("change", update);
  window.addEventListener("storage", onStorage);
  window.addEventListener(CHANGE_EVENT, update);
  applyTheme();
  return () => {
    media?.removeEventListener("change", update);
    window.removeEventListener("storage", onStorage);
    window.removeEventListener(CHANGE_EVENT, update);
  };
}
