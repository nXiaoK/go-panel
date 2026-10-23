import {
  RouterProvider,
  createRootRoute,
  createRouter,
  createMemoryHistory,
} from "@tanstack/react-router";
import { TooltipProvider } from "../src/components/ui/tooltip";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Input } from "../src/components/ui/input";
import { FormField, QueryErrorNotice } from "../src/components/page";
import { ConfirmProvider } from "../src/components/patterns/confirm-dialog";
import { UserPage } from "../src/features/user/page";
import { LimitPage } from "../src/features/limit/page";
import { NodePage } from "../src/features/node/page";
import { TunnelPage } from "../src/features/tunnel/page";
import { ForwardPage } from "../src/features/forward/page";
import { Route as TunnelRoute } from "../src/routes/_app.tunnel";
import { apiPost } from "../src/lib/api/client";
vi.mock("../src/lib/api/client", async (original) => ({
  ...(await original<typeof import("../src/lib/api/client")>()),
  apiPost: vi.fn(),
}));
vi.mock("@tanstack/react-router", async (original) => ({
  ...(await original<typeof import("@tanstack/react-router")>()),
  useNavigate: () => vi.fn(),
}));
let client: QueryClient;
function show(Page: React.ComponentType) {
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <ConfirmProvider>
        {Page === TunnelPage || Page === NodePage ? (
          <TooltipProvider>
            <RouterProvider
              router={createRouter({
                routeTree: createRootRoute({ component: Page }),
                history: createMemoryHistory({ initialEntries: ["/"] }),
              })}
            />
          </TooltipProvider>
        ) : (
          <Page />
        )}
      </ConfirmProvider>
    </QueryClientProvider>,
  );
}
beforeEach(() => {
  window.matchMedia = vi
    .fn()
    .mockReturnValue({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() });
  localStorage.setItem("token", "test-token");
  localStorage.setItem("role_id", "0");
  vi.spyOn(TunnelRoute, "useSearch").mockReturnValue({ action: undefined });
  vi.mocked(apiPost).mockImplementation(async (path) => {
    if (path === "/node/list") return [{ id: 1, name: "入口节点", status: 0, ip: "127.0.0.1" }];
    if (path === "/tunnel/list")
      return [{ id: 2, name: "测试隧道", inNodeId: 1, type: 1, status: 1 }];
    if (path === "/forward/list")
      return [
        {
          id: 3,
          name: "测试转发",
          inPort: 10000,
          remoteAddr: "example.com:443",
          status: 1,
          tunnelId: 2,
        },
      ];
    if (path === "/speed-limit/list") return [{ id: 4, name: "测试限速", tunnelId: 2, speed: 100 }];
    if (path === "/user/list")
      return [
        {
          id: 5,
          user: "alice",
          name: "Alice",
          flow: 100,
          num: 10,
          status: 1,
          inFlow: 0,
          outFlow: 0,
        },
      ];
    return [];
  });
});
afterEach(() => {
  cleanup();
  client?.clear();
  vi.restoreAllMocks();
  localStorage.clear();
});
describe("真实页面的无障碍契约", () => {
  it("真实用户表单的标签关联用户名、昵称、密码、配额字段", async () => {
    show(UserPage);
    await screen.findByText("@alice");
    fireEvent.click(screen.getByRole("button", { name: /新增用户|新建用户/ }));
    for (const name of ["用户名", "昵称", "密码", "流量限制 (GB)", "转发数量"])
      expect(screen.getByLabelText(name)).toBeTruthy();
  });
  it("真实限速表单可按标签操作，行编辑/删除按钮有名称", async () => {
    show(LimitPage);
    expect(await screen.findByRole("button", { name: "编辑限速策略" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "删除限速策略" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "新建策略" }));
    expect(screen.getByLabelText("策略名称")).toBeTruthy();
    expect(screen.getByLabelText("限速速率 (Mbps)")).toBeTruthy();
  });
  it.each([
    [NodePage, "节点操作：入口节点"],
    [TunnelPage, "隧道操作：测试隧道"],
    [ForwardPage, "转发操作：测试转发"],
  ] as const)("真实资源行操作具有可访问名称 %s", async (Page, name) => {
    show(Page);
    expect(await screen.findByRole("button", { name })).toBeTruthy();
  });
  it("真实用户列表操作按钮有名称", async () => {
    show(UserPage);
    expect(await screen.findByRole("button", { name: "用户操作" })).toBeTruthy();
  });
  it("announces query failures and exposes a retry action", () => {
    render(<QueryErrorNotice error={new Error("network unavailable")} onRetry={() => {}} />);
    expect(screen.getByRole("alert").textContent).toContain("network unavailable");
    expect(screen.getByRole("button", { name: "重试" })).toBeTruthy();
  });
  it("connects shared form-field labels to their controls", () => {
    render(
      <FormField label="节点名称" htmlFor="node-name">
        <Input id="node-name" />
      </FormField>,
    );
    expect(screen.getByLabelText("节点名称")).toBeTruthy();
  });
});
