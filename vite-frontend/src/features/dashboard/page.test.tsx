import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { apiPost } from "@/lib/api/client";
import { nodeLiveStore } from "@/features/node/live-store";
import { Dashboard } from "./page";
vi.mock("@/lib/api/client", async (original) => ({
  ...(await original<typeof import("@/lib/api/client")>()),
  apiPost: vi.fn(),
}));
const gb = 1024 ** 3;
let client: QueryClient;
function packageData() {
  return {
    userInfo: {
      id: 2,
      user: "alice",
      flow: 100,
      inFlow: 82.4 * gb,
      outFlow: 0,
      num: 10,
      flowResetTime: 1,
      expTime: Date.now() + 70 * 86400000,
    },
    tunnelPermissions: [
      {
        id: 1,
        tunnelId: 3,
        tunnelName: "hk→jp",
        flow: 50,
        inFlow: 12 * gb,
        outFlow: 0,
        expTime: Date.now() + 86400000,
        speed: 100,
      },
    ],
    forwards: [
      { id: 1, name: "web", status: 1 },
      { id: 2, name: "ssh", status: 0 },
    ],
    trafficRange: "24h",
    trafficTunnels: [{ tunnelId: 3, tunnelName: "hk→jp" }],
    todayTraffic: { totalFlow: 1.2 * gb },
    trafficTotals: { totalFlow: 100 * gb },
    trafficTrendTotals: { inFlow: 3 * gb, outFlow: 2 * gb, totalFlow: 5 * gb },
    statisticsFlows: [{ time: "12:00", inFlow: 3 * gb, outFlow: 2 * gb }],
  };
}
function renderPage(role = 0) {
  localStorage.setItem("role_id", String(role));
  return render(
    <QueryClientProvider client={client}>
      <Dashboard />
    </QueryClientProvider>,
  );
}
beforeEach(() => {
  localStorage.setItem("token", "test-token");
  client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 30_000 } } });
  vi.stubGlobal("matchMedia", () => ({
    matches: false,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  }));
  vi.stubGlobal(
    "ResizeObserver",
    class {
      constructor(private cb: (entries: unknown[]) => void) {}
      observe() {
        this.cb([{ contentRect: { width: 800, height: 280 } }]);
      }
      disconnect() {}
    },
  );
  vi.stubGlobal(
    "WebSocket",
    class {
      static OPEN = 1;
      static CONNECTING = 0;
      readyState = 1;
      close() {}
    },
  );
  vi.mocked(apiPost).mockImplementation(async (path) =>
    path === "/user/package"
      ? packageData()
      : path === "/node/list"
        ? [
            { id: 1, name: "offline", status: 0, version: "1.0" },
            { id: 2, name: "online", status: 1, version: "1.1" },
          ]
        : path === "/tunnel/list"
          ? [{ id: 3, name: "hk→jp", status: 1 }]
          : path === "/forward/list"
            ? [
                { id: 1, name: "broken", status: -1 },
                { id: 2, name: "ok", status: 1 },
              ]
            : path === "/tunnel/user/tunnel"
              ? [
                  {
                    id: 3,
                    name: "hk→jp",
                    ip: "1.2.3.4",
                    inNodePortSta: 10000,
                    inNodePortEnd: 60000,
                    available: true,
                  },
                ]
              : [],
  );
});
afterEach(() => {
  cleanup();
  client.clear();
  nodeLiveStore.clear();
  vi.unstubAllGlobals();
  vi.clearAllMocks();
  localStorage.clear();
});
describe("控制台实际页面", () => {
  it("普通用户仅两个安全接口、四项账户指标与合并后的隧道，不显示管理员区块", async () => {
    const { container } = renderPage(1);
    await screen.findByText("1.2.3.4");
    expect(
      vi
        .mocked(apiPost)
        .mock.calls.map(([path]) => path)
        .sort(),
    ).toEqual(["/tunnel/user/tunnel", "/user/package"]);
    expect(screen.queryByRole("heading", { name: "节点状态" })).toBeNull();
    expect(screen.queryByRole("heading", { name: /需要关注/ })).toBeNull();
    expect(screen.getByLabelText("账户指标").children).toHaveLength(4);
    expect(screen.getByRole("meter", { name: "账户流量配额" }).getAttribute("aria-valuemax")).toBe(
      String(100 * gb),
    );
    expect(screen.getByText("10000–60000")).toBeTruthy();
    expect(screen.getByText("100 Mbps")).toBeTruthy();
    expect(screen.getByRole("link", { name: /已暂停/ }).getAttribute("href")).toBe(
      "/forward?status=0",
    );
    expect(container.querySelector(".lucide-arrow-up-right")).toBeNull();
    expect(screen.queryByText("system nominal")).toBeNull();
    expect(screen.queryByRole("button", { name: "压力测试" })).toBeNull();
  });
  it.each([
    [0, "不自动重置流量"],
    [1, "每月 1 日重置"],
    [undefined, "重置日期未知"],
  ])("如实呈现重置日 %s，不伪造每月1日", async (reset, expected) => {
    const pkg = packageData();
    Object.assign(pkg.userInfo, { flowResetTime: reset });
    const original = vi.mocked(apiPost).getMockImplementation()!;
    vi.mocked(apiPost).mockImplementation((path, ...args) =>
      path === "/user/package" ? Promise.resolve(pkg) : original(path, ...args),
    );
    renderPage(1);
    await screen.findByText("1.2.3.4");
    expect(screen.getByText(expected)).toBeTruthy();
    if (reset !== 1) expect(screen.queryByText("每月 1 日重置")).toBeNull();
  });
  it("范围切换只重新请求流量，加载中保留相同几何与KPI", async () => {
    const { container } = renderPage();
    await screen.findByText("offline 离线");
    const before = container.querySelector('path[data-series="down"]')?.getAttribute("d");
    const stats = screen.getByLabelText("系统指标").textContent;
    let resolve!: (value: unknown) => void;
    vi.mocked(apiPost).mockImplementation((path) =>
      path === "/user/package"
        ? new Promise((done) => {
            resolve = done;
          })
        : Promise.resolve([]),
    );
    fireEvent.click(screen.getByRole("button", { name: "7 天" }));
    await waitFor(() =>
      expect(
        vi.mocked(apiPost).mock.calls.filter(([path]) => path === "/user/package"),
      ).toHaveLength(2),
    );
    expect(container.querySelector('path[data-series="down"]')?.getAttribute("d")).toBe(before);
    expect(screen.getByTestId("traffic-frame").className).toContain("opacity-60");
    expect(screen.getByLabelText("系统指标").textContent).toBe(stats);
    for (const path of ["/node/list", "/tunnel/list", "/forward/list", "/user/list"])
      expect(vi.mocked(apiPost).mock.calls.filter(([value]) => value === path)).toHaveLength(1);
    await act(async () => resolve({ ...packageData(), trafficRange: "7d" }));
    await waitFor(() =>
      expect(screen.getByTestId("traffic-frame").className).not.toContain("opacity-60"),
    );
    fireEvent.click(screen.getByRole("button", { name: "表格" }));
    expect(
      screen.getByRole("table", { name: "流量明细" }).querySelectorAll("tbody tr"),
    ).toHaveLength(7);
  });
  it("管理员五项真实KPI与筛选跳转、无趋势/伪活动/测速，实时指标不被REST刷新覆盖", async () => {
    const { container } = renderPage();
    await screen.findByText("offline 离线");
    const stats = screen.getByLabelText("系统指标");
    expect(stats.children).toHaveLength(5);
    expect(within(stats).getByRole("link", { name: "1 离线" }).getAttribute("href")).toBe(
      "/node?status=offline",
    );
    expect(within(stats).getByRole("link", { name: "1 异常" }).getAttribute("href")).toBe(
      "/forward?status=error",
    );
    expect(container.querySelector(".lucide-arrow-up-right")).toBeNull();
    expect(screen.queryByText("最近活动")).toBeNull();
    expect(screen.queryByRole("button", { name: "压力测试" })).toBeNull();
    expect(screen.getByRole("link", { name: "新建转发" }).getAttribute("href")).toBe(
      "/forward?create=1",
    );
    const table = screen.getByRole("table", { name: "节点状态" });
    expect(within(table).getAllByText("—")).toHaveLength(4);
    act(() =>
      nodeLiveStore.receive({ id: 2, type: "info", data: { cpu_usage: 38.2, memory_usage: 62.4 } }),
    );
    expect(within(table).getByText("38.2%")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "刷新" }));
    await waitFor(() =>
      expect(vi.mocked(apiPost).mock.calls.filter(([path]) => path === "/node/list")).toHaveLength(
        2,
      ),
    );
    expect(within(table).getByText("62.4%")).toBeTruthy();
  });
  it("部分失败仅在相关区块呈现，保留其他数据且不能声称一切正常", async () => {
    const original = vi.mocked(apiPost).getMockImplementation()!;
    vi.mocked(apiPost).mockImplementation((path, ...args) =>
      path === "/node/list" ? Promise.reject(new Error("节点暂不可达")) : original(path, ...args),
    );
    renderPage();
    await screen.findAllByText("节点暂不可达");
    expect(screen.getByText("流量趋势 · 范围内统计")).toBeTruthy();
    expect(screen.getByText("转发 broken 异常")).toBeTruthy();
    expect(screen.queryByText("一切正常")).toBeNull();
    expect(screen.queryByText(/缺失：/)).toBeNull();
    expect(screen.getAllByRole("alert").length).toBeGreaterThanOrEqual(2);
  });
  it("空资源有下一步操作，0GB显示已用尽、不可用授权原因明确", async () => {
    const pkg = packageData();
    pkg.userInfo.flow = 0;
    pkg.tunnelPermissions[0].flow = 0;
    vi.mocked(apiPost).mockImplementation(async (path) =>
      path === "/user/package"
        ? pkg
        : path === "/tunnel/user/tunnel"
          ? [
              {
                id: 3,
                name: "hk→jp",
                ip: "1.2.3.4",
                available: false,
                unavailableReason: "隧道已禁用",
              },
            ]
          : [],
    );
    renderPage(1);
    await screen.findByText("隧道已禁用");
    expect(screen.getAllByText("0 GB 已用尽")).toHaveLength(2);
    expect(screen.queryByText("不限流量")).toBeNull();
    cleanup();
    client.clear();
    renderPage(0);
    await screen.findByText("暂无节点");
    expect(screen.getByRole("link", { name: "添加节点" }).getAttribute("href")).toBe(
      "/node?create=1",
    );
    expect(screen.getByText("一切正常")).toBeTruthy();
  });
});
