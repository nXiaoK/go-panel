import { createFileRoute } from "@tanstack/react-router";
import { Dashboard } from "@/features/dashboard/page";

export const Route = createFileRoute("/_app/")({
  staticData: { title: "控制台" },
  head: () => ({
    meta: [
      { title: "控制台" },
      { name: "description", content: "控制台：流量趋势、资源状态与账户配额。" },
    ],
  }),
  component: Dashboard,
});
