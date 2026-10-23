import {
  RouterProvider,
  Outlet,
  createRootRoute,
  createRoute,
  createRouter,
  createMemoryHistory,
} from "@tanstack/react-router";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { TooltipProvider } from "@/components/ui/tooltip";
import { ConfirmProvider } from "@/components/patterns/confirm-dialog";
import { configureSessionForTest, setSession } from "@/lib/session";
import { apiPost } from "@/lib/api/client";
import { TunnelPage } from "./page";
import { Route } from "@/routes/_app.tunnel";
vi.mock("@/lib/api/client", async (original) => ({
  ...(await original<typeof import("@/lib/api/client")>()),
  apiPost: vi.fn(),
}));
let client: QueryClient;
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
  setSession({ token: "t", name: "admin", roleId: 0, mustChangePassword: false });
  vi.mocked(apiPost).mockImplementation(async (path) =>
    path === "/tunnel/list"
      ? [
          { id: 7, name: "定位隧道", type: 1, inNodeId: 1, status: 1 },
          { id: 8, name: "禁用隧道", type: 1, inNodeId: 1, status: 0 },
        ]
      : [{ id: 1, name: "节点", status: 1, forwardMode: "gost" }],
  );
});
afterEach(() => {
  cleanup();
  client?.clear();
  vi.unstubAllGlobals();
  localStorage.clear();
});
async function show(url: string) {
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const root = createRootRoute({ component: Outlet });
  const tunnel = createRoute({
    getParentRoute: () => root,
    path: "/tunnel",
    validateSearch: Route.options.validateSearch,
    component: TunnelPage,
  });
  const router = createRouter({
    routeTree: root.addChildren([tunnel]),
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
describe("真实隧道路由与URL状态", () => {
  it.each([
    ["0", "禁用隧道", "定位隧道"],
    ["1", "定位隧道", "禁用隧道"],
  ])("直接打开 status=%s 并重载路由仍保留筛选", async (status, visible, hidden) => {
    const router = await show(`/tunnel?status=${status}`);
    expect(await screen.findByText(visible)).toBeTruthy();
    expect(screen.queryByText(hidden)).toBeNull();
    expect(router.state.location.search.status).toBe(status);
    await act(async () => {
      await router.load();
    });
    expect(router.state.location.search.status).toBe(status);
    expect(screen.getByText(visible)).toBeTruthy();
    expect(screen.queryByText(hidden)).toBeNull();
  });
  it("非法状态明确恢复全部状态", async () => {
    const router = await show("/tunnel?status=9");
    expect(await screen.findByText("定位隧道")).toBeTruthy();
    expect(screen.getByText("禁用隧道")).toBeTruthy();
    expect(router.state.location.search.status).toBeUndefined();
  });

  it("numeric focus 清除冲突筛选并定位高亮真实行", async () => {
    const router = await show("/tunnel?q=missing&type=2&focus=7");
    const name = await screen.findByText("定位隧道");
    await waitFor(() => expect(name.closest("tr")?.getAttribute("data-highlighted")).toBe("true"));
    expect(router.state.location.search).toEqual(
      expect.objectContaining({ focus: 7, q: "", type: "", status: "" }),
    );
    expect(router.state.location.searchStr).toContain("focus=7");
    expect(router.state.location.searchStr).not.toContain("%22");
  });
  it.each(["create=1", "action=create"])("消费 %s 打开表单后清理一次性参数", async (query) => {
    const router = await show(`/tunnel?${query}`);
    expect(await screen.findByRole("dialog", { name: "新建隧道" })).toBeTruthy();
    await waitFor(() => expect(router.state.location.search.create).toBeUndefined());
    expect(router.state.location.search.action).toBeUndefined();
  });
  it("纯数字q安全转换为字符串，非法类型恢复默认列表", async () => {
    const router = await show("/tunnel?q=7&type=999");
    expect(await screen.findByText("定位隧道")).toBeTruthy();
    expect(router.state.location.search.q).toBe("7");
    expect(router.state.location.search.type).toBeUndefined();
  });
});
