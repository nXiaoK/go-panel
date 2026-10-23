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
async function listReady() {
  localStorage.setItem("go-panel-node-view", "list");
  await mount();
  await screen.findByRole("table", { name: "节点列表" });
  await screen.findByText("香港在线");
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
  it("保存失败内联显示、保留输入可重试，再次打开清理旧错误", async () => {
    await mount("/node?create=1");
    await waitFor(() => expect(client.getQueryData(["node", "list", "raw"])).toBeTruthy());
    const dialog = await screen.findByRole("dialog", { name: "添加节点" });
    fireEvent.change(within(dialog).getByRole("textbox", { name: /节点名称/ }), {
      target: { value: "新节点" },
    });
    fireEvent.change(within(dialog).getByRole("textbox", { name: /服务器 IP/ }), {
      target: { value: "::1" },
    });
    fireEvent.change(within(dialog).getByRole("textbox", { name: /入口地址/ }), {
      target: { value: "2001:db8::1" },
    });
    post.mockRejectedValueOnce(new Error("服务暂不可用"));
    fireEvent.submit(dialog.querySelector("form")!);
    expect(await screen.findByText("服务暂不可用")).toBeTruthy();
    expect(
      (within(dialog).getByRole("textbox", { name: /节点名称/ }) as HTMLInputElement).value,
    ).toBe("新节点");
    expect(within(dialog).getByRole("button", { name: "添加节点" }).hasAttribute("disabled")).toBe(
      false,
    );
    fireEvent.click(within(dialog).getByRole("button", { name: "取消" }));
    fireEvent.click(await screen.findByRole("button", { name: "放弃修改" }));
    fireEvent.click(screen.getByRole("button", { name: "添加节点" }));
    expect(await screen.findByRole("dialog", { name: "添加节点" })).toBeTruthy();
    expect(screen.queryByText("服务暂不可用")).toBeNull();
    expect((screen.getByRole("textbox", { name: /节点名称/ }) as HTMLInputElement).value).toBe("");
  });
  it("升级确认写明重启影响，长请求期间加载且防止重复提交", async () => {
    await mount();
    await ready();
    let resolve!: (value: unknown) => void;
    post.mockImplementationOnce(
      () =>
        new Promise((done) => {
          resolve = done;
        }),
    );
    fireEvent.click(
      within(screen.getByRole("article", { name: "节点 香港在线" })).getByRole("button", {
        name: "升级",
      }),
    );
    expect(await screen.findByText(/节点会自动重启并短暂离线/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "升级节点" }));
    await waitFor(() =>
      expect(post.mock.calls.filter(([path]) => path === "/node/upgrade")).toHaveLength(1),
    );
    const button = within(screen.getByRole("article", { name: "节点 香港在线" })).getByRole(
      "button",
      { name: "升级" },
    );
    expect(button.hasAttribute("disabled")).toBe(true);
    fireEvent.click(button);
    expect(post.mock.calls.filter(([path]) => path === "/node/upgrade")).toHaveLength(1);
    await act(async () => resolve({ nodeId: 1, latestVersion: "1.2.7", message: "已触发" }));
    await waitFor(() => expect(button.hasAttribute("disabled")).toBe(false));
  });
  it("桌面列表只内联检测和更多，菜单保留全部操作及正确编辑删除目标", async () => {
    await listReady();
    const row = screen.getByText("香港在线").closest("tr")!;
    expect(
      within(row)
        .getAllByRole("button")
        .map((button) => button.getAttribute("aria-label") || button.textContent),
    ).toEqual(["检测", "节点操作：香港在线"]);
    menu("香港在线");
    for (const name of ["安装命令", "升级", "编辑节点", "检测状态", "卸载命令", "删除节点"])
      expect(await screen.findByRole("menuitem", { name: new RegExp(`^${name}$`) })).toBeTruthy();
    fireEvent.click(screen.getByRole("menuitem", { name: /^编辑节点$/ }));
    const editing = await screen.findByRole("dialog", { name: "编辑节点" });
    expect(
      (within(editing).getByRole("textbox", { name: /节点名称/ }) as HTMLInputElement).value,
    ).toBe("香港在线");
    fireEvent.click(within(editing).getByRole("button", { name: /^取消$/ }));
    menu("香港在线");
    fireEvent.click(await screen.findByRole("menuitem", { name: /^删除节点$/ }));
    expect(await screen.findByText(/依赖该节点的 2 条隧道/)).toBeTruthy();
    expect(screen.getByRole("textbox", { name: "输入「香港在线」确认" })).toBeTruthy();
    expect(screen.getByRole("button", { name: /^删除节点$/ }).hasAttribute("disabled")).toBe(true);
  });
  it.each([
    ["安装命令", "/node/install"],
    ["卸载命令", "/node/uninstall"],
  ])("桌面列表的%s使用对应节点并仍只在显式复制时写剪贴板", async (name, path) => {
    await listReady();
    menu("香港在线");
    fireEvent.click(await screen.findByRole("menuitem", { name: new RegExp(`^${name}$`) }));
    expect(await screen.findByRole("dialog", { name: `${name} · 香港在线` })).toBeTruthy();
    expect(await screen.findByText("echo protected-node-command")).toBeTruthy();
    expect(post.mock.calls.find(([calledPath]) => calledPath === path)?.[1]).toEqual({
      id: 1,
      forwardMode: "gost",
    });
    expect(copy).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: /^复制命令$/ }));
    await waitFor(() => expect(copy).toHaveBeenCalledWith("echo protected-node-command"));
  });
  it("桌面列表内联检测只请求所选离线节点", async () => {
    await listReady();
    post.mockResolvedValueOnce({
      nodeId: 2,
      name: "日本离线",
      connected: false,
      responsive: false,
      dbStatus: 0,
      forwardMode: "nftables",
      syncState: "failed",
      syncLastError: "等待节点重连",
      checkedAt: Date.now(),
    });
    const row = screen.getByText("日本离线").closest("tr")!;
    fireEvent.click(within(row).getByRole("button", { name: /^检测$/ }));
    expect(await screen.findByRole("dialog", { name: "节点检测 · 日本离线" })).toBeTruthy();
    expect(post).toHaveBeenCalledWith("/node/check-status", { nodeId: 2 }, undefined);
    expect(post.mock.calls.filter(([path]) => path === "/node/check-status")).toHaveLength(1);
  });
  it("桌面列表离线升级禁用并说明原因，不调用升级接口", async () => {
    await listReady();
    menu("日本离线");
    const upgrade = await screen.findByRole("menuitem", { name: /^升级$/ });
    expect(upgrade.getAttribute("aria-disabled")).toBe("true");
    expect(document.getElementById(upgrade.getAttribute("aria-describedby")!)?.textContent).toBe(
      "节点离线，无法远程升级",
    );
    fireEvent.click(upgrade);
    expect(post.mock.calls.some(([path]) => path === "/node/upgrade")).toBe(false);
    expect(screen.queryByRole("alertdialog")).toBeNull();
  });
  it("桌面列表升级确认后保留菜单加载状态并防止重复提交", async () => {
    await listReady();
    menu("香港在线");
    fireEvent.click(await screen.findByRole("menuitem", { name: /^升级$/ }));
    expect(await screen.findByText(/节点会自动重启并短暂离线/)).toBeTruthy();
    let resolve!: (value: unknown) => void;
    post.mockImplementationOnce(
      () =>
        new Promise((done) => {
          resolve = done;
        }),
    );
    fireEvent.click(screen.getByRole("button", { name: /^升级节点$/ }));
    await waitFor(() =>
      expect(post.mock.calls.filter(([path]) => path === "/node/upgrade")).toHaveLength(1),
    );
    expect(post.mock.calls.find(([path]) => path === "/node/upgrade")?.[1]).toEqual({ id: 1 });
    menu("香港在线");
    const pending = await screen.findByRole("menuitem", { name: /^升级$/ });
    expect(pending.getAttribute("aria-disabled")).toBe("true");
    expect(pending.getAttribute("aria-busy")).toBe("true");
    expect(pending.textContent).toContain("升级中");
    fireEvent.click(pending);
    expect(post.mock.calls.filter(([path]) => path === "/node/upgrade")).toHaveLength(1);
    await act(async () => resolve({ nodeId: 1, latestVersion: "1.2.7", message: "已触发" }));
    await waitFor(() => expect(screen.queryByRole("menu")).toBeNull());
    menu("香港在线");
    const available = await screen.findByRole("menuitem", { name: /^升级$/ });
    expect(available.getAttribute("aria-disabled")).toBeNull();
    expect(available.getAttribute("aria-busy")).toBeNull();
    expect(available.textContent).toBe("升级");
  });
  it("手机列表卡片保留安装、升级和检测常用按钮", async () => {
    vi.stubGlobal("matchMedia", () => ({
      matches: true,
      addEventListener() {},
      removeEventListener() {},
    }));
    localStorage.setItem("go-panel-node-view", "list");
    await mount();
    await ready();
    const card = screen.getByRole("article", { name: "节点 香港在线" });
    for (const name of ["安装命令", "升级", "检测", "节点操作：香港在线"])
      expect(within(card).getByRole("button", { name: new RegExp(`^${name}$`) })).toBeTruthy();
  });
});
