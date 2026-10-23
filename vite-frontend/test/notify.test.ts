import { beforeEach, describe, expect, it, vi } from "vitest";
import { notify } from "@/lib/notify";
import { setPreference } from "@/lib/preferences";
const toast = vi.hoisted(() => ({
  success: vi.fn(),
  info: vi.fn(),
  error: vi.fn(),
  warning: vi.fn(),
  loading: vi.fn(() => "loading-id"),
  dismiss: vi.fn(),
}));
vi.mock("sonner", () => ({ toast }));
beforeEach(() => {
  localStorage.clear();
  vi.clearAllMocks();
});
describe("操作通知偏好", () => {
  it("关闭偏好后屏蔽成功和信息，错误与警告始终显示并保留操作按钮", () => {
    setPreference("notify", false);
    const options = { action: { label: "重试", onClick: vi.fn() } };
    notify.success("已保存");
    notify.info("消息");
    notify.error("失败", options);
    notify.warning("警告");
    expect(toast.success).not.toHaveBeenCalled();
    expect(toast.info).not.toHaveBeenCalled();
    expect(toast.error).toHaveBeenCalledWith("失败", options);
    expect(toast.warning).toHaveBeenCalledWith("警告");
    setPreference("notify", true);
    notify.success("恢复");
    notify.info("消息");
    expect(toast.success).toHaveBeenCalledWith("恢复");
    expect(toast.info).toHaveBeenCalledOnce();
  });
  it("promise显示进行中到成功，返回原始结果并清理加载状态", async () => {
    const operation = vi.fn(async () => ({ version: "v2" }));
    const result = await notify.promise(operation, {
      loading: "升级中",
      success: (value) => `已升级${value.version}`,
      error: "升级失败",
    });
    expect(result).toEqual({ version: "v2" });
    expect(operation).toHaveBeenCalledOnce();
    expect(toast.loading).toHaveBeenCalledWith("升级中");
    expect(toast.success).toHaveBeenCalledWith("已升级v2");
    expect(toast.dismiss).toHaveBeenCalledWith("loading-id");
  });
  it("关闭偏好时promise无加载或成功，但失败显示且保留原始rejection", async () => {
    setPreference("notify", false);
    await notify.promise(Promise.resolve(42), { loading: "等待", success: "成功", error: "失败" });
    const failure = new Error("连接断开");
    await expect(
      notify.promise(Promise.reject(failure), {
        loading: "等待",
        success: "成功",
        error: (error) => (error as Error).message,
      }),
    ).rejects.toBe(failure);
    expect(toast.loading).not.toHaveBeenCalled();
    expect(toast.success).not.toHaveBeenCalled();
    expect(toast.dismiss).not.toHaveBeenCalled();
    expect(toast.error).toHaveBeenCalledWith("连接断开");
  });
  it("操作执行中关闭偏好会抑制完成提示，同步抛错也不会遗留加载提示", async () => {
    let done!: (value: number) => void;
    const pending = notify.promise(
      new Promise<number>((resolve) => {
        done = resolve;
      }),
      { loading: "等待", success: "成功", error: "失败" },
    );
    setPreference("notify", false);
    done(1);
    await pending;
    expect(toast.success).not.toHaveBeenCalled();
    expect(toast.dismiss).toHaveBeenCalledOnce();
    setPreference("notify", true);
    await expect(
      notify.promise(
        () => {
          throw new Error("同步错误");
        },
        { loading: "等待", success: "成功", error: "失败" },
      ),
    ).rejects.toThrow("同步错误");
    expect(toast.error).toHaveBeenCalledWith("失败");
    expect(toast.dismiss).toHaveBeenCalledTimes(2);
  });
});
