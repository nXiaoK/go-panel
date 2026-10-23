import { getForwardList } from "@/lib/api";
import type { Forward } from "@/lib/types";

type ForwardListFetcher = typeof getForwardList;

/** 后端 normalizeForwardListIndexes 已补齐所有 inx，顺序只以后端为准，不再读取旧本地缓存。 */
export async function loadSortedForwards(
  fetchForwardList: ForwardListFetcher = getForwardList,
  signal?: AbortSignal,
): Promise<Forward[]> {
  const list = await fetchForwardList({ signal });
  return [...list].sort((left, right) => (left.inx ?? 0) - (right.inx ?? 0));
}
