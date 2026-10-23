import { resourceSearch } from "@/features/search/route-search";

/** 转发页 URL：数字隧道 ID 保持数字以生成简洁地址；筛选和排序只接受当前页面支持的取值。 */
export function forwardSearch(search: Record<string, unknown>) {
  const common = resourceSearch(search);
  const tunnelId =
    typeof search.tunnel === "number" || typeof search.tunnel === "string"
      ? Number(search.tunnel)
      : NaN;
  const status = String(search.status ?? "all");
  const sort = typeof search.sort === "string" ? search.sort : "";
  return {
    ...common,
    ...("tunnel" in search
      ? { tunnel: Number.isSafeInteger(tunnelId) && tunnelId > 0 ? tunnelId : "all" }
      : {}),
    ...("status" in search
      ? { status: ["all", "1", "0", "error"].includes(status) ? status : "all" }
      : {}),
    ...("sort" in search ? { sort: /^(name|traffic)\.(asc|desc)$/.test(sort) ? sort : "" } : {}),
  };
}
