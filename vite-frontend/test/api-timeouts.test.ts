import { beforeEach, describe, expect, it, vi } from "vitest";

import type { InternalAxiosRequestConfig } from "axios";
const adapter = vi.hoisted(() => vi.fn());
vi.mock("axios", async (importOriginal) => {
  const actual = await importOriginal<typeof import("axios")>();
  const instance = actual.default.create({ adapter });
  return { ...actual, default: { ...actual.default, create: () => instance } };
});

import {
  applyPanelUpdate,
  cancelPanelUpdate,
  checkPanelUpdate,
  diagnoseForward,
  diagnoseTunnel,
  downloadSiteBackup,
  getPanelUpdateStatus,
  restoreSiteBackup,
  rollbackPanel,
  speedTestTunnel,
  upgradeNode,
} from "../src/lib/api";
import { apiDownload, apiUpload } from "../src/lib/api/client";

describe("long request deadlines", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    adapter.mockImplementation(async (config: InternalAxiosRequestConfig) => ({
      data: config.responseType === "blob" ? new Blob(["backup"]) : { code: 0, data: null },
      headers: {
        "content-type":
          config.responseType === "blob" ? "application/vnd.sqlite3" : "application/json",
      },
      status: 200,
      statusText: "OK",
      config,
    }));
  });

  it.each([
    [undefined, 60_000],
    [0, 60_000],
    [-1, 60_000],
    [1, 51_000],
    [60, 110_000],
    [120, 170_000],
    [121, 170_000],
  ])("keeps speed timeout after server deadline for duration %s", async (duration, timeout) => {
    await speedTestTunnel({ tunnelId: 1, durationSeconds: duration });
    expect(adapter).toHaveBeenCalledWith(
      expect.objectContaining({
        url: "/tunnel/speed-test",
        data: JSON.stringify({ tunnelId: 1, durationSeconds: duration }),
        timeout,
      }),
    );
  });

  it("keeps both diagnosis calls beyond the five minute server window", async () => {
    await diagnoseForward(2);
    await diagnoseTunnel(3);
    expect(adapter).toHaveBeenCalledWith(
      expect.objectContaining({
        url: "/forward/diagnose",
        data: JSON.stringify({ forwardId: 2 }),
        timeout: 305_000,
      }),
    );
    expect(adapter).toHaveBeenCalledWith(
      expect.objectContaining({
        url: "/tunnel/diagnose",
        data: JSON.stringify({ tunnelId: 3 }),
        timeout: 305_000,
      }),
    );
  });

  it("keeps node upgrade connected through its 90 second server wait", async () => {
    await upgradeNode(9);
    expect(adapter).toHaveBeenCalledWith(
      expect.objectContaining({
        url: "/node/upgrade",
        data: JSON.stringify({ id: 9 }),
        timeout: 100_000,
      }),
    );
  });

  it("waits longer than the backend WebSocket upgrade command even with a shorter caller timeout", async () => {
    await upgradeNode(10, { timeoutMs: 1 });
    expect(adapter).toHaveBeenCalledWith(
      expect.objectContaining({ url: "/node/upgrade", timeout: 100_000 }),
    );
  });

  it("waits 25 seconds for update checks and sends the soft update contract bodies", async () => {
    await checkPanelUpdate();
    await getPanelUpdateStatus();
    await applyPanelUpdate("v0.3.0", true);
    await applyPanelUpdate("v0.3.1");
    await cancelPanelUpdate("job-1");
    await rollbackPanel("builtin");
    expect(adapter).toHaveBeenCalledWith(
      expect.objectContaining({ url: "/system/update/check", data: "{}", timeout: 25_000 }),
    );
    expect(adapter).toHaveBeenCalledWith(
      expect.objectContaining({ url: "/system/update/status", data: "{}", timeout: 30_000 }),
    );
    expect(adapter).toHaveBeenCalledWith(
      expect.objectContaining({
        url: "/system/update/apply",
        data: JSON.stringify({ targetVersion: "v0.3.0", force: true }),
        timeout: 30_000,
      }),
    );
    expect(adapter).toHaveBeenCalledWith(
      expect.objectContaining({
        url: "/system/update/apply",
        data: JSON.stringify({ targetVersion: "v0.3.1", force: false }),
      }),
    );
    expect(adapter).toHaveBeenCalledWith(
      expect.objectContaining({
        url: "/system/update/cancel",
        data: JSON.stringify({ jobId: "job-1" }),
      }),
    );
    expect(adapter).toHaveBeenCalledWith(
      expect.objectContaining({
        url: "/system/update/rollback",
        data: JSON.stringify({ target: "builtin" }),
      }),
    );
  });

  it("uses ten minutes for backup download and restore without changing generic defaults", async () => {
    await downloadSiteBackup();
    const file = new File(["backup"], "backup.db");
    await restoreSiteBackup(file);
    expect(adapter).toHaveBeenCalledWith(
      expect.objectContaining({ url: "/backup/download", timeout: 600_000, responseType: "blob" }),
    );
    const restoreCall = adapter.mock.calls[1][0];
    expect(restoreCall.url).toBe("/backup/restore");
    expect(restoreCall.data.get("file")).toBe(file);
    expect(restoreCall.timeout).toBe(600_000);

    await apiDownload("/other-download");
    await apiUpload("/other-upload", new FormData());
    expect(adapter).toHaveBeenCalledWith(
      expect.objectContaining({ url: "/other-download", timeout: 30_000 }),
    );
    expect(adapter).toHaveBeenLastCalledWith(
      expect.objectContaining({
        url: "/other-upload",
        data: expect.any(FormData),
        timeout: 60_000,
      }),
    );
  });
});
