import "@testing-library/jest-dom/vitest";
import { ConfirmProvider } from "@/components/patterns/confirm-dialog";
import { TooltipProvider } from "@/components/ui/tooltip";
import { apiPost } from "@/lib/api/client";
import { configureSessionForTest, setSession } from "@/lib/session";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  RouterProvider,
  Outlet,
  createRootRoute,
  createRoute,
  createRouter,
  createMemoryHistory,
} from "@tanstack/react-router";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ProxyNode, SubscriptionSettings } from "@/lib/types";
import { SubscriptionPage } from "./page";
import { subscriptionSearch } from "./search";
import { SubscriptionQrDialog } from "./components/qr-dialog";
vi.mock("@/lib/api/client", async (original) => ({
  ...(await original<typeof import("@/lib/api/client")>()),
  apiPost: vi.fn(),
}));
let client: QueryClient;
const profile = {
  id: 1,
  name: "家庭订阅",
  token: "profile-token-secret",
  surgeTemplate: "",
  clashTemplate: "",
  singboxTemplate: "",
  defaultFormat: "surge" as const,
  status: 1,
};
const baseNode: ProxyNode = {
  id: 1,
  name: "东京节点",
  protocol: "vless",
  server: "tokyo.example",
  port: 443,
  allowInsecure: 0,
  udp: 1,
  sort: 0,
  status: 1,
  lastReportTime: Date.now() - 60000,
};
let settings: SubscriptionSettings;
beforeEach(() => {
  vi.clearAllMocks();
  window.matchMedia = vi
    .fn()
    .mockReturnValue({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() });
  vi.stubGlobal(
    "ResizeObserver",
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  );
  HTMLElement.prototype.scrollIntoView = vi.fn();
  window.scrollTo = vi.fn();
  configureSessionForTest({ storage: localStorage, navigate: vi.fn() });
  setSession({ token: "test-token", name: "admin", roleId: 0, mustChangePassword: false });
  settings = {
    apiKey: "abcd-full-secret-wxyz",
    profiles: [profile],
    nodes: [baseNode],
    profileNodes: [{ subscriptionId: 1, proxyNodeId: 1, sort: 0 }],
    tunnels: [],
    vlessBootstrapScript: "curl https://local/report -H key:abcd",
  };
  vi.mocked(apiPost).mockImplementation(async (path) => (path === "/sub/settings" ? settings : []));
});
afterEach(() => {
  cleanup();
  client?.clear();
  localStorage.clear();
  vi.unstubAllGlobals();
});
async function show(url = "/subscription") {
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const root = createRootRoute({ component: Outlet });
  const route = createRoute({
    getParentRoute: () => root,
    path: "/subscription",
    validateSearch: subscriptionSearch,
    component: SubscriptionPage,
  });
  const router = createRouter({
    routeTree: root.addChildren([route]),
    history: createMemoryHistory({ initialEntries: [url] }),
  });
  await act(async () => {
    render(
      <QueryClientProvider client={client}>
        <TooltipProvider>
          <ConfirmProvider>
            <RouterProvider router={router} />
          </ConfirmProvider>
        </TooltipProvider>
      </QueryClientProvider>,
    );
  });
  return router;
}
async function menu(name: string) {
  fireEvent.pointerDown(await screen.findByRole("button", { name: `${name} 更多操作` }), {
    button: 0,
    ctrlKey: false,
  });
  return await screen.findByRole("menu");
}
const calls = (path: string) => vi.mocked(apiPost).mock.calls.filter(([url]) => url === path);

describe("订阅重构真实页面", () => {
  it("订阅单行链接只提供一次复制并保留打开与二维码标签", async () => {
    await show();
    expect(await screen.findAllByRole("button", { name: "复制 家庭订阅 订阅链接" })).toHaveLength(
      1,
    );
    const open = screen.getByRole("link", { name: "打开 家庭订阅 订阅链接" });
    expect(open.getAttribute("href")).toContain("/api/v1/sub/render/profile-token-secret/surge");
    expect(screen.getByRole("button", { name: "显示 家庭订阅 订阅二维码" })).toBeInTheDocument();
    expect(screen.getByText(open.getAttribute("href")!)).toHaveAttribute(
      "title",
      open.getAttribute("href"),
    );
  });
  it("更换 Token 取消不写入，确认后仅调用一次", async () => {
    await show();
    fireEvent.click(within(await menu("家庭订阅")).getByRole("menuitem", { name: "更换 Token" }));
    expect(await screen.findByRole("alertdialog")).toHaveTextContent("旧链接会立即失效");
    expect(calls("/sub/profile/token")).toHaveLength(0);
    fireEvent.click(screen.getByRole("button", { name: "取消" }));
    expect(calls("/sub/profile/token")).toHaveLength(0);
    fireEvent.click(within(await menu("家庭订阅")).getByRole("menuitem", { name: "更换 Token" }));
    fireEvent.click(screen.getByRole("button", { name: "更换 Token" }));
    await waitFor(() => expect(calls("/sub/profile/token")).toHaveLength(1));
    expect(calls("/sub/profile/token")[0][1]).toEqual({ id: 1 });
  });
  it("API Key 默认打码，随机及自定义更换都必须确认", async () => {
    await show();
    fireEvent.click(screen.getByRole("button", { name: "API Key" }));
    const input = screen.getByLabelText("API Key") as HTMLInputElement;
    expect(input.value).toBe("abcd••••••••wxyz");
    expect(input.value).not.toContain("full-secret");
    fireEvent.click(screen.getByRole("button", { name: "随机生成" }));
    const requestConfirm = await screen.findByRole("alertdialog");
    expect(requestConfirm).toHaveTextContent("已部署的上报脚本需要更新");
    fireEvent.click(within(requestConfirm).getByRole("button", { name: "取消" }));
    expect(calls("/sub/api-key")).toHaveLength(0);
    await waitFor(() => expect(screen.getByRole("button", { name: "显示" })).toBeEnabled());
    fireEvent.click(screen.getByRole("button", { name: "显示" }));
    expect(input.value).toBe(settings.apiKey);
    fireEvent.change(input, { target: { value: "new-custom-key" } });
    fireEvent.click(screen.getByRole("button", { name: "保存" }));
    const customConfirmation = await screen.findByRole("alertdialog");
    fireEvent.click(within(customConfirmation).getByRole("button", { name: "取消" }));
    expect(calls("/sub/api-key")).toHaveLength(0);
    await waitFor(() => expect(screen.getByRole("button", { name: "保存" })).toBeEnabled());
    fireEvent.click(screen.getByRole("button", { name: "保存" }));
    fireEvent.click(await screen.findByRole("button", { name: "更换 API Key" }));
    await waitFor(() => expect(calls("/sub/api-key")).toHaveLength(1));
    expect(calls("/sub/api-key")[0][1]).toEqual({ apiKey: "new-custom-key" });
  });
  it("确认随机轮换发送空值由服务端生成", async () => {
    await show();
    fireEvent.click(screen.getByRole("button", { name: "API Key" }));
    fireEvent.click(screen.getByRole("button", { name: "随机生成" }));
    fireEvent.click(await screen.findByRole("button", { name: "更换 API Key" }));
    await waitFor(() => expect(calls("/sub/api-key")).toHaveLength(1));
    expect(calls("/sub/api-key")[0][1]).toEqual({ apiKey: "" });
  });
  it("删除中转节点默认不勾选，勾选后发送 deleteForward=true", async () => {
    settings.nodes = [{ ...baseNode, forwardId: 9, forwardName: "东京中转" }];
    await show("/subscription?tab=nodes");
    fireEvent.click(within(await menu("东京节点")).getByRole("menuitem", { name: "删除节点" }));
    const dialog = await screen.findByRole("dialog");
    const checkbox = within(dialog).getByRole("checkbox");
    expect(checkbox).toHaveAttribute("data-state", "unchecked");
    expect(dialog).toHaveTextContent("关联转发规则将保留");
    expect(within(dialog).getByRole("button", { name: "删除节点" })).toBeDisabled();
    fireEvent.change(within(dialog).getByRole("textbox", { name: /输入节点名称/ }), {
      target: { value: "东京节点" },
    });
    fireEvent.click(checkbox);
    fireEvent.click(within(dialog).getByRole("button", { name: "删除节点" }));
    await waitFor(() => expect(calls("/sub/node/delete")).toHaveLength(1));
    expect(calls("/sub/node/delete")[0][1]).toEqual({ id: 1, deleteForward: true });
  });
  it("来源节点未勾选时阻止提交并说明后端拒绝删除", async () => {
    settings.nodes = [{ ...baseNode, relayChildCount: 2 }];
    await show("/subscription?tab=nodes");
    fireEvent.click(within(await menu("东京节点")).getByRole("menuitem", { name: "删除节点" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("checkbox")).toHaveAttribute("data-state", "unchecked");
    expect(within(dialog).getByRole("button", { name: "删除节点" })).toBeDisabled();
    expect(dialog).toHaveTextContent("未勾选时后端会拒绝删除");
    expect(calls("/sub/node/delete")).toHaveLength(0);
  });
  it("无中转不显示复选框，删除发送 false", async () => {
    await show("/subscription?tab=nodes");
    fireEvent.click(within(await menu("东京节点")).getByRole("menuitem", { name: "删除节点" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).queryByRole("checkbox")).toBeNull();
    expect(within(dialog).getByRole("button", { name: "删除节点" })).toBeDisabled();
    fireEvent.change(within(dialog).getByRole("textbox", { name: /输入节点名称/ }), {
      target: { value: "东京节点" },
    });
    fireEvent.click(within(dialog).getByRole("button", { name: "删除节点" }));
    await waitFor(() => expect(calls("/sub/node/delete")).toHaveLength(1));
    expect(calls("/sub/node/delete")[0][1]).toEqual({ id: 1, deleteForward: false });
  });
  it("URL 还原 tab/数字状态，清除筛选保留书签", async () => {
    const router = await show("/subscription?tab=nodes&status=0&q=missing&bookmark=keep");
    expect(await screen.findByText("没有匹配的结果")).toBeInTheDocument();
    expect(router.state.location.search.status).toBe("0");
    fireEvent.click(screen.getByRole("button", { name: "清除筛选" }));
    await screen.findByText("东京节点");
    expect(router.state.location.search).toMatchObject({
      tab: "nodes",
      q: "",
      protocol: "",
      status: "",
      relay: "",
      bookmark: "keep",
    });
    await act(async () => {
      await router.load();
    });
    expect(screen.getByRole("tab", { name: "协议节点 1" })).toHaveAttribute("data-state", "active");
  });
  it("上报 URL 直接显示密钥风险说明与可复制脚本", async () => {
    await show("/subscription?tab=report");
    expect(await screen.findByText(/上报脚本中包含 API Key/)).toBeInTheDocument();
    expect(screen.getByText(settings.vlessBootstrapScript)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "复制脚本" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "家庭订阅" })).toBeNull();
  });
  it("绑定 Dialog 保留预选并提交用户修改的选择", async () => {
    settings.profiles = [profile, { ...profile, id: 2, name: "备用订阅" }];
    await show("/subscription?tab=nodes");
    fireEvent.click(within(await menu("东京节点")).getByRole("menuitem", { name: "绑定订阅" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("checkbox", { name: "选择行 1" })).toHaveAttribute(
      "data-state",
      "checked",
    );
    fireEvent.click(within(dialog).getByRole("checkbox", { name: "选择行 2" }));
    fireEvent.click(within(dialog).getByRole("button", { name: "保存" }));
    await waitFor(() => expect(calls("/sub/node/assign-profiles")).toHaveLength(1));
    expect(calls("/sub/node/assign-profiles")[0][1]).toEqual({ nodeId: 1, profileIds: [1, 2] });
  });
  it("导入解析失败为关联字段的内联错误", async () => {
    const original = vi.mocked(apiPost).getMockImplementation()!;
    vi.mocked(apiPost).mockImplementation(async (path, ...args) => {
      if (path === "/sub/node/import") throw new Error("无法解析分享链接");
      return original(path, ...args);
    });
    await show();
    fireEvent.click(screen.getByRole("button", { name: "导入节点" }));
    const input = screen.getByRole("textbox", { name: /分享链接/ });
    fireEvent.change(input, { target: { value: "invalid-link" } });
    fireEvent.click(screen.getByRole("button", { name: "导入" }));
    await screen.findByText("无法解析分享链接");
    expect(input).toHaveAttribute("aria-invalid", "true");
    expect(input).toHaveAccessibleDescription("无法解析分享链接");
  });
  it("中转诊断使用共享抽屉显示全部检查项", async () => {
    settings.nodes = [{ ...baseNode, forwardId: 9 }];
    vi.mocked(apiPost).mockImplementation(async (path) =>
      path === "/sub/settings"
        ? settings
        : path === "/forward/diagnose"
          ? {
              results: [
                {
                  nodeId: 1,
                  nodeName: "入口",
                  description: "入口检查",
                  targetIp: "127.0.0.1",
                  success: true,
                  averageTime: 2,
                },
                {
                  nodeId: 2,
                  nodeName: "出口",
                  description: "出口检查",
                  targetIp: "127.0.0.2",
                  success: false,
                  message: "无法连接",
                },
              ],
            }
          : [],
    );
    await show("/subscription?tab=nodes");
    fireEvent.click(within(await menu("东京节点")).getByRole("menuitem", { name: "测试中转" }));
    expect(await screen.findByRole("heading", { name: "中转诊断 · 东京节点" })).toBeInTheDocument();
    expect(await screen.findByText("入口检查")).toBeInTheDocument();
    expect(screen.getByText("出口检查")).toBeInTheDocument();
    expect(screen.getByText("无法连接")).toBeInTheDocument();
  });
});

it("二维码有白衬底、格式名和真实 PNG 下载", async () => {
  render(
    <SubscriptionQrDialog
      open
      title="家庭订阅 · Surge"
      value="https://panel.example/sub/token"
      onOpenChange={vi.fn()}
    />,
  );
  const image = await screen.findByRole("img", { name: "订阅二维码" });
  const src = image.getAttribute("src")!;
  expect(src).toMatch(/^data:image\/png;base64,iVBORw0KGgo/);
  expect(image.parentElement).toHaveStyle({ backgroundColor: "#ffffff" });
  const link = screen.getByRole("link", { name: "下载 PNG" });
  expect(link).toHaveAttribute("href", src);
  expect(link.getAttribute("download")).toMatch(/Surge\.png$/);
  expect(screen.getAllByText("家庭订阅 · Surge")).toHaveLength(2);
});

it("批量启用走共享队列，六个请求最多同时四个", async () => {
  settings.profiles = Array.from({ length: 6 }, (_, index) => ({
    ...profile,
    id: index + 1,
    name: `订阅${index + 1}`,
    status: 0,
  }));
  let active = 0,
    peak = 0,
    started = 0;
  const releases: Array<() => void> = [];
  vi.mocked(apiPost).mockImplementation(async (path) => {
    if (path === "/sub/settings") return settings;
    if (path === "/sub/profile/update") {
      started++;
      active++;
      peak = Math.max(peak, active);
      await new Promise<void>((resolve) =>
        releases.push(() => {
          active--;
          resolve();
        }),
      );
      return profile;
    }
    return [];
  });
  await show();
  fireEvent.click(await screen.findByRole("checkbox", { name: "选择全部订阅" }));
  fireEvent.click(screen.getByRole("button", { name: "批量启用" }));
  fireEvent.click(
    within(await screen.findByRole("alertdialog")).getByRole("button", { name: "批量启用订阅" }),
  );
  await waitFor(() => expect(started).toBe(4));
  expect(active).toBe(4);
  await act(async () => {
    releases.shift()!();
  });
  await waitFor(() => expect(started).toBe(5));
  expect(active).toBe(4);
  await act(async () => {
    releases.shift()!();
  });
  await waitFor(() => expect(started).toBe(6));
  await act(async () => {
    releases.splice(0).forEach((release) => release());
  });
  await screen.findByRole("heading", { name: "批量操作结果" });
  expect(peak).toBe(4);
  expect(calls("/sub/profile/update")).toHaveLength(6);
  expect(active).toBe(0);
});

it("中转抽屉以表格显示预览并保存当前模式与端口", async () => {
  settings.tunnels = [{ id: 8, name: "本地隧道", inIp: "127.0.0.1", type: 1, status: 1 }];
  vi.mocked(apiPost).mockImplementation(async (path) =>
    path === "/sub/settings"
      ? settings
      : path === "/sub/node/relay/preview"
        ? {
            nodeId: 1,
            nodeName: "东京节点",
            tunnelId: 8,
            tunnelName: "本地隧道",
            tunnelType: 1,
            tunnelTypeName: "单节点",
            entry: { nodeName: "入口节点", address: "127.0.0.1:4000" },
            target: { address: "tokyo.example:443" },
          }
        : [],
  );
  await show("/subscription?tab=nodes");
  fireEvent.click(within(await menu("东京节点")).getByRole("menuitem", { name: "配置中转" }));
  expect(await screen.findByRole("heading", { name: "创建/修改中转" })).toBeInTheDocument();
  expect(await screen.findByRole("table", { name: "中转预览" })).toHaveTextContent(
    "127.0.0.1:4000",
  );
  expect(screen.getByRole("table", { name: "中转预览" })).toHaveTextContent("tokyo.example:443");
  const port = screen.getByRole("spinbutton", { name: /入口端口/ });
  fireEvent.change(port, { target: { value: "0" } });
  fireEvent.click(screen.getByRole("button", { name: "保存中转" }));
  expect(port).toHaveAttribute("aria-invalid", "true");
  expect(calls("/sub/node/relay")).toHaveLength(0);
  fireEvent.change(port, { target: { value: "4100" } });
  await waitFor(() => expect(screen.getByRole("button", { name: "保存中转" })).toBeEnabled());
  fireEvent.click(screen.getByRole("button", { name: "保存中转" }));
  await waitFor(() => expect(calls("/sub/node/relay")).toHaveLength(1));
  expect(calls("/sub/node/relay")[0][1]).toMatchObject({
    nodeId: 1,
    tunnelId: 8,
    mode: "replace",
    inPort: 4100,
  });
});

it("编辑节点使用脏表单保护，放弃后重新打开还原原值", async () => {
  await show("/subscription?tab=nodes");
  fireEvent.click(await screen.findByRole("button", { name: "编辑节点 东京节点" }));
  fireEvent.change(screen.getByRole("textbox", { name: /名称/ }), {
    target: { value: "未保存节点" },
  });
  fireEvent.click(screen.getByRole("button", { name: "取消" }));
  expect(await screen.findByRole("alertdialog")).toHaveTextContent("本次修改将不会保存");
  fireEvent.click(screen.getByRole("button", { name: "放弃修改" }));
  fireEvent.click(await screen.findByRole("button", { name: "编辑节点 东京节点" }));
  expect(screen.getByRole("textbox", { name: /名称/ })).toHaveValue("东京节点");
  expect(calls("/sub/node/update")).toHaveLength(0);
});

it("混合节点批量删除保持原语义，仅关联中转传 true", async () => {
  settings.nodes = [
    { ...baseNode, forwardId: 9 },
    { ...baseNode, id: 2, name: "直连节点" },
  ];
  await show("/subscription?tab=nodes");
  fireEvent.click(await screen.findByRole("checkbox", { name: "选择全部" }));
  fireEvent.click(screen.getByRole("button", { name: "批量删除" }));
  const dialog = await screen.findByRole("alertdialog");
  expect(dialog).toHaveTextContent("其中 1 个存在关联中转，将一并删除中转规则和中转节点");
  expect(calls("/sub/node/delete")).toHaveLength(0);
  fireEvent.click(within(dialog).getByRole("button", { name: "删除节点" }));
  await waitFor(() => expect(calls("/sub/node/delete")).toHaveLength(2));
  expect(calls("/sub/node/delete").map(([, payload]) => payload)).toEqual([
    { id: 1, deleteForward: true },
    { id: 2, deleteForward: false },
  ]);
});

it("URL 数字搜索词仍用于节点地址筛选", async () => {
  settings.nodes = [
    { ...baseNode, name: "目标节点", server: "203.0.113.80", port: 443 },
    { ...baseNode, id: 2, name: "其他节点", server: "other.example", port: 80 },
  ];
  const router = await show("/subscription?tab=nodes&q=443");
  expect(await screen.findByText("目标节点")).toBeInTheDocument();
  expect(screen.queryByText("其他节点")).toBeNull();
  expect(router.state.location.search.q).toBe("443");
});

it("初次查询失败只显示重试错误，不混入空数据状态", async () => {
  vi.mocked(apiPost).mockRejectedValue(new Error("后端暂不可用"));
  await show("/subscription?tab=nodes");
  expect(await screen.findByRole("alert")).toHaveTextContent("后端暂不可用");
  expect(screen.queryByText("暂无节点")).toBeNull();
  expect(screen.queryByText("暂无订阅配置")).toBeNull();
  expect(screen.getByRole("button", { name: "重试" })).toBeInTheDocument();
});
