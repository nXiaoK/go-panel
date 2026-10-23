import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { PropsWithChildren } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { apiPost } from "../src/lib/api/client";
import { useDashboardSources } from "../src/features/dashboard/queries";
import { useNodes, nodeKeys } from "../src/features/node/queries";
import { useSearchItems } from "../src/features/search/queries";
import { useLimitMutation } from "../src/features/limit/queries";
import { accountKeys, useTrafficPackage } from "../src/features/account/queries";
import { invalidateFor, resourceKeys } from "../src/lib/query-keys";
import { buildSystemInfoURL } from "../src/features/node/model";
vi.mock("../src/lib/api/client", async (original) => ({
  ...(await original<typeof import("../src/lib/api/client")>()),
  apiPost: vi.fn(),
}));
let client: QueryClient;
function wrapper({ children }: PropsWithChildren) {
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}
beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  localStorage.setItem("token", "admin-token");
  localStorage.setItem("role_id", "0");
  client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: 30_000 }, mutations: { retry: false } },
  });
  vi.mocked(apiPost).mockImplementation(async (path) =>
    path === "/user/package"
      ? { userInfo: { user: "alice" }, forwards: [] }
      : path === "/node/list"
        ? [{ id: 1, name: "节点", status: 1 }]
        : [],
  );
});
afterEach(() => {
  cleanup();
  client.clear();
  vi.restoreAllMocks();
  vi.unstubAllEnvs();
  localStorage.clear();
});
describe("资源查询与缓存行为", () => {
  it("控制台切换范围/隧道只重取流量，搜索与列表共用实体缓存", async () => {
    const dashboard = renderHook(({ range, tunnelId }) => useDashboardSources(range, tunnelId), {
      wrapper,
      initialProps: { range: "24h", tunnelId: undefined as number | undefined },
    });
    await waitFor(() => expect(dashboard.result.current.data?.status).toBe("success"));
    const search = renderHook(() => useSearchItems(true), { wrapper });
    await waitFor(() =>
      expect(search.result.current.items.some((item) => item.title === "节点")).toBe(true),
    );
    renderHook(() => useNodes(), { wrapper });
    dashboard.rerender({ range: "7d", tunnelId: 9 });
    await waitFor(() =>
      expect(
        vi.mocked(apiPost).mock.calls.filter(([path]) => path === "/user/package"),
      ).toHaveLength(2),
    );
    for (const path of ["/node/list", "/tunnel/list", "/forward/list", "/user/list"])
      expect(vi.mocked(apiPost).mock.calls.filter(([value]) => value === path)).toHaveLength(1);
    expect(client.getQueryData(accountKeys.traffic("7d", 9))).toBeTruthy();
  });
  it("普通用户控制台和搜索不触发管理员节点/隧道/用户查询", async () => {
    localStorage.setItem("role_id", "1");
    const dashboard = renderHook(() => useDashboardSources("24h"), { wrapper });
    const search = renderHook(() => useSearchItems(true), { wrapper });
    await waitFor(() => expect(dashboard.result.current.data?.status).toBe("success"));
    await waitFor(() => expect(search.result.current.isFetching).toBe(false));
    expect(
      vi
        .mocked(apiPost)
        .mock.calls.map(([path]) => path)
        .sort(),
    ).toEqual(["/forward/list", "/user/package"]);
  });
  it.each(["no-token", "forced-password"])("%s 时所有业务实体/流量查询均禁止", async (kind) => {
    if (kind === "no-token") localStorage.removeItem("token");
    else localStorage.setItem("must_change_pwd", "1");
    renderHook(
      () => {
        useDashboardSources("24h");
        useSearchItems(true);
      },
      { wrapper },
    );
    await act(async () => {
      await Promise.resolve();
    });
    expect(apiPost).not.toHaveBeenCalled();
  });
  it("原始节点缓存不能冒充含运行态的节点页缓存", async () => {
    renderHook(() => useNodes(), { wrapper });
    await waitFor(() => expect(client.getQueryData(nodeKeys.rawList())).toBeTruthy());
    const runtime = renderHook(() => useNodes({ runtime: true }), { wrapper });
    await waitFor(() => expect(runtime.result.current.data?.[0].connectionStatus).toBe("online"));
    expect(runtime.result.current.data?.[0].systemInfo).toBeNull();
    expect(client.getQueryData(nodeKeys.rawList())).toEqual([{ id: 1, name: "节点", status: 1 }]);
    expect(vi.mocked(apiPost).mock.calls.filter(([path]) => path === "/node/list")).toHaveLength(2);
  });
  it("限速变更成功使用户授权与账户套餐缓存同步失效", async () => {
    const userKey = resourceKeys.user.tunnels(7);
    const packageKey = accountKeys.package();
    client.setQueryData(userKey, [{ speedName: "旧策略" }]);
    client.setQueryData(packageKey, { speedName: "旧策略" });
    const mutation = renderHook(
      () => useLimitMutation("limit.update", { mutationFn: async () => "ok" }),
      { wrapper },
    );
    await act(async () => {
      await mutation.result.current.mutateAsync();
    });
    expect(client.getQueryState(userKey)?.isInvalidated).toBe(true);
    expect(client.getQueryState(packageKey)?.isInvalidated).toBe(true);
  });
  it.each([
    "user.authorize",
    "user.delete",
    "forward.status",
    "forward.order",
    "subscription.relay",
  ] as const)("%s 同步失效转发与账户流量", async (event) => {
    client.setQueryData(resourceKeys.forward.list(), ["旧转发"]);
    client.setQueryData(accountKeys.traffic("24h"), ["旧流量"]);
    await invalidateFor(client, event);
    expect(client.getQueryState(resourceKeys.forward.list())?.isInvalidated).toBe(true);
    expect(client.getQueryState(accountKeys.traffic("24h"))?.isInvalidated).toBe(true);
  });

  it("会话清缓存后切换流量范围不能用旧账号响应作为占位", async () => {
    const traffic = renderHook(({ range }) => useTrafficPackage(range), {
      wrapper,
      initialProps: { range: "24h" },
    });
    await waitFor(() => expect(traffic.result.current.data?.userInfo.user).toBe("alice"));
    vi.mocked(apiPost).mockImplementation(() => new Promise(() => {}));
    act(() => {
      client.clear();
      localStorage.setItem("token", "new-account");
    });
    traffic.rerender({ range: "7d" });
    expect(traffic.result.current.data).toBeUndefined();
    expect(client.getQueryData(accountKeys.traffic("24h"))).toBeUndefined();
  });
  it("节点状态WebSocket忽略旧地址，保留开发配置子路径与协议", () => {
    localStorage.setItem("panel_address", "https://panel.example/base/api/v1");
    expect(buildSystemInfoURL()).toBe(`ws://${window.location.host}/system-info?type=0`);
    vi.stubEnv("DEV", true);
    vi.stubEnv("VITE_API_BASE", "https://dev.example/base/api/v1");
    expect(buildSystemInfoURL()).toBe("wss://dev.example/base/system-info?type=0");
  });
});
