import { ConfirmProvider } from "@/components/patterns/confirm-dialog";
import { apiPost } from "@/lib/api/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { SubscriptionPage } from "./page";
import {
  RouterProvider,
  Outlet,
  createRootRoute,
  createRoute,
  createRouter,
  createMemoryHistory,
} from "@tanstack/react-router";
import { subscriptionSearch } from "./search";

vi.mock("@/lib/api/client", async (original) => ({
  ...(await original<typeof import("@/lib/api/client")>()),
  apiPost: vi.fn(),
}));
const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
afterEach(() => {
  cleanup();
  client.clear();
  localStorage.clear();
  vi.resetAllMocks();
  vi.unstubAllGlobals();
});

it("真实订阅页保存后仅重取一次，关闭表单并显示新配置", async () => {
  window.matchMedia = vi
    .fn()
    .mockReturnValue({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() });
  window.scrollTo = vi.fn();
  vi.stubGlobal(
    "ResizeObserver",
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  );
  localStorage.setItem("token", "test-token");
  localStorage.setItem("role_id", "0");
  let saved = false;
  const profile = {
    id: 1,
    name: "新订阅",
    token: "local-test-token",
    defaultFormat: "surge",
    status: 1,
  };
  vi.mocked(apiPost).mockImplementation(async (path) => {
    if (path === "/sub/settings")
      return { profiles: saved ? [profile] : [], nodes: [], profileNodes: [], tunnels: [] };
    if (path === "/sub/profile/create") {
      saved = true;
      return profile;
    }
    return [];
  });
  // 标签化后先在节点标签验证原空态，再切回订阅标签；全部原有请求与保存断言保持不变。
  const root = createRootRoute({ component: Outlet });
  const route = createRoute({
    getParentRoute: () => root,
    path: "/subscription",
    validateSearch: subscriptionSearch,
    component: SubscriptionPage,
  });
  const router = createRouter({
    routeTree: root.addChildren([route]),
    history: createMemoryHistory({ initialEntries: ["/subscription?tab=nodes"] }),
  });
  await act(async () => {
    render(
      <QueryClientProvider client={client}>
        <ConfirmProvider>
          <RouterProvider router={router} />
        </ConfirmProvider>
      </QueryClientProvider>,
    );
  });
  await screen.findByText("暂无节点，先配置脚本上报或导入分享链接");
  expect(vi.mocked(apiPost).mock.calls.filter(([path]) => path === "/sub/settings")).toHaveLength(
    1,
  );
  fireEvent.mouseDown(screen.getByRole("tab", { name: "订阅配置 0" }), {
    button: 0,
    ctrlKey: false,
  });
  await screen.findByText("暂无订阅配置");
  fireEvent.click(screen.getByRole("button", { name: "新建订阅" }));
  fireEvent.change(screen.getByLabelText(/名称/), { target: { value: "新订阅" } });
  fireEvent.click(screen.getByRole("button", { name: "保存" }));
  await screen.findByRole("heading", { name: "新订阅" });
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  expect(
    vi.mocked(apiPost).mock.calls.filter(([path]) => path === "/sub/profile/create"),
  ).toHaveLength(1);
  expect(vi.mocked(apiPost).mock.calls.filter(([path]) => path === "/sub/settings")).toHaveLength(
    2,
  );
});
