import { createFileRoute } from "@tanstack/react-router";
import { Dashboard } from "@/features/dashboard/page";

export const Route = createFileRoute("/_app/")({
  staticData: { title: "控制台" },
  head: () => ({
    meta: [
      { title: "控制台" },
      { name: "description", content: "控制台：实时流量、节点状态与最近活动。" },
    ],
  }),
  component: Dashboard,
});
