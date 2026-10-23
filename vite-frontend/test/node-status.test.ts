import { afterEach, describe, expect, it, vi } from "vitest";
import { checkNodeStatus } from "../src/lib/api";
import Network from "../src/lib/api/network";
import type { NodeStatusCheck } from "../src/lib/types";

afterEach(() => vi.restoreAllMocks());

describe("single node status API", () => {
  it("sends the selected ID and preserves a failed-ping response as responsive", async () => {
    const status: NodeStatusCheck = {
      nodeId: 17,
      name: "hk-01",
      forwardMode: "gost",
      dbStatus: 1,
      connected: true,
      responsive: true,
      roundTripMs: 23,
      version: "1.0.0",
      latestVersion: "1.2.8",
      upgradeAvailable: true,
      syncState: "failed",
      syncLastError: "规则被拒绝",
      syncLastAttempt: 123,
      checkedAt: 456,
    };
    const response = { code: 0, msg: "操作成功", data: status };
    const post = vi.spyOn(Network, "post").mockResolvedValue(response);
    expect(await checkNodeStatus(17)).toEqual(response);
    expect(post).toHaveBeenCalledWith("/node/check-status", { nodeId: 17 });
  });
});
