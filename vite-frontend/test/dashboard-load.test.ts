import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createElement, type PropsWithChildren } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { apiPost } from "../src/lib/api/client";
import { useDashboardSources } from "../src/features/dashboard/queries";
import { resourceKeys } from "../src/lib/query-keys";
vi.mock("../src/lib/api/client", async (original) => ({
  ...(await original<typeof import("../src/lib/api/client")>()),
  apiPost: vi.fn(),
}));
let client: QueryClient;
function wrapper({ children }: PropsWithChildren) {
  return createElement(QueryClientProvider, { client }, children);
}
beforeEach(() => {
  localStorage.setItem("token", "test-token");
  localStorage.setItem("role_id", "0");
  client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 30_000 } } });
  vi.mocked(apiPost).mockImplementation(async (path) =>
    path === "/user/package" ? { userInfo: { user: "alice" }, statisticsFlows: [] } : [],
  );
});
afterEach(() => {
  cleanup();
  client.clear();
  localStorage.clear();
  vi.clearAllMocks();
});
describe("独立控制台数据源（替代旧聚合 loader）", () => {
  it("将范围和隧道只传给流量请求", async () => {
    const hook = renderHook(() => useDashboardSources("7d", 42), { wrapper });
    await waitFor(() => expect(hook.result.current.traffic.isSuccess).toBe(true));
    expect(apiPost).toHaveBeenCalledWith(
      "/user/package",
      { range: "7d", tunnelId: 42 },
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    );
  });
  it("节点失败保留其错误和其他成功数据，不伪造空列表为成功", async () => {
    vi.mocked(apiPost).mockImplementation(async (path) => {
      if (path === "/node/list") throw new Error("节点不可达");
      return path === "/user/package" ? { userInfo: { user: "alice" } } : [{ id: 9 }];
    });
    const hook = renderHook(() => useDashboardSources("24h"), { wrapper });
    await waitFor(() => expect(hook.result.current.nodes.isError).toBe(true));
    expect(hook.result.current.nodes.error?.message).toBe("节点不可达");
    expect(hook.result.current.nodes.data).toBeUndefined();
    expect(hook.result.current.traffic.data?.userInfo.user).toBe("alice");
    expect(hook.result.current.users.data).toEqual([{ id: 9 }]);
    expect(hook.result.current.lastSync).toBeGreaterThan(0);
  });
  it("所有数据成功后记录真实更新时间，实体缓存复用", async () => {
    const hook = renderHook(() => useDashboardSources("24h"), { wrapper });
    await waitFor(() => expect(hook.result.current.users.isSuccess).toBe(true));
    expect(hook.result.current.lastSync).toBeGreaterThan(0);
    expect(client.getQueryData(resourceKeys.node.rawList())).toEqual([]);
    expect(vi.mocked(apiPost).mock.calls.filter(([path]) => path === "/tunnel/list")).toHaveLength(
      1,
    );
  });
  it("全部接口失败时无成功时间且每个区块保留自己的错误", async () => {
    vi.mocked(apiPost).mockRejectedValue(new Error("全部不可达"));
    const hook = renderHook(() => useDashboardSources("24h"), { wrapper });
    await waitFor(() => expect(hook.result.current.users.isError).toBe(true));
    expect(hook.result.current.lastSync).toBe(0);
    for (const key of ["traffic", "nodes", "tunnels", "forwards", "users"] as const)
      expect(hook.result.current[key].error?.message).toBe("全部不可达");
  });
  it("普通用户仅查询两个安全接口，刷新仍保持隔离", async () => {
    localStorage.setItem("role_id", "1");
    const hook = renderHook(() => useDashboardSources("24h"), { wrapper });
    await waitFor(() => expect(hook.result.current.userTunnels.isSuccess).toBe(true));
    expect(
      vi
        .mocked(apiPost)
        .mock.calls.map(([path]) => path)
        .sort(),
    ).toEqual(["/tunnel/user/tunnel", "/user/package"]);
    await act(async () => {
      await hook.result.current.refetch();
    });
    expect(
      vi
        .mocked(apiPost)
        .mock.calls.map(([path]) => path)
        .sort(),
    ).toEqual(["/tunnel/user/tunnel", "/tunnel/user/tunnel", "/user/package", "/user/package"]);
  });
  it("刷新失效所有当前源，不使其他流量范围失效", async () => {
    const other = resourceKeys.account.traffic("30d", 3);
    client.setQueryData(other, { old: true });
    const hook = renderHook(() => useDashboardSources("24h"), { wrapper });
    await waitFor(() => expect(hook.result.current.users.isSuccess).toBe(true));
    await act(async () => {
      await hook.result.current.refetch();
    });
    for (const path of [
      "/node/list",
      "/tunnel/list",
      "/forward/list",
      "/user/list",
      "/user/package",
    ])
      expect(vi.mocked(apiPost).mock.calls.filter(([value]) => value === path)).toHaveLength(2);
    expect(client.getQueryState(other)?.isInvalidated).toBe(false);
  });
});
