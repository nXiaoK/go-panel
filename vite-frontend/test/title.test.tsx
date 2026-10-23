import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { QueryClient } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Route as RootRoute } from "@/routes/__root";
import { getCachedAppName, setCachedAppName } from "@/lib/site-config";
import { configureSessionForTest, setSession, clearSession } from "@/lib/session";
import { forwardKeys } from "@/features/forward/queries";
import { buildGlobalSearchItems } from "@/features/search/model";

beforeEach(() => {
  window.scrollTo = vi.fn();
  localStorage.clear();
  configureSessionForTest({ storage: localStorage, navigate: vi.fn() });
  window.matchMedia = vi
    .fn()
    .mockReturnValue({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() });
});
afterEach(() => {
  cleanup();
});
async function showApp() {
  const first = createRoute({
    getParentRoute: () => RootRoute,
    path: "/forward",
    head: () => ({ meta: [{ title: "转发规则" }, { name: "description", content: "转发说明" }] }),
    component: () => <div>转发内容</div>,
  });
  const second = createRoute({
    getParentRoute: () => RootRoute,
    path: "/node",
    head: () => ({ meta: [{ title: "节点管理" }, { name: "description", content: "节点说明" }] }),
    component: () => <div>节点内容</div>,
  });
  const queryClient = new QueryClient();
  const router = createRouter({
    routeTree: RootRoute.addChildren([first, second]),
    history: createMemoryHistory({ initialEntries: ["/forward"] }),
    context: { queryClient },
  });
  await act(async () => {
    await router.load();
    render(<RouterProvider router={router} />);
  });
  await screen.findByText("转发内容");
  return { router, queryClient };
}
describe("路由标题与会话缓存", () => {
  it("真实路由变化和HeadContent提交后仍保留自定义品牌，配置更新立即生效", async () => {
    setCachedAppName("我的面板");
    const { router } = await showApp();
    await waitFor(() => expect(document.title).toBe("转发规则 · 我的面板"));
    expect(document.querySelector('meta[name="description"]')?.getAttribute("content")).toBe(
      "转发说明",
    );
    await act(async () => {
      await router.navigate({ to: "/node" });
    });
    await screen.findByText("节点内容");
    await waitFor(() => expect(document.title).toBe("节点管理 · 我的面板"));
    expect(document.querySelector('meta[name="description"]')?.getAttribute("content")).toBe(
      "节点说明",
    );
    act(() => setCachedAppName("运维站"));
    expect(document.title).toBe("节点管理 · 运维站");
    // 模拟HeadContent迟到写入/替换title节点；唯一标题管理器必须修正竞争结果。
    document.title = "节点管理";
    await waitFor(() => expect(document.title).toBe("节点管理 · 运维站"));
    const replacement = document.createElement("title");
    replacement.textContent = "错误品牌";
    const original = document.head.querySelector("title")!;
    original.replaceWith(replacement);
    await waitFor(() => expect(document.title).toBe("节点管理 · 运维站"));
    replacement.replaceWith(original);
    await waitFor(() => expect(document.title).toBe("节点管理 · 运维站"));
    expect(getCachedAppName()).toBe("运维站");
  });
  it("无配置时默认go-panel，退出和跨标签登录均清理真实QueryClient及搜索缓存", async () => {
    const { queryClient } = await showApp();
    await waitFor(() => expect(document.title).toBe("转发规则 · go-panel"));
    async function seedCaches() {
      queryClient.setQueryData(["private"], "secret");
      queryClient.setQueryData(forwardKeys.list(), [{ id: 1, name: "secret" }]);
      expect(
        buildGlobalSearchItems({ forwards: queryClient.getQueryData(forwardKeys.list()) }),
      ).toHaveLength(1);
    }
    await seedCaches();
    act(() => clearSession());
    expect(queryClient.getQueryData(["private"])).toBeUndefined();
    expect(queryClient.getQueryData(forwardKeys.list())).toBeUndefined();
    expect(
      buildGlobalSearchItems({ forwards: queryClient.getQueryData(forwardKeys.list()) }),
    ).toEqual([]);
    await seedCaches();
    act(() => setSession({ token: "new", roleId: 1, name: "alice", mustChangePassword: false }));
    expect(queryClient.getQueryData(["private"])).toBeUndefined();
    expect(queryClient.getQueryData(forwardKeys.list())).toBeUndefined();
    expect(
      buildGlobalSearchItems({ forwards: queryClient.getQueryData(forwardKeys.list()) }),
    ).toEqual([]);
    await seedCaches();
    act(() => {
      localStorage.setItem("token", "other-tab");
      localStorage.setItem("role_id", "0");
      window.dispatchEvent(
        new StorageEvent("storage", { key: "token", storageArea: localStorage }),
      );
    });
    expect(queryClient.getQueryData(["private"])).toBeUndefined();
    expect(queryClient.getQueryData(forwardKeys.list())).toBeUndefined();
    expect(
      buildGlobalSearchItems({ forwards: queryClient.getQueryData(forwardKeys.list()) }),
    ).toEqual([]);
  });
});
