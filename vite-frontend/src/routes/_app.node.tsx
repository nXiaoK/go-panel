import { createFileRoute } from "@tanstack/react-router";
import { NodePage } from "@/features/node/page";

export const Route = createFileRoute("/_app/node")({
  head: () => ({ meta: [{ title: "节点管理" }] }),
  component: NodePage,
});
