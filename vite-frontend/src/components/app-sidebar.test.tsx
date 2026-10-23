import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";

import { checkPanelUpdate, getConfigs, getSystemStatus, getSystemVersion } from "@/lib/api";
import { configureSessionForTest } from "@/lib/session";
import { ApiError } from "@/lib/api/client";
import type { BuildInfo, PanelUpdateStatus } from "@/lib/types";
import { AppSidebar } from "./app-sidebar";

vi.mock("@/lib/api", () => ({
  checkPanelUpdate: vi.fn(),
  getConfigs: vi.fn(),
  getSystemStatus: vi.fn(),
  getSystemVersion: vi.fn(),
}));
vi.mock("@tanstack/react-router", () => ({
  Link: ({ to, children }: { to: string; children: ReactNode }) => <a href={to}>{children}</a>,
  useRouterState: () => "/",
  useNavigate: () => vi.fn(),
}));
vi.mock("@/components/ui/sidebar", () => {
  const Container = ({ children }: { children: ReactNode }) => <div>{children}</div>;
  return {
    Sidebar: Container,
    SidebarContent: Container,
    SidebarFooter: Container,
    SidebarGroup: Container,
    SidebarGroupContent: Container,
    SidebarGroupLabel: Container,
    SidebarHeader: Container,
    SidebarMenu: Container,
    SidebarMenuButton: Container,
    SidebarMenuItem: Container,
    useSidebar: () => ({ state: "expanded" }),
  };
});
vi.mock("@/features/system/components/system-update-dialog", () => ({
  SystemUpdateDialog: ({ open }: { open: boolean }) => open && <div>管理员升级提示</div>,
}));

const build: BuildInfo = {
  version: "v1.2.3",
  commit: "abc1234",
  buildTime: "2026-10-23",
  sourceUrl: "https://example.com",
};
const update: PanelUpdateStatus = {
  current: build,
  enabled: true,
  latestVersion: "v1.2.4",
  updateAvailable: true,
  autoUpdateConfigured: false,
};

let client: QueryClient;
beforeEach(() => {
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  vi.useFakeTimers();
  window.localStorage.clear();
  configureSessionForTest({ storage: window.localStorage, navigate: vi.fn() });
  vi.mocked(getConfigs).mockResolvedValue({ app_name: "任务面板" });
  vi.mocked(getSystemStatus).mockResolvedValue({
    startedAt: Date.now() - 90_000,
    uptimeSeconds: 90,
  });
  vi.mocked(getSystemVersion).mockResolvedValue(build);
  vi.mocked(checkPanelUpdate).mockResolvedValue(update);
});
afterEach(() => {
  cleanup();
  client.clear();
  vi.useRealTimers();
  vi.clearAllMocks();
});

async function showSidebar(role: number, forcePassword = false) {
  window.localStorage.setItem("role_id", String(role));
  window.localStorage.setItem("token", "test-token");
  if (forcePassword) window.localStorage.setItem("must_change_pwd", "1");
  await act(async () => {
    render(
      <QueryClientProvider client={client}>
        <AppSidebar />
      </QueryClientProvider>,
    );
  });
  await act(async () => {
    await vi.advanceTimersByTimeAsync(0);
  });
}

describe("sidebar version and process status", () => {
  it("does not fetch or display version/update information for regular users, including polling", async () => {
    await showSidebar(1);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30 * 60_000);
    });
    expect(getSystemVersion).not.toHaveBeenCalled();
    expect(checkPanelUpdate).not.toHaveBeenCalled();
    expect(getSystemStatus).toHaveBeenCalledOnce();
    expect(screen.queryByText("v1.2.3")).toBeNull();
    expect(screen.queryByText("管理员升级提示")).toBeNull();
    expect(screen.getByText(/面板已运行/)).toBeTruthy();
  });

  it("keeps administrator version, update dialog and half-hour polling", async () => {
    await showSidebar(0);
    expect(getSystemVersion).toHaveBeenCalledOnce();
    expect(checkPanelUpdate).toHaveBeenCalledOnce();
    expect(screen.getByText("v1.2.3")).toBeTruthy();
    expect(screen.getByText("管理员升级提示")).toBeTruthy();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30 * 60_000);
    });
    expect(checkPanelUpdate).toHaveBeenCalledTimes(2);
  });

  it.each([0, 1])(
    "stops all sidebar business calls while role %s must change password",
    async (role) => {
      await showSidebar(role, true);
      expect(getConfigs).not.toHaveBeenCalled();
      expect(getSystemStatus).not.toHaveBeenCalled();
      expect(getSystemVersion).not.toHaveBeenCalled();
      expect(checkPanelUpdate).not.toHaveBeenCalled();
      expect(screen.queryByText("管理员升级提示")).toBeNull();
      expect(screen.queryByText(/面板已运行/)).toBeNull();
    },
  );

  it("keeps a successful status online when site configuration fails", async () => {
    vi.mocked(getConfigs).mockRejectedValue(new Error("config unavailable"));
    await showSidebar(1);
    expect(screen.getByText("系统运行中")).toBeTruthy();
    expect(screen.queryByText("连接异常")).toBeNull();
  });

  it.each(["rejected", "business error"])(
    "shows status failure when status is %s",
    async (failure) => {
      if (failure === "rejected")
        vi.mocked(getSystemStatus).mockRejectedValue(new Error("offline"));
      else vi.mocked(getSystemStatus).mockRejectedValue(new ApiError("offline", -1));
      await showSidebar(1);
      expect(screen.getByText("连接异常")).toBeTruthy();
    },
  );
});
