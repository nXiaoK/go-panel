import { nodeSearch } from "@/features/node/model";
import { createFileRoute } from "@tanstack/react-router";
import { NodePage } from "@/features/node/page";

export const Route = createFileRoute("/_app/node")({
  validateSearch: nodeSearch,
  staticData: { title: "节点" },
  head: () => ({ meta: [{ title: "节点管理" }] }),
  component: NodePage,
});
