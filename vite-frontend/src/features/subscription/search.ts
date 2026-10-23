/** 标签与筛选只接受可显示的值；保留其他书签参数，数字状态兼容直接输入 URL。 */
export function subscriptionSearch(search: Record<string, unknown>) {
  return {
    ...search,
    tab: search.tab === "nodes" || search.tab === "report" ? search.tab : "profiles",
    q:
      typeof search.q === "string" || (typeof search.q === "number" && Number.isFinite(search.q))
        ? String(search.q)
        : "",
    protocol: typeof search.protocol === "string" ? search.protocol.toLowerCase() : "",
    status:
      search.status === 0 || search.status === "0"
        ? "0"
        : search.status === 1 || search.status === "1"
          ? "1"
          : "",
    relay: search.relay === "yes" || search.relay === "no" ? search.relay : "",
  };
}
