import { beforeEach, describe, expect, it, vi } from "vitest";

const httpMocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("axios", () => ({
  default: {
    create: () => httpMocks,
    isAxiosError: () => false,
  },
}));

import {
  diagnoseForward,
  diagnoseTunnel,
  downloadSiteBackup,
  restoreSiteBackup,
  speedTestTunnel,
} from "../src/lib/api";
import Network from "../src/lib/api/network";

describe("long request deadlines", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    httpMocks.post.mockResolvedValue({ data: { code: 0, data: null } });
    httpMocks.get.mockResolvedValue({
      data: new Blob(["backup"]),
      headers: { "content-type": "application/vnd.sqlite3" },
    });
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
    expect(httpMocks.post).toHaveBeenCalledWith(
      "/tunnel/speed-test",
      { tunnelId: 1, durationSeconds: duration },
      expect.objectContaining({ timeout }),
    );
  });

  it("allows both diagnosis calls to finish after the 90 second server window", async () => {
    await diagnoseForward(2);
    await diagnoseTunnel(3);
    expect(httpMocks.post).toHaveBeenCalledWith(
      "/forward/diagnose",
      { forwardId: 2 },
      expect.objectContaining({ timeout: 95_000 }),
    );
    expect(httpMocks.post).toHaveBeenCalledWith(
      "/tunnel/diagnose",
      { tunnelId: 3 },
      expect.objectContaining({ timeout: 95_000 }),
    );
  });

  it("uses ten minutes for backup download and restore without changing generic defaults", async () => {
    await downloadSiteBackup();
    const file = new File(["backup"], "backup.db");
    await restoreSiteBackup(file);
    expect(httpMocks.get).toHaveBeenCalledWith(
      "/backup/download",
      expect.objectContaining({ timeout: 600_000, responseType: "blob" }),
    );
    const restoreCall = httpMocks.post.mock.calls[0];
    expect(restoreCall[0]).toBe("/backup/restore");
    expect(restoreCall[1].get("file")).toBe(file);
    expect(restoreCall[2]).toEqual(expect.objectContaining({ timeout: 600_000 }));

    await Network.download("/other-download");
    await Network.upload("/other-upload", new FormData());
    expect(httpMocks.get).toHaveBeenLastCalledWith(
      "/other-download",
      expect.objectContaining({ timeout: 30_000 }),
    );
    expect(httpMocks.post).toHaveBeenLastCalledWith(
      "/other-upload",
      expect.any(FormData),
      expect.objectContaining({ timeout: 60_000 }),
    );
  });
});
