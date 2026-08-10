import { beforeEach, describe, expect, it, vi } from "vitest";

const apiMocks = vi.hoisted(() => ({
  getUserPackageInfo: vi.fn(),
  getNodeList: vi.fn(),
  getTunnelList: vi.fn(),
  getForwardList: vi.fn(),
  getAllUsers: vi.fn(),
}));

vi.mock("../src/lib/api", () => apiMocks);

import {
  failSource,
  fakeSources,
  formatMissingSources,
  healthBadge,
  listFromResponse,
  loadDashboardSources,
  successSource,
} from "../src/lib/dashboard-load";

describe("loadDashboardSources", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("passes the selected tunnel to the package trend request", async () => {
    apiMocks.getUserPackageInfo.mockResolvedValue({ code: 0, data: { statisticsFlows: [] } });

    const result = await loadDashboardSources(false, "7d", 42);

    expect(result.status).toBe("success");
    expect(apiMocks.getUserPackageInfo).toHaveBeenCalledWith({ range: "7d", tunnelId: 42 });
  });

  it("marks one failed admin source as partial", async () => {
    const result = await loadDashboardSources(
      true,
      "24h",
      undefined,
      fakeSources({
        package: successSource({ userInfo: {} }),
        nodes: failSource("node unavailable"),
        tunnels: successSource([]),
        forwards: successSource([]),
        users: successSource([]),
      }),
    );
    expect(result.status).toBe("partial");
    expect(result.errors.nodes).toBe("node unavailable");
    expect(result.completedAt).toBeNull();
    expect(result.missing).toContain("nodes");
  });

  it("marks full success with completedAt only when every source succeeds", async () => {
    const result = await loadDashboardSources(
      true,
      "24h",
      undefined,
      fakeSources({
        package: successSource({ userInfo: { user: "a" } }),
        nodes: successSource([{ id: 1 }]),
        tunnels: successSource([]),
        forwards: successSource([]),
        users: successSource({ records: [{ id: 9 }] }),
      }),
    );
    expect(result.status).toBe("success");
    expect(result.completedAt).toBeInstanceOf(Date);
    expect(result.nodes).toEqual([{ id: 1 }]);
    expect(result.users).toEqual([{ id: 9 }]);
    expect(result.packageData).toEqual({ userInfo: { user: "a" } });
  });

  it("marks error when no required source succeeds", async () => {
    const result = await loadDashboardSources(
      false,
      "24h",
      undefined,
      fakeSources({
        package: failSource("package down"),
      }),
    );
    expect(result.status).toBe("error");
    expect(result.completedAt).toBeNull();
    expect(result.errors.package).toBe("package down");
  });

  it("skips admin sources for non-admin loads", async () => {
    let nodesCalled = false;
    const result = await loadDashboardSources(
      false,
      "7d",
      undefined,
      fakeSources({
        package: successSource({ ok: true }),
        nodes: async () => {
          nodesCalled = true;
          return { ok: true, data: [] };
        },
      }),
    );
    expect(result.status).toBe("success");
    expect(nodesCalled).toBe(false);
    expect(result.nodes).toEqual([]);
  });
});

describe("dashboard health helpers", () => {
  it("lists records payloads and formats badges", () => {
    expect(listFromResponse({ records: [1, 2] })).toEqual([1, 2]);
    expect(healthBadge("partial").label).toBe("partial sync");
    expect(formatMissingSources(["nodes", "users"])).toContain("节点");
  });
});
