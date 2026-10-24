import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";

import {
  checkPanelUpdate,
  getConfigs,
  getPanelUpdateStatus,
  getSystemStatus,
  getSystemVersion,
} from "@/lib/api";
import { configureSessionForTest } from "@/lib/session";
import { ApiError } from "@/lib/api/client";
import type { BuildInfo, PanelUpdateStatus } from "@/lib/types";
import { PanelStatusProvider } from "@/features/system/panel-status";
import { panelUpdateStore } from "@/features/system/update-store";
import { AppSidebar } from "./app-sidebar";

vi.mock("@/lib/api", () => ({
  applyPanelUpdate: vi.fn(),
  cancelPanelUpdate: vi.fn(),
  checkPanelUpdate: vi.fn(),
  getConfigs: vi.fn(),
  getPanelUpdateStatus: vi.fn(),
  getSystemStatus: vi.fn(),
  getSystemVersion: vi.fn(),
  rollbackPanel: vi.fn(),
}));
vi.mock("@tanstack/react-router", () => ({
  Link: ({ to, children }: { to: string; children: ReactNode }) => <a href={to}>{children}</a>,
  useRouterState: () => "/",
  useNavigate: () => vi.fn(),
}));
// 移动端侧栏是默认关闭的抽屉，Sidebar 不渲染子元素；用开关模拟该状态。
const sidebarState = vi.hoisted(() => ({ mobileClosed: false }));
vi.mock("@/components/ui/sidebar", () => {
  const Container = ({ children }: { children: ReactNode }) => <div>{children}</div>;
  return {
    Sidebar: ({ children }: { children: ReactNode }) =>
      sidebarState.mobileClosed ? null : <div>{children}</div>,
    SidebarContent: Container,
    SidebarFooter: Container,
    SidebarGroup: Container,
    SidebarGroupContent: Container,
    SidebarGroupLabel: Container,
    SidebarHeader: Container,
    SidebarTrigger: () => <button>切换侧栏</button>,
    SidebarMenu: Container,
    SidebarMenuButton: Container,
    SidebarMenuItem: Container,
    useSidebar: () => ({
      state: "expanded",
      isMobile: sidebarState.mobileClosed,
      setOpenMobile: vi.fn(),
    }),
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
  strategy: "manual",
  softUpdate: { available: false, reason: "not_supervised" },
  runtime: {
    mode: "unsupervised",
    imageTag: "latest",
    rollback: { available: false },
    builtinRestore: { available: false },
  },
};

let client: QueryClient;
beforeEach(() => {
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  vi.useFakeTimers();
  window.localStorage.clear();
  panelUpdateStore.reset();
  configureSessionForTest({ storage: window.localStorage, navigate: vi.fn() });
  vi.mocked(getConfigs).mockResolvedValue({ app_name: "任务面板" });
  vi.mocked(getSystemStatus).mockResolvedValue({
    startedAt: Date.now() - 90_000,
    uptimeSeconds: 90,
  });
  vi.mocked(getSystemVersion).mockResolvedValue(build);
  vi.mocked(checkPanelUpdate).mockResolvedValue(update);
  vi.mocked(getPanelUpdateStatus).mockResolvedValue(update);
});
afterEach(() => {
  cleanup();
  client.clear();
  panelUpdateStore.reset();
  sidebarState.mobileClosed = false;
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.clearAllMocks();
});

async function showSidebar(role: number, forcePassword = false) {
  window.localStorage.setItem("role_id", String(role));
  window.localStorage.setItem("token", "test-token");
  if (forcePassword) window.localStorage.setItem("must_change_pwd", "1");
  await act(async () => {
    render(
      <QueryClientProvider client={client}>
        <PanelStatusProvider>
          <AppSidebar />
        </PanelStatusProvider>
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
    expect(getSystemStatus).not.toHaveBeenCalled();
    expect(screen.queryByText("v1.2.3")).toBeNull();
    expect(screen.queryByText("管理员升级提示")).toBeNull();
    expect(screen.queryByText(/面板已运行/)).toBeNull();
  });

  it("keeps administrator version, update dialog and half-hour polling", async () => {
    await showSidebar(0);
    expect(getSystemVersion).toHaveBeenCalledOnce();
    expect(checkPanelUpdate).toHaveBeenCalledOnce();
    expect(screen.getByText("v1.2.3")).toBeTruthy();
    expect(screen.getByText("管理员升级提示")).toBeTruthy();
    expect(screen.getByRole("button", { name: "可更新，打开面板升级" }).textContent).toContain(
      "可更新",
    );
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30 * 60_000);
    });
    expect(checkPanelUpdate).toHaveBeenCalledTimes(2);
  });

  it("does not reopen a dismissed release but keeps the badge", async () => {
    window.localStorage.setItem("dismissed_update_version", "v1.2.4");
    await showSidebar(0);
    expect(screen.queryByText("管理员升级提示")).toBeNull();
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "可更新，打开面板升级" }));
    });
    expect(screen.getByText("管理员升级提示")).toBeTruthy();
  });

  it("移动端侧栏抽屉关闭时仍会检查更新并弹出更新弹窗", async () => {
    sidebarState.mobileClosed = true;
    await showSidebar(0);
    expect(checkPanelUpdate).toHaveBeenCalledOnce();
    expect(screen.queryByRole("button", { name: "可更新，打开面板升级" })).toBeNull();
    expect(screen.getByText("管理员升级提示")).toBeTruthy();
  });

  it("作业进行中时徽标显示“更新中”，结束后恢复原提示", async () => {
    const running: PanelUpdateStatus = {
      ...update,
      strategy: "soft",
      softUpdate: { available: false, reason: "job_running" },
      runtime: { ...update.runtime, mode: "builtin" },
      job: {
        id: "job-1",
        targetVersion: "v1.2.4",
        stage: "downloading",
        startedAt: 1,
        updatedAt: 2,
        bytesDone: 1,
        bytesTotal: 10,
      },
    };
    window.localStorage.setItem("dismissed_update_version", "v1.2.4");
    vi.mocked(checkPanelUpdate).mockResolvedValue(running);
    vi.mocked(getPanelUpdateStatus).mockResolvedValue(running);
    await showSidebar(0);
    const badge = screen.getByRole("button", { name: "更新中，查看面板更新进度" });
    expect(badge.textContent).toContain("更新中");
    expect(screen.queryByText("可更新")).toBeNull();
    // 作业进行中每 1.5 秒读取一次状态，不访问 GitHub 检查接口。
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_500);
    });
    expect(getPanelUpdateStatus).toHaveBeenCalledOnce();
    expect(checkPanelUpdate).toHaveBeenCalledOnce();
    vi.mocked(getPanelUpdateStatus).mockResolvedValue({
      ...running,
      job: { ...running.job!, stage: "canceled" },
      softUpdate: { available: true },
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_500);
    });
    expect(screen.getByRole("button", { name: "可更新，打开面板升级" }).textContent).toContain(
      "可更新",
    );
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "可更新，打开面板升级" }));
    });
    expect(screen.getByText("管理员升级提示")).toBeTruthy();
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
    await showSidebar(0);
    expect(screen.getByText("正常")).toBeTruthy();
    expect(screen.queryByText("连接异常")).toBeNull();
  });

  it.each(["rejected", "business error"])(
    "shows status failure when status is %s",
    async (failure) => {
      if (failure === "rejected")
        vi.mocked(getSystemStatus).mockRejectedValue(new Error("offline"));
      else vi.mocked(getSystemStatus).mockRejectedValue(new ApiError("offline", -1));
      await showSidebar(0);
      expect(screen.queryByText("连接异常")).toBeNull();
      await act(async () => {
        await vi.advanceTimersByTimeAsync(60_010);
      });
      expect(screen.getByText("连接异常")).toBeTruthy();
      expect(getSystemStatus).toHaveBeenCalledTimes(2);
      vi.mocked(getSystemStatus).mockResolvedValue({
        startedAt: Date.now() - 1000,
        uptimeSeconds: 1,
      });
      await act(async () => {
        await vi.advanceTimersByTimeAsync(60_010);
      });
      expect(screen.queryByText("连接异常")).toBeNull();
    },
  );
  it("管理员每60秒轮询，隐藏时暂停，重新可见后恢复", async () => {
    await showSidebar(0);
    expect(getSystemStatus).toHaveBeenCalledOnce();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(60_000);
    });
    expect(getSystemStatus).toHaveBeenCalledTimes(2);
    vi.spyOn(document, "visibilityState", "get").mockReturnValue("hidden");
    await act(async () => {
      document.dispatchEvent(new Event("visibilitychange"));
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(120_000);
    });
    expect(getSystemStatus).toHaveBeenCalledTimes(2);
    vi.restoreAllMocks();
    await act(async () => {
      document.dispatchEvent(new Event("visibilitychange"));
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(60_000);
    });
    expect(vi.mocked(getSystemStatus).mock.calls.length).toBeGreaterThan(2);
  });
  it("管理员仅显示8个业务入口，普通用户显示控制台/我的转发/账户", async () => {
    await showSidebar(0);
    expect(screen.getAllByRole("link")).toHaveLength(8);
    cleanup();
    client.clear();
    await showSidebar(1);
    expect(screen.getAllByRole("link").map((link) => link.textContent)).toEqual([
      "控制台",
      "我的转发",
      "账户",
    ]);
  });
});
