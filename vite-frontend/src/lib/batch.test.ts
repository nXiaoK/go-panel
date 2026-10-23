import { expect, it, vi } from "vitest";
import { runBatch } from "./batch";
it("批量最多4并发、按输入顺序返回并保留部分失败与进度", async () => {
  let active = 0,
    max = 0;
  const progress = vi.fn();
  const results = await runBatch(
    [1, 2, 3, 4, 5, 6],
    async (item) => {
      active++;
      max = Math.max(max, active);
      await new Promise((resolve) => setTimeout(resolve, 7 - item));
      active--;
      if (item === 2) throw new Error("禁止操作");
    },
    { concurrency: 99, nameOf: (item) => `资源${item}`, onProgress: progress },
  );
  expect(max).toBe(4);
  expect(results.map((x) => x.item)).toEqual([1, 2, 3, 4, 5, 6]);
  expect(results[1]).toMatchObject({ name: "资源2", success: false, reason: "禁止操作" });
  expect(progress.mock.calls.map(([value]) => value.done)).toEqual([1, 2, 3, 4, 5, 6]);
  expect(progress.mock.lastCall?.[0]).toMatchObject({ done: 6, total: 6 });
});
it("空列表不发请求，串行配置仍可用", async () => {
  const action = vi.fn();
  expect(await runBatch([], action)).toEqual([]);
  expect(action).not.toHaveBeenCalled();
  let active = 0,
    max = 0;
  await runBatch(
    [1, 2],
    async () => {
      active++;
      max = Math.max(max, active);
      await Promise.resolve();
      active--;
    },
    { concurrency: 1 },
  );
  expect(max).toBe(1);
});
