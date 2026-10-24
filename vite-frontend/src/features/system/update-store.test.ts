import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  applyPanelUpdate,
  cancelPanelUpdate,
  getPanelUpdateStatus,
  getSystemVersion,
  rollbackPanel,
} from "@/lib/api";
import { notify } from "@/lib/notify";
import type {
  BuildInfo,
  PanelRuntimeInfo,
  PanelUpdateJob,
  PanelUpdateResult,
  PanelUpdateStatus,
} from "@/lib/types";
import { createPanelUpdateStore, isPanelUpdateBusy } from "./update-store";

vi.mock("@/lib/api", () => ({
  applyPanelUpdate: vi.fn(),
  cancelPanelUpdate: vi.fn(),
  checkPanelUpdate: vi.fn(),
  getPanelUpdateStatus: vi.fn(),
  getSystemVersion: vi.fn(),
  rollbackPanel: vi.fn(),
}));
vi.mock("@/lib/notify", () => ({
  notify: {
    promise: vi.fn(async (operation: Promise<unknown> | (() => Promise<unknown>)) =>
      typeof operation === "function" ? operation() : operation,
    ),
    success: vi.fn(),
    error: vi.fn(),
    warning: vi.fn(),
    info: vi.fn(),
  },
}));

const MARKER = "go-panel:update-result";
const build = (version: string): BuildInfo => ({
  version,
  commit: "abcdef0123",
  buildTime: "",
  sourceUrl: "",
});
const runtime = (patch: Partial<PanelRuntimeInfo> = {}): PanelRuntimeInfo => ({
  mode: "builtin",
  builtinVersion: "v0.2.0",
  imageTag: "latest",
  rollback: { available: false },
  builtinRestore: { available: false },
  ...patch,
});
const status = (patch: Partial<PanelUpdateStatus> = {}): PanelUpdateStatus => ({
  current: build("v0.2.0"),
  enabled: true,
  latestVersion: "v0.3.0",
  updateAvailable: true,
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
const result = (patch: Partial<PanelUpdateResult> = {}): PanelUpdateResult => ({
  requestId: "req-1",
  action: "upgrade",
  version: "v0.3.0",
  outcome: "committed",
  at: 5_000,
  ...patch,
});
function memoryStorage() {
  const values = new Map<string, string>();
  return {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => void values.set(key, value),
    removeItem: (key: string) => void values.delete(key),
  };
}

let reload: ReturnType<typeof vi.fn<() => void>>;
let storage: ReturnType<typeof memoryStorage>;
const createStore = () => createPanelUpdateStore({ reload, storage: () => storage });

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date("2026-10-24T00:00:00Z"));
  reload = vi.fn();
  storage = memoryStorage();
  vi.mocked(getSystemVersion).mockRejectedValue(new Error("无法连接面板"));
  vi.mocked(getPanelUpdateStatus).mockRejectedValue(new Error("无法连接面板"));
});
afterEach(() => {
  vi.useRealTimers();
  vi.clearAllMocks();
});

describe("软更新流程", () => {
  it("作业每 1.5 秒轮询，重启后先等版本再等试运行提交，然后刷新页面并展示结果", async () => {
    const store = createStore();
    store.receiveStatus(status());
    vi.mocked(applyPanelUpdate).mockResolvedValue({
      targetVersion: "v0.3.0",
      backupFile: "",
      started: true,
      strategy: "soft",
      jobId: "job-1",
    });
    vi.mocked(getPanelUpdateStatus).mockResolvedValueOnce(
      status({ job: job("downloading", { bytesDone: 10, bytesTotal: 100 }) }),
    );
    await store.apply();
    expect(applyPanelUpdate).toHaveBeenCalledWith("v0.3.0", false);
    expect(store.getSnapshot()).toMatchObject({
      dialogOpen: true,
      flow: { phase: "job", jobId: "job-1", targetVersion: "v0.3.0" },
    });
    expect(isPanelUpdateBusy(store.getSnapshot())).toBe(true);
    await vi.advanceTimersByTimeAsync(0);
    expect(getPanelUpdateStatus).toHaveBeenCalledTimes(1);
    expect(store.getSnapshot().status?.job?.stage).toBe("downloading");

    vi.mocked(getPanelUpdateStatus).mockResolvedValueOnce(
      status({ job: job("restarting", { requestId: "req-1", readyTimeoutSec: 120 }) }),
    );
    await vi.advanceTimersByTimeAsync(1_499);
    expect(getPanelUpdateStatus).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(getPanelUpdateStatus).toHaveBeenCalledTimes(2);
    const flow = store.getSnapshot().flow;
    expect(flow).toMatchObject({
      phase: "restarting",
      target: { kind: "upgrade", requestId: "req-1", since: 1_000 },
    });
    // 就绪超时 120 秒 + 60 秒稳定期。
    if (flow.phase === "restarting")
      expect(flow.target.deadline - flow.target.startedAt).toBe(180_000);

    // 重启中请求失败属于预期，继续每 3 秒探测版本。
    await vi.advanceTimersByTimeAsync(3_000);
    expect(getSystemVersion).toHaveBeenCalledTimes(1);
    expect(store.getSnapshot().flow.phase).toBe("restarting");

    vi.mocked(getSystemVersion).mockResolvedValueOnce(build("v0.3.0"));
    vi.mocked(getPanelUpdateStatus).mockResolvedValueOnce(
      status({
        current: build("v0.3.0"),
        runtime: runtime({
          mode: "trial",
          trialVersion: "v0.3.0",
          lastResult: result({ requestId: "old", at: 1 }),
        }),
      }),
    );
    await vi.advanceTimersByTimeAsync(3_000);
    expect(store.getSnapshot().flow.phase).toBe("confirming");
    expect(reload).not.toHaveBeenCalled();

    vi.mocked(getPanelUpdateStatus).mockResolvedValueOnce(
      status({
        current: build("v0.3.0"),
        updateAvailable: false,
        runtime: runtime({ mode: "trial", trialVersion: "v0.3.0", lastResult: result() }),
      }),
    );
    await vi.advanceTimersByTimeAsync(3_000);
    expect(reload).toHaveBeenCalledOnce();
    expect(store.getSnapshot().flow.phase).toBe("reloading");
    expect(JSON.parse(storage.getItem(MARKER)!)).toMatchObject({
      targetVersion: "v0.3.0",
      requestId: "req-1",
      action: "upgrade",
    });

    // 刷新后的新页面按服务端结果弹出“已更新”，并清除标记。
    const reloaded = createStore();
    reloaded.receiveStatus(
      status({
        current: build("v0.3.0"),
        updateAvailable: false,
        runtime: runtime({ mode: "release", lastResult: result() }),
      }),
    );
    expect(reloaded.getSnapshot()).toMatchObject({
      dialogOpen: true,
      flow: { phase: "result", result: { outcome: "committed", requestId: "req-1" } },
    });
    expect(storage.getItem(MARKER)).toBeNull();
  });

  it("新版本未就绪、旧版本带着本次回滚结果恢复时直接展示，不刷新页面", async () => {
    const store = createStore();
    store.receiveStatus(status({ job: job("restarting", { requestId: "req-2" }) }));
    expect(store.getSnapshot().flow.phase).toBe("restarting");
    vi.mocked(getSystemVersion).mockResolvedValue(build("v0.2.0"));
    // 旧进程仍在排空：上一次的回滚结果不能当成本次结果。
    vi.mocked(getPanelUpdateStatus).mockResolvedValueOnce(
      status({
        job: job("restarting", { requestId: "req-2" }),
        runtime: runtime({ lastResult: result({ requestId: "old", outcome: "rolled_back" }) }),
      }),
    );
    await vi.advanceTimersByTimeAsync(3_000);
    expect(store.getSnapshot().flow.phase).toBe("restarting");
    const rolledBack = result({
      requestId: "req-2",
      outcome: "rolled_back",
      stage: "healthz",
      detail: "panic: boom",
      dbRestored: true,
    });
    vi.mocked(getPanelUpdateStatus).mockResolvedValueOnce(
      status({ runtime: runtime({ lastResult: rolledBack }) }),
    );
    await vi.advanceTimersByTimeAsync(3_000);
    expect(store.getSnapshot()).toMatchObject({
      dialogOpen: true,
      flow: { phase: "result", result: rolledBack },
    });
    expect(reload).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(30_000);
    expect(getSystemVersion).toHaveBeenCalledTimes(2);
  });

  it("没有请求编号时用作业开始时间排除更早的同版本结果", async () => {
    const store = createStore();
    store.receiveStatus(status({ job: job("restarting", { startedAt: 10_000 }) }));
    vi.mocked(getSystemVersion).mockResolvedValue(build("v0.2.0"));
    vi.mocked(getPanelUpdateStatus).mockResolvedValueOnce(
      status({ runtime: runtime({ lastResult: result({ outcome: "rolled_back", at: 9_999 }) }) }),
    );
    await vi.advanceTimersByTimeAsync(3_000);
    expect(store.getSnapshot().flow.phase).toBe("restarting");
    vi.mocked(getPanelUpdateStatus).mockResolvedValueOnce(
      status({ runtime: runtime({ lastResult: result({ outcome: "rejected", at: 10_001 }) }) }),
    );
    await vi.advanceTimersByTimeAsync(3_000);
    expect(store.getSnapshot().flow).toMatchObject({
      phase: "result",
      result: { outcome: "rejected" },
    });
  });

  it("等待超过“就绪超时 + 60 秒”时停止轮询并提示查看日志", async () => {
    const store = createStore();
    store.receiveStatus(status({ job: job("restarting", { requestId: "req-3" }) }));
    // 后端未给出 readyTimeoutSec 时按 360 秒等待。
    await vi.advanceTimersByTimeAsync(357_000);
    expect(store.getSnapshot().flow.phase).toBe("restarting");
    await vi.advanceTimersByTimeAsync(3_000);
    expect(store.getSnapshot()).toMatchObject({ dialogOpen: true, flow: { phase: "timeout" } });
    const calls = vi.mocked(getSystemVersion).mock.calls.length;
    await vi.advanceTimersByTimeAsync(60_000);
    expect(getSystemVersion).toHaveBeenCalledTimes(calls);
    expect(reload).not.toHaveBeenCalled();
  });

  it("错过 restarting 阶段时，迁移演练后连续两次读取失败即按重启等待", async () => {
    const store = createStore();
    store.receiveStatus(status({ job: job("migrate_check") }));
    expect(store.getSnapshot().flow.phase).toBe("job");
    await vi.advanceTimersByTimeAsync(1_500);
    expect(store.getSnapshot().flow.phase).toBe("job");
    await vi.advanceTimersByTimeAsync(1_500);
    expect(store.getSnapshot().flow).toMatchObject({
      phase: "restarting",
      target: { targetVersion: "v0.3.0", since: 1_000 },
    });
  });

  it("下载阶段读取失败不会误判为重启", async () => {
    const store = createStore();
    store.receiveStatus(status({ job: job("downloading") }));
    await vi.advanceTimersByTimeAsync(15_000);
    expect(store.getSnapshot().flow.phase).toBe("job");
  });

  it("作业失败回到资格视图，弹窗关闭时用通知提示原因", async () => {
    const store = createStore();
    store.receiveStatus(status({ job: job("downloading") }));
    vi.mocked(getPanelUpdateStatus).mockResolvedValueOnce(
      status({ job: job("failed", { error: "更新包校验失败" }) }),
    );
    await vi.advanceTimersByTimeAsync(1_500);
    expect(store.getSnapshot().flow.phase).toBe("idle");
    expect(notify.error).toHaveBeenCalledWith("面板更新失败：更新包校验失败");
    const calls = vi.mocked(getPanelUpdateStatus).mock.calls.length;
    await vi.advanceTimersByTimeAsync(10_000);
    expect(getPanelUpdateStatus).toHaveBeenCalledTimes(calls);
  });

  it("只在备份数据库之前允许取消", async () => {
    const store = createStore();
    store.receiveStatus(status({ job: job("downloading") }));
    vi.mocked(cancelPanelUpdate).mockResolvedValue(null);
    vi.mocked(getPanelUpdateStatus).mockResolvedValueOnce(status({ job: job("canceled") }));
    await store.cancel();
    expect(cancelPanelUpdate).toHaveBeenCalledWith("job-1");
    expect(notify.success).toHaveBeenCalledWith("已取消面板更新");
    expect(store.getSnapshot().flow.phase).toBe("idle");
    store.receiveStatus(status({ job: job("backing_up", { id: "job-2" }) }));
    await store.cancel();
    expect(cancelPanelUpdate).toHaveBeenCalledTimes(1);
  });

  it("隔离版本带 force 重试，提交失败时保留状态并刷新资格", async () => {
    const store = createStore();
    store.receiveStatus(
      status({ strategy: "manual", softUpdate: { available: false, reason: "quarantined" } }),
    );
    vi.mocked(applyPanelUpdate).mockRejectedValueOnce(new Error("版本已变化，请重新检查"));
    vi.mocked(getPanelUpdateStatus).mockResolvedValueOnce(status({ latestVersion: "v0.3.1" }));
    const pending = store.apply(true);
    expect(store.getSnapshot().busy).toBe("retry");
    await pending;
    expect(applyPanelUpdate).toHaveBeenCalledWith("v0.3.0", true);
    expect(store.getSnapshot().busy).toBeNull();
    expect(store.getSnapshot().flow.phase).toBe("idle");
    await vi.advanceTimersByTimeAsync(0);
    expect(store.getSnapshot().status?.latestVersion).toBe("v0.3.1");
  });

  it("侧车策略沿用版本轮询，版本一致即刷新且不读取试运行状态", async () => {
    const store = createStore();
    store.receiveStatus(status({ strategy: "sidecar", softUpdate: { available: false } }));
    vi.mocked(applyPanelUpdate).mockResolvedValue({
      targetVersion: "v0.3.0",
      backupFile: "backups/pre-update-v0.3.0.db",
      started: true,
      strategy: "sidecar",
    });
    vi.mocked(getPanelUpdateStatus).mockClear();
    await store.apply();
    expect(store.getSnapshot().flow).toMatchObject({
      phase: "restarting",
      target: { kind: "sidecar" },
    });
    vi.mocked(getSystemVersion).mockResolvedValueOnce(build("v0.2.0"));
    await vi.advanceTimersByTimeAsync(3_000);
    expect(reload).not.toHaveBeenCalled();
    vi.mocked(getSystemVersion).mockResolvedValueOnce(build("v0.3.0"));
    await vi.advanceTimersByTimeAsync(3_000);
    expect(reload).toHaveBeenCalledOnce();
    expect(getPanelUpdateStatus).not.toHaveBeenCalled();
    // 侧车没有切换记录：刷新后按运行版本确认已更新。
    const reloaded = createStore();
    reloaded.receiveStatus(status({ current: build("v0.3.0"), updateAvailable: false }));
    expect(reloaded.getSnapshot().flow).toMatchObject({
      phase: "result",
      result: { outcome: "committed", version: "v0.3.0", action: "upgrade" },
    });
  });

  it("回滚请求进入重启等待，提交后刷新页面", async () => {
    const store = createStore();
    store.receiveStatus(
      status({ runtime: runtime({ mode: "release", previousVersion: "v0.2.0" }) }),
    );
    vi.mocked(rollbackPanel).mockResolvedValue({
      requestId: "req-r",
      targetVersion: "v0.2.0",
      started: true,
    });
    await store.rollback("previous");
    expect(rollbackPanel).toHaveBeenCalledWith("previous");
    const flow = store.getSnapshot().flow;
    expect(flow).toMatchObject({
      phase: "restarting",
      target: { kind: "rollback", action: "rollback", requestId: "req-r" },
    });
    if (flow.phase === "restarting")
      expect(flow.target.deadline - flow.target.startedAt).toBe(360_000);
    vi.mocked(getSystemVersion).mockResolvedValueOnce(build("v0.2.0"));
    vi.mocked(getPanelUpdateStatus).mockResolvedValueOnce(
      status({
        runtime: runtime({
          mode: "builtin",
          lastResult: result({ requestId: "req-r", action: "rollback", version: "v0.2.0" }),
        }),
      }),
    );
    await vi.advanceTimersByTimeAsync(3_000);
    expect(reload).toHaveBeenCalledOnce();
  });

  it("试运行期间刷新页面：等到离开 trial 才刷新，不把同版本旧结果当成结论", async () => {
    const store = createStore();
    const trial = runtime({
      mode: "trial",
      trialVersion: "v0.3.0",
      lastResult: result({ requestId: "earlier", outcome: "rolled_back" }),
    });
    store.receiveStatus(status({ current: build("v0.3.0"), runtime: trial }));
    expect(store.getSnapshot().flow).toMatchObject({
      phase: "confirming",
      target: { resumed: true, targetVersion: "v0.3.0" },
    });
    expect(isPanelUpdateBusy(store.getSnapshot())).toBe(true);
    vi.mocked(getPanelUpdateStatus).mockResolvedValueOnce(
      status({ current: build("v0.3.0"), runtime: trial }),
    );
    await vi.advanceTimersByTimeAsync(3_000);
    expect(reload).not.toHaveBeenCalled();
    vi.mocked(getPanelUpdateStatus).mockResolvedValueOnce(
      status({
        current: build("v0.2.0"),
        runtime: runtime({ lastResult: result({ requestId: "req-9", outcome: "rolled_back" }) }),
      }),
    );
    await vi.advanceTimersByTimeAsync(3_000);
    expect(reload).toHaveBeenCalledOnce();
  });

  it("重置后停止全部轮询并清空状态", async () => {
    const store = createStore();
    store.receiveStatus(status({ job: job("downloading") }));
    store.reset();
    expect(store.getSnapshot()).toMatchObject({ status: null, flow: { phase: "idle" } });
    await vi.advanceTimersByTimeAsync(10_000);
    expect(getPanelUpdateStatus).not.toHaveBeenCalled();
  });

  it("关闭结果弹窗后，下次打开回到资格视图", async () => {
    storage.setItem(
      MARKER,
      JSON.stringify({
        targetVersion: "v0.3.0",
        requestId: "req-1",
        action: "upgrade",
        savedAt: Date.now(),
      }),
    );
    const store = createStore();
    store.receiveStatus(
      status({
        current: build("v0.3.0"),
        updateAvailable: false,
        runtime: runtime({ lastResult: result() }),
      }),
    );
    expect(store.getSnapshot().flow.phase).toBe("result");
    store.closeDialog();
    expect(store.getSnapshot().flow.phase).toBe("result");
    store.openDialog();
    expect(store.getSnapshot()).toMatchObject({ dialogOpen: true, flow: { phase: "idle" } });
  });

  it("过期或损坏的结果标记会被丢弃", () => {
    storage.setItem(MARKER, "{broken");
    const store = createStore();
    store.receiveStatus(status({ current: build("v0.3.0") }));
    expect(store.getSnapshot().flow.phase).toBe("idle");
    storage.setItem(
      MARKER,
      JSON.stringify({
        targetVersion: "v0.3.0",
        action: "upgrade",
        savedAt: Date.now() - 16 * 60_000,
      }),
    );
    const next = createStore();
    next.receiveStatus(status({ current: build("v0.3.0") }));
    expect(next.getSnapshot().flow.phase).toBe("idle");
    expect(storage.getItem(MARKER)).toBeNull();
  });
});
