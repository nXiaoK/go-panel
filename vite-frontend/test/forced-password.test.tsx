import type { ComponentType } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { configureSessionForTest } from "../src/lib/session";
import { Route as AppRoute } from "../src/routes/_app";
import { Route as ProfileRoute } from "../src/routes/_app.profile";

const api = vi.hoisted(() => ({ package: vi.fn() }));
vi.mock("../src/lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../src/lib/api")>()),
  getUserPackageInfo: api.package,
}));
vi.mock("@/components/app-sidebar", () => ({ AppSidebar: () => null }));
vi.mock("@/components/app-topbar", () => ({ AppTopbar: () => null }));
// 资料页只需导航回调；路由守卫直接执行真实 beforeLoad。
vi.mock("@tanstack/react-router", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@tanstack/react-router")>()),
  createFileRoute: () => (options: unknown) => ({ options, useSearch: () => ({}) }),
  useNavigate: () => vi.fn(),
}));

beforeEach(() => {
  localStorage.clear();
  localStorage.setItem("token", "secret");
  localStorage.setItem("role_id", "1");
  configureSessionForTest({ storage: localStorage, navigate: vi.fn() });
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
});
