import { describe, expect, it } from "vitest";

import type {
  PanelRuntimeInfo,
  PanelUpdateJob,
  PanelUpdateResult,
  PanelUpdateStatus,
  SoftUpdateBlockReason,
  UpdateJobStage,
} from "../src/lib/types";
import {
  canCancelUpdateJob,
  dismissUpdate,
  displayVersion,
  downloadPercent,
  formatDownloadProgress,
  isUpdateJobActive,
  MANUAL_UPDATE_COMMAND,
  matchesUpdateResult,
  normalizePanelUpdateStatus,
  PANEL_LOGS_COMMAND,
  primaryUpdateAction,
  restartWaitLimitMs,
  resultLabel,
  resultStageLabel,
  resultTone,
  runModeLabel,
  shouldPromptForUpdate,
  softUpdateReasonText,
  updateStageLabel,
  updateStageStates,
  updateStrategyLabel,
} from "../src/lib/update";

function memoryStorage() {
  const values = new Map<string, string>();
  return {
    getItem(key: string) {
      return values.get(key) ?? null;
    },
    setItem(key: string, value: string) {
      values.set(key, value);
    },
  };
}

const runtime = (patch: Partial<PanelRuntimeInfo> = {}): PanelRuntimeInfo => ({
  mode: "builtin",
  imageTag: "latest",
  rollback: { available: false },
  builtinRestore: { available: false },
  ...patch,
});
const status = (patch: Partial<PanelUpdateStatus> = {}): PanelUpdateStatus => ({
  current: { version: "v0.2.0", commit: "abc", buildTime: "", sourceUrl: "" },
  enabled: true,
  latestVersion: "v0.3.0",
  updateAvailable: true,
  autoUpdateConfigured: true,
  strategy: "soft",
  softUpdate: { available: true },
  runtime: runtime(),
  ...patch,
});
const job = (stage: UpdateJobStage): PanelUpdateJob => ({
  id: "job-1",
  targetVersion: "v0.3.0",
  stage,
  startedAt: 1,
  updatedAt: 2,
});
const result = (patch: Partial<PanelUpdateResult> = {}): PanelUpdateResult => ({
  requestId: "req-1",
  action: "upgrade",
  version: "v0.3.0",
  outcome: "committed",
  at: 100,
  ...patch,
});

describe("panel update helpers", () => {
  it("formats release and development versions", () => {
    expect(displayVersion("v1.2.3", "abcdef0123")).toBe("v1.2.3");
    expect(displayVersion("dev", "abcdef0123")).toBe("dev-abcdef0");
    expect(displayVersion("", "unknown")).toBe("dev");
  });

  it("only suppresses the exact dismissed release", () => {
    const storage = memoryStorage();
    expect(shouldPromptForUpdate("v1.1.0", storage)).toBe(true);
    dismissUpdate("v1.1.0", storage);
    expect(shouldPromptForUpdate("v1.1.0", storage)).toBe(false);
    expect(shouldPromptForUpdate("v1.2.0", storage)).toBe(true);
  });

  it("keeps the manual and log commands", () => {
    expect(MANUAL_UPDATE_COMMAND).toBe("docker compose pull && docker compose up -d");
    expect(PANEL_LOGS_COMMAND).toBe("docker compose logs --tail 200 panel");
  });
});

describe("作业阶段", () => {
  it.each([
    ["queued", "等待开始"],
    ["downloading", "下载更新包"],
    ["extracting", "校验并解包"],
    ["preflight", "检查新版本"],
    ["backing_up", "备份数据库"],
    ["migrate_check", "演练数据迁移"],
    ["restarting", "正在重启"],
    ["failed", "失败"],
    ["canceled", "已取消"],
  ] as const)("%s → %s", (stage, label) => {
    expect(updateStageLabel(stage)).toBe(label);
  });

  it("未知或原型属性名不会显示为文案", () => {
    expect(updateStageLabel("toString")).toBe("未知阶段");
    expect(updateStageLabel(undefined)).toBe("未知阶段");
  });

  it("阶段列表按顺序标出已完成、当前与未开始", () => {
    expect(updateStageStates("queued").map((step) => step.state)).toEqual(Array(6).fill("pending"));
    expect(updateStageStates("preflight").map((step) => [step.label, step.state])).toEqual([
      ["下载更新包", "done"],
      ["校验并解包", "done"],
      ["检查新版本", "current"],
      ["备份数据库", "pending"],
      ["演练数据迁移", "pending"],
      ["正在重启", "pending"],
    ]);
    expect(updateStageStates("restarting").at(-1)?.state).toBe("current");
  });

  it.each([
    ["queued", true, true],
    ["downloading", true, true],
    ["extracting", true, true],
    ["preflight", true, true],
    ["backing_up", true, false],
    ["migrate_check", true, false],
    ["restarting", true, false],
    ["failed", false, false],
    ["canceled", false, false],
  ] as const)("阶段 %s：进行中=%s，可取消=%s", (stage, active, cancelable) => {
    expect(isUpdateJobActive(job(stage))).toBe(active);
    expect(canCancelUpdateJob(job(stage))).toBe(cancelable);
  });

  it("没有作业时既不进行中也不可取消", () => {
    expect(isUpdateJobActive(undefined)).toBe(false);
    expect(canCancelUpdateJob(null)).toBe(false);
  });
});

describe("软更新原因码", () => {
  const cases: [SoftUpdateBlockReason, string][] = [
    ["disabled", "已按部署配置 UPDATE_STRATEGY 关闭软更新，请通过更新侧车或手动更新镜像"],
    ["not_supervised", "当前镜像不支持软更新，请先手动更新一次镜像"],
    ["development_build", "开发构建不支持软更新，请改用正式版本镜像"],
    ["runtime_dir_unwritable", "运行时目录不可写，无法保存新版本，请检查数据卷权限后重试"],
    ["no_update", "当前已是最新稳定版，无需更新"],
    ["check_failed", "检查更新失败，请稍后重试，或确认服务器可以访问 GitHub"],
    ["manifest_missing", "该版本没有提供软更新清单，请更新镜像"],
    ["signature_invalid", "更新清单签名校验未通过，已拒绝软更新，请改为更新镜像"],
    ["manifest_invalid", "更新清单无效或与发布版本不一致，已拒绝软更新，请改为更新镜像"],
    ["arch_unavailable", "该版本没有当前 CPU 架构的更新包，请更新镜像"],
    ["requires_image_update", "该版本需要更新镜像，请在部署目录执行下方命令"],
    ["upgrade_path_unsupported", "当前版本过旧，不能直接软更新到该版本，请先更新镜像"],
    ["quarantined", "该版本曾试运行失败，已暂停软更新，确认问题已解决后可以重试"],
    ["job_running", "已有更新作业正在进行，请等待完成"],
  ];
  it.each(cases)("%s", (reason, text) => {
    expect(softUpdateReasonText(reason)).toBe(text);
  });

  it("覆盖契约中的全部 14 个原因码", () => {
    expect(new Set(cases.map(([reason]) => reason)).size).toBe(14);
  });

  it("detail 插在说明与建议之间，空白 detail 忽略", () => {
    expect(softUpdateReasonText("quarantined", "就绪超时")).toBe(
      "该版本曾试运行失败（就绪超时），已暂停软更新，确认问题已解决后可以重试",
    );
    expect(softUpdateReasonText("signature_invalid", "未配置发布签名公钥")).toBe(
      "更新清单签名校验未通过（未配置发布签名公钥），已拒绝软更新，请改为更新镜像",
    );
    expect(softUpdateReasonText("check_failed", "  ")).toBe(
      "检查更新失败，请稍后重试，或确认服务器可以访问 GitHub",
    );
  });

  it("未知原因码给出通用建议", () => {
    expect(softUpdateReasonText("future_reason")).toBe("暂时无法软更新，请更新镜像");
    expect(softUpdateReasonText(undefined, "网络错误")).toBe(
      "暂时无法软更新（网络错误），请更新镜像",
    );
  });
});

describe("主操作选择矩阵", () => {
  it.each([
    ["软更新资格通过", status(), "soft"],
    ["没有新版本", status({ updateAvailable: false }), "none"],
    ["没有版本号", status({ latestVersion: undefined }), "none"],
    ["作业进行中", status({ job: job("downloading") }), "none"],
    ["正在重启", status({ job: job("restarting") }), "none"],
    ["作业已失败可重试", status({ job: job("failed") }), "soft"],
    [
      "已有作业原因码",
      status({ strategy: "manual", softUpdate: { available: false, reason: "job_running" } }),
      "none",
    ],
    ["试运行中", status({ runtime: runtime({ mode: "trial", trialVersion: "v0.3.0" }) }), "none"],
    [
      "需要更新镜像且侧车可用",
      status({
        strategy: "sidecar",
        softUpdate: { available: false, reason: "requires_image_update" },
      }),
      "sidecar",
    ],
    [
      "需要更新镜像且无侧车",
      status({
        strategy: "manual",
        softUpdate: { available: false, reason: "requires_image_update" },
      }),
      "manual",
    ],
    [
      "隔离版本且有侧车",
      status({ strategy: "sidecar", softUpdate: { available: false, reason: "quarantined" } }),
      "sidecar",
    ],
    [
      "隔离版本且无侧车",
      status({ strategy: "manual", softUpdate: { available: false, reason: "quarantined" } }),
      "manual",
    ],
    ["策略为 soft 但资格未通过", status({ softUpdate: { available: false } }), "manual"],
    ["UPDATE_STRATEGY=manual", status({ strategy: "manual" }), "manual"],
  ] as const)("%s → %s", (_name, value, action) => {
    expect(primaryUpdateAction(value)).toBe(action);
  });

  it("空状态没有主操作", () => {
    expect(primaryUpdateAction(null)).toBe("none");
    expect(primaryUpdateAction(undefined)).toBe("none");
  });
});

describe("运行方式、策略与结果", () => {
  it.each([
    ["unsupervised", "直接运行（不支持软更新）"],
    ["builtin", "镜像内置版本"],
    ["release", "软更新版本"],
    ["trial", "试运行中"],
    ["toString", "未知"],
  ])("运行方式 %s → %s", (mode, label) => {
    expect(runModeLabel(mode)).toBe(label);
  });

  it.each([
    ["soft", "软更新（无需重建容器）"],
    ["sidecar", "更新侧车（拉取镜像并重建容器）"],
    ["manual", "手动更新镜像"],
    [undefined, "未知"],
  ])("策略 %s → %s", (strategy, label) => {
    expect(updateStrategyLabel(strategy)).toBe(label);
  });

  it("结果色调映射到 StatusPill", () => {
    expect(resultTone("committed")).toBe("good");
    expect(resultTone("rolled_back")).toBe("critical");
    expect(resultTone("rejected")).toBe("warning");
  });

  it("结果文案区分更新、回滚与恢复内置版本", () => {
    expect(resultLabel({ outcome: "committed", action: "upgrade" })).toBe("已更新");
    expect(resultLabel({ outcome: "committed", action: "rollback" })).toBe("已回滚");
    expect(resultLabel({ outcome: "committed", action: "use-builtin" })).toBe("已恢复内置版本");
    expect(resultLabel({ outcome: "rolled_back", action: "upgrade" })).toBe("已自动回滚");
    expect(resultLabel({ outcome: "rejected", action: "upgrade" })).toBe("已拒绝切换");
  });

  it("失败阶段只显示中文，未知阶段省略", () => {
    expect(resultStageLabel("ready_timeout")).toBe("就绪超时");
    expect(resultStageLabel("healthz")).toBe("健康检查");
    expect(resultStageLabel("schema")).toBe("数据库兼容性检查");
    expect(resultStageLabel("verify")).toBe("启动复验");
    expect(resultStageLabel("start")).toBe("启动");
    expect(resultStageLabel("stability")).toBe("稳定期检查");
    expect(resultStageLabel("something_new")).toBeUndefined();
    expect(resultStageLabel(undefined)).toBeUndefined();
  });
});

describe("重启等待与结果匹配", () => {
  it("等待上限为就绪超时 + 60 秒，缺失时按 360 秒", () => {
    expect(restartWaitLimitMs(300)).toBe(360_000);
    expect(restartWaitLimitMs(120)).toBe(180_000);
    expect(restartWaitLimitMs(undefined)).toBe(360_000);
    expect(restartWaitLimitMs(0)).toBe(360_000);
    expect(restartWaitLimitMs(Number.NaN)).toBe(360_000);
  });

  it("有请求编号时只认同一请求", () => {
    expect(matchesUpdateResult(result(), { requestId: "req-1", targetVersion: "v9" })).toBe(true);
    expect(matchesUpdateResult(result(), { requestId: "req-2", targetVersion: "v0.3.0" })).toBe(
      false,
    );
    expect(matchesUpdateResult(undefined, { requestId: "req-1", targetVersion: "v0.3.0" })).toBe(
      false,
    );
  });

  it("没有请求编号时按目标版本与开始时间匹配", () => {
    expect(matchesUpdateResult(result({ at: 100 }), { targetVersion: "v0.3.0", since: 100 })).toBe(
      true,
    );
    expect(matchesUpdateResult(result({ at: 99 }), { targetVersion: "v0.3.0", since: 100 })).toBe(
      false,
    );
    expect(matchesUpdateResult(result(), { targetVersion: "v0.4.0" })).toBe(false);
    expect(matchesUpdateResult(result(), { targetVersion: "v0.3.0" })).toBe(true);
  });
});

describe("下载进度", () => {
  it("按 1024 进制显示已下载与总大小", () => {
    expect(formatDownloadProgress(12_897_485, 43_918_511)).toBe("12.3 MB / 41.9 MB");
    expect(formatDownloadProgress(512, undefined)).toBe("已下载 512 B");
    expect(downloadPercent(12_897_485, 43_918_511)).toBe(29);
  });

  it("百分比限制在 0–100，未知总量返回 null", () => {
    expect(downloadPercent(200, 100)).toBe(100);
    expect(downloadPercent(-1, 100)).toBe(0);
    expect(downloadPercent(undefined, 100)).toBe(0);
    expect(downloadPercent(10, 0)).toBeNull();
    expect(downloadPercent(10, undefined)).toBeNull();
  });
});

describe("状态补齐", () => {
  it("旧版后端只有 autoUpdateConfigured 时按侧车或手动处理", () => {
    const legacy = { current: status().current, enabled: false, updateAvailable: false };
    expect(normalizePanelUpdateStatus({ ...legacy, autoUpdateConfigured: true })).toMatchObject({
      strategy: "sidecar",
      softUpdate: { available: false },
      runtime: {
        mode: "unsupervised",
        imageTag: "latest",
        rollback: { available: false },
        builtinRestore: { available: false },
      },
    });
    expect(normalizePanelUpdateStatus(legacy).strategy).toBe("manual");
  });

  it("新版字段原样保留", () => {
    const full = status({
      runtime: runtime({ mode: "release", imageTag: "v0.2.0", previousVersion: "v0.2.0" }),
      softUpdate: { available: true, downloadBytes: 1 },
    });
    expect(normalizePanelUpdateStatus(full)).toEqual(full);
  });
});
