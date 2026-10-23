import { resourceSearch } from "@/features/search/route-search";
import { createFileRoute } from "@tanstack/react-router";
import { TunnelPage } from "@/features/tunnel/page";

export const Route = createFileRoute("/_app/tunnel")({
  validateSearch: (search: Record<string, unknown>) => ({
    ...resourceSearch(search),
    action: search.action === "create" ? "create" : undefined,
  }),
  staticData: { title: "隧道" },
  head: () => ({ meta: [{ title: "隧道管理" }] }),
  component: TunnelPage,
});
