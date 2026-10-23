import { ConfirmProvider } from "@/components/patterns/confirm-dialog";
import { ForwardPage } from "./page";
import { forwardSearch } from "./search";
import { ApiError, apiPost } from "@/lib/api/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  RouterProvider,
  createRootRoute,
  createRoute,
  createRouter,
  createMemoryHistory,
} from "@tanstack/react-router";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/lib/api/client", async (original) => ({
  ...(await original<typeof import("@/lib/api/client")>()),
  apiPost: vi.fn(),
}));
const post = vi.mocked(apiPost);
const clients: QueryClient[] = [];
beforeEach(() => {
  window.scrollTo = vi.fn();
  window.matchMedia = vi
    .fn()
    .mockReturnValue({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() });
  HTMLElement.prototype.scrollIntoView = vi.fn();
});
afterEach(() => {
  cleanup();
  clients.forEach((client) => client.clear());
  clients.length = 0;
  vi.clearAllMocks();
  localStorage.clear();
});
const rows = [
  {
    id: 231,
    name: "game-a",
    tunnelId: 3,
    inIp: "entry.example.com",
    inPort: 20001,
    remoteAddr: "target.example:443",
    status: -1,
    inx: 1,
  },
  {
    id: 232,
    name: "web-b",
    tunnelId: 3,
    inPort: 20002,
    remoteAddr: "target.example:80",
    status: 1,
    inx: 2,
  },
  {
    id: 233,
    name: "web-c",
    tunnelId: 3,
    inPort: 20003,
    remoteAddr: "target.example:8080",
    status: 1,
    inx: 3,
  },
];
function setupData() {
  post.mockImplementation(async (path) =>
    path === "/forward/list"
      ? rows
      : path === "/tunnel/list" || path === "/tunnel/user/tunnel"
        ? [{ id: 3, name: "hk→jp", type: 1, available: true }]
        : [],
  );
}
function renderPage(role: number, url = "/forward") {
  localStorage.setItem("role_id", String(role));
  localStorage.setItem("token", "test-token");
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  clients.push(client);
  const root = createRootRoute();
  const route = createRoute({
    getParentRoute: () => root,
    path: "/forward",
    validateSearch: forwardSearch,
    component: ForwardPage,
  });
  const router = createRouter({
    routeTree: root.addChildren([route]),
    history: createMemoryHistory({ initialEntries: [url] }),
  });
  render(
    <QueryClientProvider client={client}>
      <ConfirmProvider>
        <RouterProvider router={router} />
      </ConfirmProvider>
    </QueryClientProvider>,
  );
  return { client, router };
}

describe("forward page role requests", () => {
  it("loads only regular-user endpoints and displays the forward entry address", async () => {
    post.mockImplementation(async (path) => (path === "/forward/list" ? [rows[0]] : []));
    renderPage(1);
    await screen.findByText("entry.example.com:20001");
    expect(post.mock.calls.map(([path]) => path).sort()).toEqual([
      "/forward/list",
      "/tunnel/user/tunnel",
    ]);
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.queryByRole("columnheader", { name: "用户" })).toBeNull();
    expect(screen.queryByRole("button", { name: "识别 NFT 规则" })).toBeNull();
  });
  it("retains administrator endpoints from the first render", async () => {
    post.mockResolvedValue([]);
    renderPage(0);
    await waitFor(() => expect(post).toHaveBeenCalledTimes(3));
    expect(post.mock.calls.map(([path]) => path).sort()).toEqual([
      "/forward/list",
      "/node/list",
      "/tunnel/list",
    ]);
    expect(screen.getByText("识别 NFT 规则")).toBeTruthy();
  });
});
describe("forward URL and custom order", () => {
  it("restores filters from a refreshed URL and writes header sort back to it", async () => {
    setupData();
    const { router } = renderPage(0, "/forward?status=error&q=game&tunnel=3");
    await screen.findByText("game-a");
    expect(screen.queryByText("web-b")).toBeNull();
    expect((screen.getByRole("textbox", { name: "搜索转发" }) as HTMLInputElement).value).toBe(
      "game",
    );
    expect(screen.getByRole("combobox", { name: "筛选状态" }).textContent).toBe("异常");
    fireEvent.click(screen.getByRole("button", { name: "名称" }));
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({
        q: "game",
        status: "error",
        tunnel: 3,
        sort: "name.asc",
      }),
    );
  });
  it("clears filters on order mode, moves a row to top, and submits the complete inx sequence once", async () => {
    setupData();
    const { router } = renderPage(0, "/forward?q=game&status=error");
    await screen.findByText("game-a");
    fireEvent.click(screen.getByRole("button", { name: "调整顺序" }));
    await screen.findByText("web-c");
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({
        q: "",
        status: "all",
        tunnel: "all",
        sort: "",
      }),
    );
    fireEvent.click(screen.getByRole("button", { name: "置顶：web-c" }));
    fireEvent.click(screen.getByRole("button", { name: "保存顺序" }));
    await waitFor(() =>
      expect(post.mock.calls.filter(([path]) => path === "/forward/update-order")).toHaveLength(1),
    );
    expect(post.mock.calls.find(([path]) => path === "/forward/update-order")?.[1]).toEqual({
      forwards: [
        { id: 233, inx: 1 },
        { id: 231, inx: 2 },
        { id: 232, inx: 3 },
      ],
    });
  });
  it("rolls back to server order and exposes inline errors when order save fails", async () => {
    setupData();
    const original = post.getMockImplementation()!;
    post.mockImplementation((path, data, options) =>
      path === "/forward/update-order"
        ? Promise.reject(new Error("顺序保存失败"))
        : original(path, data, options),
    );
    renderPage(0);
    await screen.findByText("game-a");
    fireEvent.click(screen.getByRole("button", { name: "调整顺序" }));
    fireEvent.click(screen.getByRole("button", { name: "置顶：web-c" }));
    fireEvent.click(screen.getByRole("button", { name: "保存顺序" }));
    expect((await screen.findByRole("alert")).textContent).toContain("顺序保存失败");
    const displayed = within(screen.getByRole("table", { name: "转发规则" }))
      .getAllByRole("row")
      .slice(1);
    expect(displayed.map((row) => row.textContent?.match(/game-a|web-b|web-c/)?.[0])).toEqual([
      "game-a",
      "web-b",
      "web-c",
    ]);
  });
  it("clears conflicting filters and highlights/scrolls the focused row", async () => {
    setupData();
    renderPage(0, "/forward?focus=231&q=missing&status=0&tunnel=99");
    await waitFor(() =>
      expect(HTMLElement.prototype.scrollIntoView).toHaveBeenCalledWith({
        block: "center",
        behavior: "instant",
      }),
    );
    expect(screen.getByText("game-a").closest("tr")?.getAttribute("data-highlighted")).toBe("true");
    expect((screen.getByRole("textbox", { name: "搜索转发" }) as HTMLInputElement).value).toBe("");
  });
  it("opens and consumes command-palette create parameters", async () => {
    setupData();
    const { router } = renderPage(1, "/forward?create=1");
    expect(await screen.findByRole("dialog", { name: "新建转发" })).toBeTruthy();
    await waitFor(() => expect(router.state.location.search.create).toBeUndefined());
    expect(screen.queryByText("出口模式")).toBeNull();
  });
});
describe("forward batch operations", () => {
  it("summarizes individual failures and refreshes after three pause requests", async () => {
    setupData();
    const original = post.getMockImplementation()!;
    post.mockImplementation((path, data, options) =>
      path === "/forward/pause" && (data as { id: number }).id === 232
        ? Promise.reject(new Error("节点离线"))
        : original(path, data, options),
    );
    renderPage(0);
    await screen.findByText("game-a");
    fireEvent.click(screen.getByRole("checkbox", { name: "选择全部" }));
    fireEvent.click(
      within(screen.getByRole("region", { name: "批量操作" })).getByRole("button", {
        name: "暂停",
      }),
    );
    const summary = await screen.findByRole("dialog", { name: "批量暂停完成" });
    expect(summary.textContent).toContain("成功 2 条，失败 1 条");
    expect(summary.textContent).toContain("web-b #232：节点离线");
    expect(post.mock.calls.filter(([path]) => path === "/forward/pause")).toHaveLength(3);
    expect(
      post.mock.calls.filter(([path]) => path === "/forward/list").length,
    ).toBeGreaterThanOrEqual(2);
  });
});

describe("forward destructive confirmations", () => {
  it("shows the server reason and requires the name before force deletion", async () => {
    setupData();
    const original = post.getMockImplementation()!;
    post.mockImplementation((path, data, options) =>
      path === "/forward/delete"
        ? Promise.reject(new ApiError("入口节点离线，删除未同步", 1, 200))
        : original(path, data, options),
    );
    renderPage(0);
    const trigger = await screen.findByRole("button", { name: "转发操作：game-a" });
    fireEvent.keyDown(trigger, { key: "Enter" });
    fireEvent.click(await screen.findByRole("menuitem", { name: "删除" }));
    fireEvent.click(await screen.findByRole("button", { name: "删除转发" }));
    const forceDialog = await screen.findByRole("alertdialog", { name: "强制删除转发「game-a」" });
    expect(forceDialog.textContent).toContain("入口节点离线，删除未同步");
    expect(forceDialog.textContent).toContain("节点仍可能残留转发规则，需要检查并手动清理");
    const forceButton = within(forceDialog).getByRole("button", { name: "强制删除" });
    expect(forceButton.hasAttribute("disabled")).toBe(true);
    fireEvent.change(within(forceDialog).getByRole("textbox"), { target: { value: "game-a" } });
    fireEvent.click(forceButton);
    await waitFor(() =>
      expect(post.mock.calls.filter(([path]) => path === "/forward/force-delete")).toHaveLength(1),
    );
  });
  it("describes the exact batch deletion count before any request", async () => {
    setupData();
    renderPage(0);
    await screen.findByText("game-a");
    fireEvent.click(screen.getByRole("checkbox", { name: "选择全部" }));
    fireEvent.click(
      within(screen.getByRole("region", { name: "批量操作" })).getByRole("button", {
        name: "删除",
      }),
    );
    const dialog = await screen.findByRole("alertdialog", { name: "删除 3 条转发" });
    expect(dialog.textContent).toContain("将删除 3 条转发，入口端口会立即停止转发");
    expect(post.mock.calls.filter(([path]) => path === "/forward/delete")).toHaveLength(0);
    fireEvent.click(within(dialog).getByRole("button", { name: "取消" }));
    expect(post.mock.calls.filter(([path]) => path === "/forward/delete")).toHaveLength(0);
  });
});
