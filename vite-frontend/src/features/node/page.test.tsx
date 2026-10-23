import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ConfirmProvider } from "@/components/patterns/confirm-dialog";
import { apiPost } from "@/lib/api/client";
import { setSession, clearSession } from "@/lib/session";
import { NodePage } from "./page";
import { nodeSearch } from "./model";
import { nodeLiveStore } from "./live-store";
const post = vi.mocked(apiPost);
vi.mock("@/lib/api/client", () => ({
  apiPost: vi.fn(),
  errorMessage: (error: unknown) => (error instanceof Error ? error.message : "请求失败"),
}));
const nodes = [
  {
    id: 1,
    name: "香港在线",
    ip: "203.0.113.10",
    serverIp: "203.0.113.10",
    status: 1,
    portSta: 1000,
    portEnd: 60000,
    version: "1.2.6",
    forwardMode: "gost",
    syncState: "synced",
    upgradeAvailable: true,
    latestVersion: "1.2.7",
  },
  {
    id: 2,
    name: "日本离线",
    ip: "198.51.100.7",
    serverIp: "198.51.100.7",
    status: 0,
    portSta: 20000,
    portEnd: 30000,
    version: "1.3.12",
    forwardMode: "nftables",
    syncState: "failed",
    syncLastError: "等待节点重连",
    upgradeAvailable: true,
    latestVersion: "1.3.13",
  },
];
let client: QueryClient;
let copy: ReturnType<typeof vi.fn>;
beforeEach(() => {
  localStorage.clear();
  nodeLiveStore.clear();
  client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  setSession({ token: "admin", roleId: 0, name: "管理员", mustChangePassword: false });
  vi.stubGlobal(
    "ResizeObserver",
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  );
  copy = vi.fn().mockResolvedValue(undefined);
  vi.stubGlobal(
    "navigator",
    Object.assign(Object.create(navigator), { clipboard: { writeText: copy } }),
  );
  vi.stubGlobal(
    "WebSocket",
    class {
      close() {}
    },
  );
  vi.stubGlobal("matchMedia", () => ({
    matches: false,
    addEventListener() {},
    removeEventListener() {},
  }));
  vi.spyOn(window, "scrollTo").mockImplementation(() => {});
  post.mockImplementation(async (path) => {
    if (path === "/node/list") return nodes;
    if (path === "/tunnel/list")
      return [
        { id: 9, inNodeId: 1, outNodeId: 2 },
        { id: 10, inNodeId: 1, relayNodeId: 1 },
      ];
    if (path === "/node/install" || path === "/node/uninstall")
      return "echo protected-node-command";
    if (path === "/node/check-status")
      return {
        nodeId: 1,
        name: "香港在线",
        connected: true,
        responsive: true,
        roundTripMs: 3,
        dbStatus: 1,
        version: "1.2.6",
        latestVersion: "1.2.7",
        forwardMode: "gost",
        syncState: "synced",
        syncLastAttempt: 0,
        checkedAt: Date.now(),
      };
    return "ok";
  });
});
afterEach(() => {
  cleanup();
  clearSession();
  client.clear();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  vi.clearAllMocks();
});
async function mount(url = "/node") {
  const root = createRootRoute({
    component: () => (
      <QueryClientProvider client={client}>
        <ConfirmProvider>
          <NodePage />
        </ConfirmProvider>
      </QueryClientProvider>
    ),
  });
  const route = createRoute({
    getParentRoute: () => root,
    path: "/node",
    validateSearch: nodeSearch,
  });
  const router = createRouter({
    routeTree: root.addChildren([route]),
    history: createMemoryHistory({ initialEntries: [url] }),
  });
  await router.load();
  render(<RouterProvider router={router} />);
  await screen.findByRole("heading", { name: "节点", level: 1, hidden: true });
  return router;
}
async function ready() {
  await screen.findByRole("article", { name: "节点 香港在线" });
}
function menu(name: string) {
  fireEvent.pointerDown(screen.getByRole("button", { name: `节点操作：${name}` }), {
    button: 0,
    ctrlKey: false,
    pointerType: "mouse",
  });
}
describe("节点页面的真实路由和操作", () => {
  it("离线筛选只显示离线节点，网格默认异常优先且无伪造指标", async () => {
    await mount("/node?status=offline");
    expect(await screen.findByRole("article", { name: "节点 日本离线" })).toBeTruthy();
    expect(screen.queryByRole("article", { name: "节点 香港在线" })).toBeNull();
    expect(screen.queryByText(/离线 \d+ 分钟/)).toBeNull();
  });
  it.each(["grid", "list"])("focus数字在%s定位目标并清除筛选", async (view) => {
    if (view === "list") localStorage.setItem("go-panel-node-view", "list");
    const router = await mount("/node?focus=1&q=none&status=offline&mode=nftables");
    await waitFor(() => expect(router.state.location.search).toEqual({ focus: 1, q: "" }));
    await waitFor(() =>
      expect(document.activeElement?.getAttribute("data-highlighted")).toBe("true"),
    );
    expect(document.activeElement?.textContent).toContain("香港在线");
  });
  it("create=1只消费一次，添加表单不显示协议屏蔽，编辑离线显示但禁用", async () => {
    const router = await mount("/node?create=1");
    expect(await screen.findByRole("dialog", { name: "添加节点" })).toBeTruthy();
    expect(screen.queryByText("协议屏蔽")).toBeNull();
    await waitFor(() => expect(router.state.location.search.create).toBeUndefined());
    fireEvent.click(screen.getByRole("button", { name: "取消" }));
    await ready();
    menu("日本离线");
    fireEvent.click(await screen.findByRole("menuitem", { name: "编辑节点" }));
    expect(await screen.findByText("节点在线后才能修改协议屏蔽")).toBeTruthy();
    for (const control of screen.getAllByRole("switch"))
      expect(control.hasAttribute("disabled")).toBe(true);
  });
  it("字段错误展示且不调用创建接口", async () => {
    await mount("/node?create=1");
    const dialog = await screen.findByRole("dialog", { name: "添加节点" });
    fireEvent.submit(dialog.querySelector("form")!);
    expect(await screen.findByText("请输入节点名称")).toBeTruthy();
    expect(screen.getByRole("textbox", { name: /节点名称/ }).getAttribute("aria-invalid")).toBe(
      "true",
    );
    expect(post.mock.calls.some(([path]) => path === "/node/create")).toBe(false);
  });
  it("安装命令只在用户点击复制后写剪贴板", async () => {
    await mount();
    await ready();
    fireEvent.click(
      within(screen.getByRole("article", { name: "节点 香港在线" })).getByRole("button", {
        name: "安装命令",
      }),
    );
    expect(await screen.findByText("echo protected-node-command")).toBeTruthy();
    expect(copy).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "复制命令" }));
    await waitFor(() => expect(copy).toHaveBeenCalledWith("echo protected-node-command"));
  });
  it("命令失败在区域中提供配置入口且检测使用指定节点", async () => {
    await mount();
    await ready();
    fireEvent.click(
      within(screen.getByRole("article", { name: "节点 香港在线" })).getByRole("button", {
        name: "检测",
      }),
    );
    expect(await screen.findByText("3 ms")).toBeTruthy();
    expect(post).toHaveBeenCalledWith("/node/check-status", { nodeId: 1 }, undefined);
    fireEvent.click(screen.getByRole("button", { name: "关闭诊断" }));
    post.mockImplementationOnce(async () => {
      throw new Error("请先设置面板公网地址");
    });
    fireEvent.click(
      within(screen.getByRole("article", { name: "节点 香港在线" })).getByRole("button", {
        name: "安装命令",
      }),
    );
    expect(await screen.findByText("请先在系统配置中设置面板公网地址")).toBeTruthy();
    expect(screen.getByRole("button", { name: "去设置" })).toBeTruthy();
  });
  it("删除显示真实唯一隧道依赖数并要求名称确认", async () => {
    await mount();
    await ready();
    menu("香港在线");
    fireEvent.click(await screen.findByRole("menuitem", { name: "删除节点" }));
    expect(await screen.findByText(/依赖该节点的 2 条隧道/)).toBeTruthy();
    const dialog = screen.getByRole("alertdialog");
    expect(within(dialog).getByRole("button", { name: "删除节点" }).hasAttribute("disabled")).toBe(
      true,
    );
    fireEvent.change(within(dialog).getByRole("textbox"), { target: { value: "香港在线" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "删除节点" }));
    await waitFor(() => expect(post).toHaveBeenCalledWith("/node/delete", { id: 1 }, undefined));
  });
  it("离线节点不能升级，普通角色不查询管理员节点接口", async () => {
    await mount();
    await ready();
    expect(
      within(screen.getByRole("article", { name: "节点 日本离线" }))
        .getByRole("button", { name: "升级" })
        .hasAttribute("disabled"),
    ).toBe(true);
    act(() =>
      setSession({ token: "viewer", roleId: 1, name: "普通用户", mustChangePassword: false }),
    );
    expect(await screen.findByText("无权访问节点")).toBeTruthy();
    const calls = post.mock.calls.length;
    await act(async () => {
      await client.invalidateQueries();
    });
    expect(post).toHaveBeenCalledTimes(calls);
  });
});
