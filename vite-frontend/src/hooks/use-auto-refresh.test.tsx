import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useAutoRefresh } from "./use-auto-refresh";

function deferred() {
  let resolve!: () => void;
  const promise = new Promise<void>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}

describe("useAutoRefresh", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    window.localStorage.clear();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("runs on the interval", async () => {
    const run = vi.fn(() => Promise.resolve());
    renderHook(() => useAutoRefresh(run, 30000));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30000);
    });
    expect(run).toHaveBeenCalledTimes(1);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30000);
    });
    expect(run).toHaveBeenCalledTimes(2);
  });

  it("prevents overlap while a run is in flight", async () => {
    const gate = deferred();
    const run = vi.fn(() => gate.promise);
    renderHook(() => useAutoRefresh(run, 30000));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(60000);
    });
    expect(run).toHaveBeenCalledTimes(1);
    gate.resolve();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30000);
    });
    expect(run).toHaveBeenCalledTimes(2);
  });

  it("pauses while the document is hidden and resumes on visibility", async () => {
    const run = vi.fn(() => Promise.resolve());
    const visibility = vi.spyOn(document, "visibilityState", "get");
    visibility.mockReturnValue("hidden");
    renderHook(() => useAutoRefresh(run, 30000));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(90000);
    });
    expect(run).not.toHaveBeenCalled();

    visibility.mockReturnValue("visible");
    await act(async () => {
      document.dispatchEvent(new Event("visibilitychange"));
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(run).toHaveBeenCalledTimes(1);
    visibility.mockRestore();
  });

  it("stops when disabled and cleans up on unmount", async () => {
    const run = vi.fn(() => Promise.resolve());
    const { unmount } = renderHook(() => useAutoRefresh(run, 30000, false));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(90000);
    });
    expect(run).not.toHaveBeenCalled();
    unmount();
  });
});
