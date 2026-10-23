export type BatchResult<T> = { item: T; name: string; success: boolean; reason?: string };
export type BatchProgress<T> = { done: number; total: number; result: BatchResult<T> };
/** 并发默认4且始终限制为1–4，逐项失败不会中断其余任务；结果顺序与输入一致。 */
export async function runBatch<T>(
  items: readonly T[],
  action: (item: T) => Promise<unknown>,
  options: {
    concurrency?: number;
    nameOf?: (item: T) => string;
    onProgress?: (progress: BatchProgress<T>) => void;
  } = {},
): Promise<BatchResult<T>[]> {
  const concurrency = Math.min(4, Math.max(1, Math.floor(options.concurrency || 4)));
  const results = new Array<BatchResult<T>>(items.length);
  let cursor = 0;
  let done = 0;
  async function worker() {
    while (cursor < items.length) {
      const index = cursor++;
      const item = items[index];
      const name = options.nameOf?.(item) || `#${index + 1}`;
      let result: BatchResult<T>;
      try {
        await action(item);
        result = { item, name, success: true };
      } catch (error) {
        result = {
          item,
          name,
          success: false,
          reason: error instanceof Error ? error.message : String(error),
        };
      }
      results[index] = result;
      options.onProgress?.({ done: ++done, total: items.length, result });
    }
  }
  await Promise.all(Array.from({ length: Math.min(concurrency, items.length) }, worker));
  return results;
}
