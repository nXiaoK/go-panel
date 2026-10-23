import type { ComponentType } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { clearSession, configureSessionForTest, readSession } from "../src/lib/session";
import { Route as AppRoute } from "../src/routes/_app";
import { Route as ProfileRoute } from "../src/routes/_app.profile";

const api = vi.hoisted(() => ({ package: vi.fn(), password: vi.fn() }));
vi.mock("../src/lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../src/lib/api")>()),
  getUserPackageInfo: api.package,
  updatePassword: api.password,
}));
vi.mock("@/components/app-sidebar", () => ({ AppSidebar: () => null }));
vi.mock("@/components/app-topbar", () => ({ AppTopbar: () => null }));
// 资料页只需导航回调；路由守卫直接执行真实 beforeLoad。
vi.mock("@tanstack/react-router", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@tanstack/react-router")>()),
  createFileRoute: () => (options: unknown) => ({ options, useSearch: () => ({}) }),
  useNavigate: () => vi.fn(),
  Outlet: () => <div>业务页面已挂载</div>,
}));

beforeEach(() => {
  window.matchMedia = vi
    .fn()
    .mockReturnValue({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() });
  localStorage.clear();
  localStorage.setItem("token", "secret");
  localStorage.setItem("role_id", "1");
  configureSessionForTest({ storage: localStorage, navigate: vi.fn() });
  api.password.mockReset().mockResolvedValue("ok");
  api.package
    .mockReset()
    .mockResolvedValue({ userInfo: { flow: 10, num: 5, inFlow: 1024 ** 3, outFlow: 0 } });
});
afterEach(cleanup);

describe("forced password route", () => {
  it("redirects a refreshed session from /forward to the forced profile route", () => {
    localStorage.setItem("must_change_pwd", "1");
    let failure: unknown;
    try {
      AppRoute.options.beforeLoad?.({ location: { pathname: "/forward" } } as never);
    } catch (error) {
      failure = error;
    }
    expect(failure).toMatchObject({ options: { to: "/profile", search: { forcePassword: 1 } } });
    expect(() =>
      AppRoute.options.beforeLoad?.({ location: { pathname: "/profile" } } as never),
    ).not.toThrow();
  });

  it("shows the requirement and never fetches a package in forced mode", async () => {
    localStorage.setItem("must_change_pwd", "1");
    const Profile = ProfileRoute.options.component as ComponentType;
    render(
      <QueryClientProvider client={new QueryClient()}>
        <Profile />
      </QueryClientProvider>,
    );
    expect(screen.getByRole("alert").textContent).toContain("请先修改初始密码");
    expect(screen.queryByText("流量配额")).toBeNull();
    await waitFor(() => expect(screen.getByLabelText("新密码")).toBeTruthy());
    expect(api.package).not.toHaveBeenCalled();
  });

  it("keeps the package available after a normal login", async () => {
    const Profile = ProfileRoute.options.component as ComponentType;
    render(
      <QueryClientProvider client={new QueryClient()}>
        <Profile />
      </QueryClientProvider>,
    );
    await waitFor(() => expect(screen.getByText("流量配额")).toBeTruthy());
    expect(api.package).toHaveBeenCalledOnce();
  });
  it("改密成功清除会话后、延迟导航前不重新请求套餐", async () => {
    localStorage.setItem("must_change_pwd", "1");
    localStorage.setItem("name", "alice");
    const Profile = ProfileRoute.options.component as ComponentType;
    const client = new QueryClient();
    render(
      <QueryClientProvider client={client}>
        <Profile />
      </QueryClientProvider>,
    );
    fireEvent.change(screen.getByLabelText("原密码"), { target: { value: "Old-Password!" } });
    fireEvent.change(screen.getByLabelText("新密码"), { target: { value: "New-Password!" } });
    fireEvent.change(screen.getByLabelText("确认新密码"), { target: { value: "New-Password!" } });
    fireEvent.click(screen.getByRole("button", { name: "更新密码" }));
    await waitFor(() => expect(api.password).toHaveBeenCalledOnce());
    await waitFor(() => expect(readSession().token).toBeNull());
    expect(readSession().mustChangePassword).toBe(false);
    expect(api.package).not.toHaveBeenCalled();
    expect(
      client
        .getQueryCache()
        .getAll()
        .every((query) => query.state.fetchStatus === "idle"),
    ).toBe(true);
  });
  it("退出时立即卸载应用业务树，无需等待导航完成", () => {
    const App = AppRoute.options.component as ComponentType;
    render(<App />);
    expect(screen.getByText("业务页面已挂载")).toBeTruthy();
    act(() => clearSession());
    expect(screen.queryByText("业务页面已挂载")).toBeNull();
  });
});
