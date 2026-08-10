import { beforeEach, describe, expect, it, vi } from "vitest";

const sonner = vi.hoisted(() => ({
  success: vi.fn(),
  info: vi.fn(),
  warning: vi.fn(),
  error: vi.fn(),
}));

vi.mock("sonner", () => ({ toast: sonner }));

import { readPreferences, setPreference } from "../src/lib/preferences";
import { notify } from "../src/lib/notify";

describe("preferences", () => {
  beforeEach(() => {
    window.localStorage.clear();
    sonner.success.mockClear();
    sonner.info.mockClear();
    sonner.warning.mockClear();
    sonner.error.mockClear();
  });

  it("defaults to enabled", () => {
    expect(readPreferences()).toEqual({ notify: true, autoRefresh: true });
  });

  it("persists and reads back a change", () => {
    setPreference("notify", false);
    setPreference("autoRefresh", false);
    expect(readPreferences()).toEqual({ notify: false, autoRefresh: false });
    expect(window.localStorage.getItem("pref_notify")).toBe("0");
    expect(window.localStorage.getItem("pref_auto_refresh")).toBe("0");
  });

  it("notifies subscribers via the preferences event", () => {
    const seen: boolean[] = [];
    const onChange = () => seen.push(readPreferences().notify);
    window.addEventListener("preferences-changed", onChange);
    setPreference("notify", false);
    setPreference("notify", true);
    window.removeEventListener("preferences-changed", onChange);
    expect(seen).toEqual([false, true]);
  });
});

describe("notify", () => {
  beforeEach(() => {
    window.localStorage.clear();
    sonner.success.mockClear();
    sonner.info.mockClear();
    sonner.warning.mockClear();
    sonner.error.mockClear();
  });

  it("suppresses success but never errors", () => {
    setPreference("notify", false);
    notify.success("saved");
    notify.error("failed");
    expect(sonner.success).not.toHaveBeenCalled();
    expect(sonner.error).toHaveBeenCalledWith("failed");
  });

  it("suppresses info but never warnings", () => {
    setPreference("notify", false);
    notify.info("hint");
    notify.warning("careful");
    expect(sonner.info).not.toHaveBeenCalled();
    expect(sonner.warning).toHaveBeenCalledWith("careful");
  });

  it("passes success through when enabled", () => {
    notify.success("saved");
    expect(sonner.success).toHaveBeenCalledWith("saved");
  });
});
