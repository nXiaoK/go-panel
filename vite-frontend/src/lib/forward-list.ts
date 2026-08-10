import { getForwardList } from "@/lib/api";
import { listData } from "@/lib/api/query";
import type { Forward } from "@/lib/types";

type ForwardListFetcher = typeof getForwardList;

/**
 * Fetch the forward list exactly once and apply the persisted ordering.
 * Backend `inx` wins; the local order is only a compatibility fallback for
 * installations that have never stored a server-side order.
 */
export async function loadSortedForwards(
  fetchForwardList: ForwardListFetcher = getForwardList,
): Promise<Forward[]> {
  const res = await fetchForwardList();
  const list = listData<Forward>(res);
  const anyInx = list.some((forward) => (forward.inx ?? 0) > 0);

  if (!anyInx && typeof window !== "undefined") {
    const raw = window.localStorage.getItem("forward-order");
    if (raw) {
      try {
        const order: number[] = JSON.parse(raw);
        const position = new Map(order.map((id, index) => [id, index]));
        return [...list].sort(
          (left, right) =>
            (position.get(left.id) ?? Number.MAX_SAFE_INTEGER) -
            (position.get(right.id) ?? Number.MAX_SAFE_INTEGER),
        );
      } catch {
        // 本地兼容顺序损坏时忽略，继续使用后端默认顺序。
      }
    }
  }

  return [...list].sort((left, right) => (left.inx ?? 0) - (right.inx ?? 0));
}
