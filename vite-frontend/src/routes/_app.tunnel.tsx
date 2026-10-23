import { resourceSearch } from "@/features/search/route-search";
import { createFileRoute } from "@tanstack/react-router";
import { TunnelPage } from "@/features/tunnel/page";

export const Route = createFileRoute("/_app/tunnel")({
  validateSearch: (search: Record<string, unknown>) => ({
    ...resourceSearch(search),
    // 页面专用筛选只接受现有隧道类型；非法值覆盖原始 URL，避免隐藏全部数据。
    type:
      search.type === "" ||
      search.type === "1" ||
      search.type === "2" ||
      search.type === 1 ||
      search.type === 2
        ? String(search.type)
        : undefined,
    // 手写或刷新的 ?status=0/1 会被 TanStack 解析成数字；统一为字符串，非法值恢复全部状态。
    status:
      search.status === "" ||
      search.status === "0" ||
      search.status === "1" ||
      search.status === 0 ||
      search.status === 1
        ? String(search.status)
        : undefined,
    action: search.action === "create" ? "create" : undefined,
  }),
  staticData: { title: "隧道" },
  head: () => ({ meta: [{ title: "隧道管理" }] }),
  component: TunnelPage,
});
