import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { runInNewContext } from "node:vm";
import bootstrap from "../src/theme-init.js?raw";
import { useTheme } from "@/hooks/use-theme";
import { getThemePreference, setThemePreference } from "@/lib/theme";

let dark = false;
let changes: Set<() => void>;
function View() {
  const { preference, resolved } = useTheme();
  return (
    <output>
      {preference}/{resolved}
    </output>
  );
}
beforeEach(() => {
  localStorage.clear();
  dark = false;
  changes = new Set();
  vi.stubGlobal(
    "matchMedia",
    vi.fn(() => ({
      get matches() {
        return dark;
      },
      addEventListener: (_event: string, callback: () => void) => changes.add(callback),
      removeEventListener: (_event: string, callback: () => void) => changes.delete(callback),
    })),
  );
  setThemePreference("dark");
  localStorage.clear();
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  localStorage.clear();
  document.documentElement.className = "";
  document.documentElement.style.colorScheme = "";
});
it("保留默认深色，切换后持久化并同步所有订阅者", () => {
  render(
    <>
      <View />
      <View />
    </>,
  );
  expect(screen.getAllByText("dark/dark")).toHaveLength(2);
  act(() => setThemePreference("light"));
  expect(screen.getAllByText("light/light")).toHaveLength(2);
  expect(localStorage.getItem("pref_theme")).toBe("light");
  expect(document.documentElement.classList.contains("dark")).toBe(false);
  expect(document.documentElement.style.colorScheme).toBe("light");
});
it("跟随系统实时切换，显式选择不被系统变化覆盖，卸载清理监听", () => {
  const view = render(<View />);
  act(() => setThemePreference("system"));
  expect(screen.getByText("system/light")).toBeTruthy();
  act(() => {
    dark = true;
    changes.forEach((change) => change());
  });
  expect(screen.getByText("system/dark")).toBeTruthy();
  act(() => setThemePreference("light"));
  act(() => {
    changes.forEach((change) => change());
  });
  expect(screen.getByText("light/light")).toBeTruthy();
  view.unmount();
  expect(changes.size).toBe(0);
});
it("其他标签页修改或清除偏好时同步，忽略其他键", () => {
  render(<View />);
  act(() => {
    localStorage.setItem("pref_theme", "light");
    window.dispatchEvent(new StorageEvent("storage", { key: "pref_theme" }));
  });
  expect(screen.getByText("light/light")).toBeTruthy();
  act(() => window.dispatchEvent(new StorageEvent("storage", { key: "token" })));
  expect(screen.getByText("light/light")).toBeTruthy();
  act(() => {
    localStorage.clear();
    window.dispatchEvent(new StorageEvent("storage", { key: null }));
  });
  expect(screen.getByText("dark/dark")).toBeTruthy();
});
it("存储受限仍能在本页切换", () => {
  vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
    throw new DOMException("denied", "SecurityError");
  });
  vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
    throw new DOMException("denied", "SecurityError");
  });
  render(<View />);
  expect(screen.getByText("dark/dark")).toBeTruthy();
  act(() => setThemePreference("light"));
  expect(screen.getByText("light/light")).toBeTruthy();
});
it.each([null, "invalid", "light", "dark", "system"])(
  "首屏脚本与运行时选择一致：%s",
  (preference) => {
    if (preference) localStorage.setItem("pref_theme", preference);
    runInNewContext(bootstrap, { document, window, localStorage });
    const expected = preference === "light" || preference === "system" ? "light" : "dark";
    expect(document.documentElement.style.colorScheme).toBe(expected);
    expect(getThemePreference()).toBe(
      preference === "light" || preference === "system" ? preference : "dark",
    );
  },
);
it("首屏脚本在禁用存储时回退深色", () => {
  runInNewContext(bootstrap, {
    document,
    window,
    localStorage: {
      getItem() {
        throw new Error("blocked");
      },
    },
  });
  expect(document.documentElement.style.colorScheme).toBe("dark");
});
