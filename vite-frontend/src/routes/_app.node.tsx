import { resourceSearch } from "@/features/search/route-search";
import { createFileRoute } from "@tanstack/react-router";
import { NodePage } from "@/features/node/page";

export const Route = createFileRoute("/_app/node")({
  validateSearch: resourceSearch,
  staticData: { title: "节点" },
  head: () => ({ meta: [{ title: "节点管理" }] }),
  component: NodePage,
});
