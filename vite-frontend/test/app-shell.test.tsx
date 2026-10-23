import {
  RouterProvider,
  createRootRoute,
  createRoute,
  createRouter,
  createMemoryHistory,
  defaultStringifySearch,
} from "@tanstack/react-router";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AppTopbar } from "@/components/app-topbar";
import { CommandPalette } from "@/features/search/command-palette";
import { LoginPage } from "@/features/auth/login-page";
import { parseLoginSearch } from "@/features/auth/login-search";
import { useUrlState } from "@/hooks/use-url-state";
import { resourceSearch } from "@/features/search/route-search";
import { readSidebarDefaultOpen } from "@/lib/sidebar-state";
import { SidebarProvider, SidebarTrigger, useSidebar } from "@/components/ui/sidebar";
import { requireSession, redirectAuthenticated } from "@/features/auth/guard";
import { Route as LoginRoute } from "@/routes/login";
import { Route as ProfileRoute } from "@/routes/_app.profile";
import { Route as SettingsRoute } from "@/routes/_app.settings";
import { Route as AccountRoute } from "@/routes/_app.account";
import { apiPost } from "@/lib/api/client";
import { login, getConfigByName } from "@/lib/api";
import { configureSessionForTest, setSession } from "@/lib/session";

const navigate = vi.hoisted(() => vi.fn());
vi.mock("@tanstack/react-router", async (original) => ({
  ...(await original<typeof import("@tanstack/react-router")>()),
  useNavigate: () => navigate,
  useRouterState: () => "转发规则",
}));
vi.mock("@/lib/api/client", async (original) => ({
  ...(await original<typeof import("@/lib/api/client")>()),
  apiPost: vi.fn(),
}));
vi.mock("@/lib/api", async (original) => ({
  ...(await original<typeof import("@/lib/api")>()),
  login: vi.fn(),
  getConfigByName: vi.fn(),
}));
let client: QueryClient;
function show(children: ReactNode) {
  return render(<QueryClientProvider client={client}>{children}</QueryClientProvider>);
}
beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  document.cookie = "sidebar_state=; max-age=0; path=/";
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
  configureSessionForTest({ storage: localStorage, navigate: vi.fn() });
  setSession({ token: "t", name: "alice", roleId: 0, mustChangePassword: false });
  client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 30_000 } } });
  vi.spyOn(LoginRoute, "useSearch").mockReturnValue({ reason: "expired" });
  vi.mocked(getConfigByName).mockResolvedValue({
    id: 1,
    name: "app_name",
    value: "测试面板",
    time: 1,
  });
  vi.mocked(apiPost).mockImplementation(async (path) =>
    path === "/forward/list"
      ? [
          {
            id: 231,
            name: "香港转发",
            tunnelName: "hk→jp",
            inPort: 20001,
            remoteAddr: "10.0.0.5:443",
            status: -1,
          },
        ]
      : path === "/node/list"
        ? [{ id: 2, name: "香港节点", status: 1 }]
        : path === "/tunnel/list"
          ? [{ id: 3, name: "香港隧道", status: 1 }]
          : path === "/user/list"
            ? [{ id: 4, name: "香港用户", status: 1 }]
            : [],
  );
});
afterEach(() => {
  cleanup();
  client.clear();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  localStorage.clear();
});

describe("应用外壳与命令面板", () => {
  it("读取持久折叠cookie，切换后重挂载仍保持", () => {
    const State = () => <span>{useSidebar().state}</span>;
    document.cookie = "sidebar_state=false; path=/";
    expect(readSidebarDefaultOpen()).toBe(false);
    const first = show(
      <SidebarProvider defaultOpen={readSidebarDefaultOpen()}>
        <SidebarTrigger />
        <State />
      </SidebarProvider>,
    );
    expect(screen.getByText("collapsed")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "切换侧栏" }));
    expect(document.cookie).toContain("sidebar_state=true");
    first.unmount();
    show(
      <SidebarProvider defaultOpen={readSidebarDefaultOpen()}>
        <State />
      </SidebarProvider>,
    );
    expect(screen.getByText("expanded")).toBeTruthy();
  });
  it("平板未保存cookie默认折叠，桌面默认展开", () => {
    vi.spyOn(window, "innerWidth", "get").mockReturnValue(900);
    expect(readSidebarDefaultOpen()).toBe(false);
    vi.spyOn(window, "innerWidth", "get").mockReturnValue(1440);
    expect(readSidebarDefaultOpen()).toBe(true);
  });
  it("CmdK打开、按类型分组、键盘回车跳转真实focus ID，Esc关闭", async () => {
    show(
      <SidebarProvider>
        <AppTopbar />
      </SidebarProvider>,
    );
    expect(apiPost).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "搜索资源" }).getAttribute("aria-haspopup")).toBe(
      "dialog",
    );
    fireEvent.keyDown(window, { key: "k", metaKey: true });
    const input = await screen.findByRole("combobox", { name: "搜索资源" });
    await screen.findByText("香港转发");
    for (const label of ["节点", "隧道", "转发", "用户", "页面", "操作"])
      expect(screen.getAllByText(label).length).toBeGreaterThan(0);
    fireEvent.change(input, { target: { value: "香港转发" } });
    await waitFor(() => expect(screen.queryByText("香港节点")).toBeNull());
    expect(screen.getByText("异常")).toBeTruthy();
    fireEvent.keyDown(input, { key: "ArrowDown" });
    fireEvent.keyDown(input, { key: "Enter" });
    await waitFor(() =>
      expect(navigate).toHaveBeenCalledWith({ to: "/forward", search: { focus: 231 } }),
    );
    expect(defaultStringifySearch({ focus: 231 })).toBe("?focus=231");
    expect(resourceSearch({ focus: 231 }).focus).toBe(231);
    expect(screen.queryByRole("dialog")).toBeNull();
    fireEvent.keyDown(window, { key: "k", ctrlKey: true });
    await screen.findByRole("dialog");
    fireEvent.keyDown(document.activeElement!, { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });
  it("普通用户仅查询并展示自己的转发，页面与操作也按角色过滤", async () => {
    setSession({ token: "u", name: "viewer", roleId: 1, mustChangePassword: false });
    show(<CommandPalette open onOpenChange={vi.fn()} />);
    await screen.findByText("香港转发");
    expect(vi.mocked(apiPost).mock.calls.map(([path]) => path)).toEqual(["/forward/list"]);
    expect(screen.queryByText("香港节点")).toBeNull();
    expect(screen.queryByText("香港用户")).toBeNull();
    expect(screen.queryByText("新建隧道")).toBeNull();
    fireEvent.click(screen.getByText("新建转发"));
    expect(navigate).toHaveBeenCalledWith({ to: "/forward", search: { create: 1 } });
  });
  it("强制改密禁用命令面板，外壳不触发搜索请求", async () => {
    setSession({ token: "u", name: "viewer", roleId: 1, mustChangePassword: true });
    show(
      <SidebarProvider>
        <AppTopbar />
      </SidebarProvider>,
    );
    expect(screen.getByText("请先修改密码")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "搜索资源" })).toBeNull();
    fireEvent.keyDown(window, { key: "k", ctrlKey: true });
    await act(async () => {
      await Promise.resolve();
    });
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(apiPost).not.toHaveBeenCalled();
  });
  it("路由实际导航保留数字focus URL，空分组隐藏且无匹配有空状态", async () => {
    function SafeUrlReader() {
      const { state } = useUrlState({
        q: "",
        status: "all",
        focus: undefined as number | undefined,
      });
      return (
        <p data-testid="safe-url-state">{`${(state.q ?? "").trim()}|${state.status ?? "all"}|${state.focus == null ? "" : String(state.focus)}`}</p>
      );
    }
    const root = createRootRoute();
    const forward = createRoute({
      getParentRoute: () => root,
      path: "/forward",
      validateSearch: resourceSearch,
      component: SafeUrlReader,
    });
    const router = createRouter({
      routeTree: root.addChildren([forward]),
      history: createMemoryHistory({ initialEntries: ["/forward"] }),
    });
    await router.load();
    await router.navigate({ to: "/forward", search: { focus: 231 } });
    expect(router.state.location.href).toBe("/forward?focus=231");
    expect(new URLSearchParams(router.state.location.searchStr).get("focus")).toBe("231");
    for (const invalid of ["abc", 0, { bad: "value" }]) {
      await router.navigate({
        to: "/forward",
        search: { q: 20001, focus: invalid, create: 2, status: 5, sort: {} },
      });
      expect(router.state.matches.at(-1)?.search).toEqual({ q: "20001" });
      expect(router.state.location.href).not.toContain("focus");
      expect(router.state.location.href).not.toContain("NaN");
    }

    const urlView = show(<RouterProvider router={router} />);
    expect(await screen.findByTestId("safe-url-state")).toHaveProperty("textContent", "20001|all|");
    urlView.unmount();
    vi.mocked(apiPost).mockResolvedValue([]);
    show(<CommandPalette open onOpenChange={vi.fn()} />);
    await waitFor(() => expect(screen.queryByText("正在加载资源")).toBeNull());
    expect(screen.queryByRole("group", { name: "节点" })).toBeNull();
    expect(screen.queryByRole("group", { name: "转发" })).toBeNull();
    fireEvent.change(screen.getByRole("combobox", { name: "搜索资源" }), {
      target: { value: "完全没有匹配的资源xyz" },
    });
    expect(await screen.findByText("没有匹配的资源、页面或操作")).toBeTruthy();
    expect(screen.queryByText("页面", { exact: true })).toBeNull();
    expect(screen.queryByText("操作", { exact: true })).toBeNull();
  });
  it("剔除非法已知URL键，但保留其他页面参数与数字端口搜索", () => {
    for (const focus of ["abc", 0, -1, Infinity, 1.2, Number.MAX_SAFE_INTEGER + 1, {}, null]) {
      expect(
        resourceSearch({ q: 20001, status: 1, sort: [], focus, create: 2, action: "create" }),
      ).toEqual({ q: "20001", action: "create" });
    }
  });
  it("资源URL保留既有页面参数，同时规范focus/create", () => {
    expect(
      resourceSearch({
        focus: 231,
        create: "1",
        q: "abc",
        status: "online",
        sort: "name",
        action: "create",
        future: "retained",
      }),
    ).toEqual({
      focus: 231,
      create: 1,
      q: "abc",
      status: "online",
      sort: "name",
      action: "create",
      future: "retained",
    });
  });
});

describe("登录与兼容路由", () => {
  it.each(["constructor", "__proto__", "toString", "unknown"])(
    "未知或继承提示原因%s不会进入UI",
    (reason) => {
      expect(parseLoginSearch({ reason })).toEqual({});
    },
  );
  it("已登录时beforeLoad直接重定向，强制改密进入account安全区", () => {
    expect(() => redirectAuthenticated()).toThrow();
    setSession({ token: "u", name: "viewer", roleId: 1, mustChangePassword: true });
    try {
      requireSession({ location: { pathname: "/forward" } });
    } catch (error) {
      expect(error).toMatchObject({
        options: { to: "/account", search: { tab: "security", forcePassword: 1 } },
      });
    }
    expect(() => requireSession({ location: { pathname: "/account" } })).not.toThrow();
  });
  it("旧资料/设置书签重定向并保留forcePassword", () => {
    const profileSearch = ProfileRoute.options.validateSearch!({ forcePassword: "1" });
    try {
      ProfileRoute.options.beforeLoad!({ search: profileSearch } as never);
    } catch (error) {
      expect(error).toMatchObject({
        options: { to: "/account", search: { tab: "security", forcePassword: 1 }, replace: true },
      });
    }
    expect(() => SettingsRoute.options.beforeLoad!({} as never)).toThrow();
    expect(AccountRoute.options.validateSearch!({})).toEqual({ tab: "security" });
  });
  it("公开品牌/过期提示、失败内联错误和密码回焦，移除地址入口", async () => {
    vi.mocked(login).mockRejectedValue(new Error("账号或密码错误"));
    show(<LoginPage />);
    await screen.findByText("测试面板");
    expect(screen.getByText("登录已失效，请重新登录")).toBeTruthy();
    expect(getConfigByName).toHaveBeenCalledWith("app_name", { signal: expect.any(AbortSignal) });
    expect(screen.queryByText(/面板地址/)).toBeNull();
    fireEvent.change(screen.getByLabelText("用户名"), { target: { value: "alice" } });
    const password = screen.getByLabelText("密码");
    expect(password.getAttribute("autocomplete")).toBe("current-password");
    fireEvent.change(password, { target: { value: "wrong" } });
    fireEvent.click(screen.getByRole("button", { name: "显示密码" }));
    expect(password.getAttribute("type")).toBe("text");
    fireEvent.click(screen.getByRole("button", { name: "隐藏密码" }));
    fireEvent.click(screen.getByRole("button", { name: "登录" }));
    await screen.findByRole("alert");
    expect(screen.getByRole("alert").textContent).toContain("账号或密码错误");
    await waitFor(() => expect(document.activeElement).toBe(password));
  });
  it("提交中禁止重复请求，登录要求改密后跳转新账户路径", async () => {
    let resolve!: (value: Awaited<ReturnType<typeof login>>) => void;
    vi.mocked(login).mockImplementation(
      () =>
        new Promise((done) => {
          resolve = done;
        }),
    );
    show(<LoginPage />);
    fireEvent.change(screen.getByLabelText("用户名"), { target: { value: "alice" } });
    fireEvent.change(screen.getByLabelText("密码"), { target: { value: "password" } });
    const button = screen.getByRole("button", { name: "登录" });
    fireEvent.click(button);
    fireEvent.click(button);
    expect(login).toHaveBeenCalledOnce();
    expect(button.hasAttribute("disabled")).toBe(true);
    await act(async () => {
      resolve({ token: "new", role_id: 1, name: "alice", requirePasswordChange: true });
    });
    expect(navigate).toHaveBeenCalledWith({
      to: "/account",
      search: { tab: "security", forcePassword: 1 },
    });
  });
});

it("改密原因在登录页展示重新登录提示", async () => {
  vi.mocked(LoginRoute.useSearch).mockReturnValue({ reason: "password-changed" });
  show(<LoginPage />);
  expect(await screen.findByRole("status")).toHaveProperty(
    "textContent",
    "登录信息已更新，请重新登录",
  );
});
