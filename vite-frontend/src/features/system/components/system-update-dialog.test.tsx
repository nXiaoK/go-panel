import "@testing-library/jest-dom/vitest";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ConfirmProvider } from "@/components/patterns/confirm-dialog";
import {
  applyPanelUpdate,
  cancelPanelUpdate,
  getPanelUpdateStatus,
  getSystemVersion,
} from "@/lib/api";
import type {
  PanelRuntimeInfo,
  PanelUpdateJob,
  PanelUpdateResult,
  PanelUpdateStatus,
} from "@/lib/types";
import { panelUpdateStore } from "../update-store";
import { SystemUpdateDialog } from "./system-update-dialog";

vi.mock("@/lib/api", () => ({
  applyPanelUpdate: vi.fn(),
  cancelPanelUpdate: vi.fn(),
  checkPanelUpdate: vi.fn(),
  getPanelUpdateStatus: vi.fn(),
  getSystemVersion: vi.fn(),
  rollbackPanel: vi.fn(),
}));

const runtime = (patch: Partial<PanelRuntimeInfo> = {}): PanelRuntimeInfo => ({
  mode: "builtin",
  builtinVersion: "v0.2.0",
  imageTag: "latest",
  rollback: { available: false },
  builtinRestore: { available: false },
  ...patch,
});
const status = (patch: Partial<PanelUpdateStatus> = {}): PanelUpdateStatus => ({
  current: { version: "v0.2.0", commit: "abcdef0", buildTime: "", sourceUrl: "" },
  enabled: true,
  latestVersion: "v0.3.0",
  updateAvailable: true,
  releaseUrl: "https://github.com/nXiaoK/go-panel/releases/tag/v0.3.0",
  releaseNotes: "修复若干问题",
  publishedAt: "2026-11-01T00:00:00Z",
  autoUpdateConfigured: true,
  strategy: "soft",
  softUpdate: { available: true, downloadBytes: 43_918_511 },
  runtime: runtime(),
  ...patch,
});
const job = (
  stage: PanelUpdateJob["stage"],
  patch: Partial<PanelUpdateJob> = {},
): PanelUpdateJob => ({
  id: "job-1",
  targetVersion: "v0.3.0",
  stage,
  startedAt: 1_000,
  updatedAt: 2_000,
  ...patch,
});
const onOpenChange = vi.fn();

function show(value: PanelUpdateStatus) {
  vi.mocked(getPanelUpdateStatus).mockResolvedValue(value);
  act(() => {
    panelUpdateStore.receiveStatus(value);
  });
  return render(
    <ConfirmProvider>
      <SystemUpdateDialog open onOpenChange={onOpenChange} />
    </ConfirmProvider>,
  );
}
function showResult(result: PanelUpdateResult) {
  // 页面因更新刷新前会写入结果标记，刷新后按服务端最新结果展示。
  window.sessionStorage.setItem(
    "go-panel:update-result",
    JSON.stringify({
      targetVersion: result.version,
      requestId: result.requestId,
      action: result.action,
      savedAt: Date.now(),
    }),
  );
  return show(
    status({
      current: { version: "v0.2.0", commit: "abcdef0", buildTime: "", sourceUrl: "" },
      updateAvailable: false,
      runtime: runtime({ lastResult: result }),
    }),
  );
}

beforeEach(() => {
  panelUpdateStore.reset();
  vi.mocked(getSystemVersion).mockRejectedValue(new Error("无法连接面板"));
});
afterEach(() => {
  cleanup();
  panelUpdateStore.reset();
  window.sessionStorage.clear();
  window.localStorage.clear();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

describe("更新弹窗：资格与主操作", () => {
  it("可以软更新：说明影响与下载大小，只提供“立即更新”，点击后显示作业进度", async () => {
    show(status());
    expect(screen.getByRole("heading", { name: "发现新版本 v0.3.0" })).toBeInTheDocument();
    expect(
      screen.getByText("无需重建容器；更新期间面板约中断 1 分钟，节点转发不受影响；失败会自动回滚"),
    ).toBeInTheDocument();
    expect(screen.getByText("下载约 41.9 MB")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "通过更新侧车更新" })).toBeNull();
    expect(screen.queryByText(/docker compose pull/)).toBeNull();
    expect(screen.getByRole("link", { name: "发布说明" })).toHaveAttribute(
      "href",
      "https://github.com/nXiaoK/go-panel/releases/tag/v0.3.0",
    );
    vi.mocked(applyPanelUpdate).mockResolvedValue({
      targetVersion: "v0.3.0",
      backupFile: "",
      started: true,
      strategy: "soft",
      jobId: "job-1",
    });
    vi.mocked(getPanelUpdateStatus).mockResolvedValue(status({ job: job("downloading") }));
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "立即更新" }));
    });
    expect(applyPanelUpdate).toHaveBeenCalledWith("v0.3.0", false);
    expect(await screen.findByRole("heading", { name: "正在更新到 v0.3.0" })).toBeInTheDocument();
  });

  it("固定镜像标签时提示软更新后运行版本将高于镜像标签", () => {
    show(status({ runtime: runtime({ imageTag: "v0.2.0" }) }));
    expect(screen.getByText(/软更新后运行版本将高于镜像标签/)).toHaveTextContent(
      "软更新后运行版本将高于镜像标签 v0.2.0",
    );
  });

  it("需要更新镜像且侧车可用：显示原因、可复制命令，主按钮为“通过更新侧车更新”", async () => {
    vi.useFakeTimers();
    const copy = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal("navigator", { clipboard: { writeText: copy } });
    show(
      status({
        strategy: "sidecar",
        softUpdate: { available: false, reason: "requires_image_update" },
      }),
    );
    expect(screen.getByText("该版本需要更新镜像，请在部署目录执行下方命令")).toBeInTheDocument();
    expect(screen.getByText("docker compose pull && docker compose up -d")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "通过更新侧车更新" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "立即更新" })).toBeNull();
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "复制更新命令" }));
    });
    expect(copy).toHaveBeenCalledWith("docker compose pull && docker compose up -d");
    expect(screen.getByRole("button", { name: "已复制" })).toBeInTheDocument();
    act(() => vi.advanceTimersByTime(1_999));
    expect(screen.getByRole("button", { name: "已复制" })).toBeInTheDocument();
    act(() => vi.advanceTimersByTime(1));
    expect(screen.getByRole("button", { name: "复制更新命令" })).toBeInTheDocument();
  });

  it("无法软更新且没有侧车：只给原因和手动命令，不提供更新按钮", () => {
    show(
      status({
        strategy: "manual",
        autoUpdateConfigured: false,
        softUpdate: { available: false, reason: "not_supervised" },
        runtime: runtime({ mode: "unsupervised", imageTag: "v0.2.0" }),
      }),
    );
    expect(screen.getByText("当前镜像不支持软更新，请先手动更新一次镜像")).toBeInTheDocument();
    expect(screen.getByText("docker compose pull && docker compose up -d")).toBeInTheDocument();
    expect(screen.getByText(/请先把 .env 中的/)).toHaveTextContent(
      "镜像标签固定为 v0.2.0，请先把 .env 中的 GO_PANEL_VERSION 改为 v0.3.0 再执行",
    );
    expect(screen.queryByRole("button", { name: "立即更新" })).toBeNull();
    expect(screen.queryByRole("button", { name: "通过更新侧车更新" })).toBeNull();
    expect(screen.getByRole("button", { name: "稍后提醒" })).toBeInTheDocument();
  });

  it("“稍后提醒”只记住当前版本", () => {
    show(status());
    fireEvent.click(screen.getByRole("button", { name: "稍后提醒" }));
    expect(window.localStorage.getItem("dismissed_update_version")).toBe("v0.3.0");
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("隔离版本：显示原因详情，“仍要重试”为危险描边按钮，确认后带 force 调用", async () => {
    show(
      status({
        strategy: "manual",
        softUpdate: { available: false, reason: "quarantined", detail: "就绪超时" },
      }),
    );
    expect(
      screen.getByText("该版本曾试运行失败（就绪超时），已暂停软更新，确认问题已解决后可以重试"),
    ).toBeInTheDocument();
    const retry = screen.getByRole("button", { name: "仍要重试" });
    expect(retry).toHaveClass("border-status-critical", "text-status-critical-text");
    fireEvent.click(retry);
    let confirm = await screen.findByRole("alertdialog", { name: "仍要重试 v0.3.0？" });
    fireEvent.click(within(confirm).getByRole("button", { name: "取消" }));
    await waitFor(() =>
      expect(screen.queryByRole("alertdialog", { name: "仍要重试 v0.3.0？" })).toBeNull(),
    );
    expect(applyPanelUpdate).not.toHaveBeenCalled();
    vi.mocked(applyPanelUpdate).mockRejectedValue(new Error("版本已变化，请重新检查"));
    fireEvent.click(screen.getByRole("button", { name: "仍要重试" }));
    confirm = await screen.findByRole("alertdialog", { name: "仍要重试 v0.3.0？" });
    expect(confirm).toHaveTextContent("v0.3.0 曾试运行失败（就绪超时）");
    await act(async () => {
      fireEvent.click(within(confirm).getByRole("button", { name: "仍要重试" }));
    });
    await waitFor(() => expect(applyPanelUpdate).toHaveBeenCalledWith("v0.3.0", true));
  });

  it("上次作业失败时提示原因并保留重试入口", () => {
    show(status({ job: job("failed", { error: "更新包校验失败" }) }));
    expect(screen.getByRole("alert")).toHaveTextContent("更新到 v0.3.0 失败：更新包校验失败");
    expect(screen.getByRole("button", { name: "立即更新" })).toBeInTheDocument();
  });
});

describe("更新弹窗：作业进度", () => {
  it("阶段列表标出当前阶段，下载进度条提供 aria 数值与文字", () => {
    show(status({ job: job("downloading", { bytesDone: 12_897_485, bytesTotal: 43_918_511 }) }));
    expect(screen.getByRole("heading", { name: "正在更新到 v0.3.0" })).toBeInTheDocument();
    const progress = screen.getByRole("progressbar", { name: "下载进度" });
    expect(progress).toHaveAttribute("aria-valuenow", "29");
    expect(progress).toHaveAttribute("aria-valuemin", "0");
    expect(progress).toHaveAttribute("aria-valuemax", "100");
    expect(progress).toHaveAttribute("aria-valuetext", "12.3 MB / 41.9 MB（29%）");
    expect(screen.getByText("12.3 MB / 41.9 MB")).toBeInTheDocument();
    const steps = within(screen.getByRole("list", { name: "更新阶段" })).getAllByRole("listitem");
    expect(steps.map((step) => step.textContent)).toEqual([
      "下载更新包进行中",
      "校验并解包未开始",
      "检查新版本未开始",
      "备份数据库未开始",
      "演练数据迁移未开始",
      "正在重启未开始",
    ]);
    expect(steps[0]).toHaveAttribute("aria-current", "step");
  });

  it("已完成与未开始的阶段用不同图标和文字区分", () => {
    show(status({ job: job("backing_up") }));
    const steps = within(screen.getByRole("list", { name: "更新阶段" })).getAllByRole("listitem");
    expect(steps[0]).toHaveTextContent("下载更新包已完成");
    expect(steps[3]).toHaveAttribute("aria-current", "step");
    expect(steps[4]).toHaveTextContent("演练数据迁移未开始");
    expect(steps[0].querySelector("svg")?.getAttribute("class")).toContain("lucide-circle-check");
    expect(steps[4].querySelector("svg")?.getAttribute("class")).toMatch(/lucide-circle(\s|$)/);
    expect(screen.queryByRole("progressbar")).toBeNull();
  });

  it.each([
    ["queued", true],
    ["downloading", true],
    ["extracting", true],
    ["preflight", true],
    ["backing_up", false],
    ["migrate_check", false],
  ] as const)("阶段 %s 是否允许取消：%s", (stage, visible) => {
    show(status({ job: job(stage) }));
    expect(Boolean(screen.queryByRole("button", { name: "取消更新" }))).toBe(visible);
  });

  it("取消更新调用取消接口", async () => {
    show(status({ job: job("extracting") }));
    vi.mocked(cancelPanelUpdate).mockResolvedValue(null);
    vi.mocked(getPanelUpdateStatus).mockResolvedValue(status({ job: job("canceled") }));
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "取消更新" }));
    });
    expect(cancelPanelUpdate).toHaveBeenCalledWith("job-1");
    expect(await screen.findByText("已取消更新到 v0.3.0")).toBeInTheDocument();
  });
});

describe("更新弹窗：重启与结果", () => {
  it("正在重启：说明文字与加载指示", () => {
    show(status({ job: job("restarting", { requestId: "req-1", readyTimeoutSec: 300 }) }));
    expect(screen.getByRole("heading", { name: "面板正在重启" })).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("正在等待面板重新上线");
    expect(screen.getByRole("status")).toHaveTextContent("最长等待约 6 分钟");
  });

  it("试运行确认中", () => {
    show(
      status({
        current: { version: "v0.3.0", commit: "", buildTime: "", sourceUrl: "" },
        runtime: runtime({ mode: "trial", trialVersion: "v0.3.0" }),
      }),
    );
    expect(screen.getByRole("heading", { name: "v0.3.0 试运行中" })).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("正在确认 v0.3.0 运行稳定");
  });

  it("已更新：good 色调", () => {
    showResult({
      requestId: "req-1",
      action: "upgrade",
      version: "v0.3.0",
      outcome: "committed",
      at: Date.now(),
    });
    expect(screen.getByRole("heading", { name: "已更新到 v0.3.0" })).toBeInTheDocument();
    expect(screen.getByText("已更新")).toHaveClass("text-status-good");
    expect(document.querySelector("details")).toBeNull();
  });

  it("已自动回滚：critical 色调，详情可折叠，恢复快照时额外警告", () => {
    showResult({
      requestId: "req-2",
      action: "upgrade",
      version: "v0.3.0",
      outcome: "rolled_back",
      stage: "ready_timeout",
      exitCode: 1,
      detail: "migrate: no such column",
      dbRestored: true,
      at: Date.now(),
    });
    expect(screen.getByRole("heading", { name: "更新失败，已自动回滚" })).toBeInTheDocument();
    expect(
      screen.getByText("v0.3.0 试运行失败（就绪超时），面板已恢复到原版本"),
    ).toBeInTheDocument();
    expect(screen.getByText("已自动回滚")).toHaveClass("text-status-critical-text");
    const details = document.querySelector("details")!;
    expect(details.open).toBe(false);
    expect(within(details).getByText("migrate: no such column")).toBeInTheDocument();
    expect(within(details).getByText("失败阶段：就绪超时")).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent(
      "已恢复更新前的数据库快照，试运行期间的修改已丢失；失败时的数据库保存在 backups/failed-update-*",
    );
  });

  it("等待超时：提示查看容器日志", async () => {
    vi.useFakeTimers();
    show(status({ job: job("restarting", { requestId: "req-3", readyTimeoutSec: 120 }) }));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(180_000);
    });
    expect(screen.getByRole("heading", { name: "暂未确认更新结果" })).toBeInTheDocument();
    expect(screen.getByText("docker compose logs --tail 200 panel")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "刷新页面" })).toBeInTheDocument();
  });
});
