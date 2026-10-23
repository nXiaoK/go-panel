import { createFileRoute } from "@tanstack/react-router";
import { LimitPage } from "@/features/limit/page";

export const Route = createFileRoute("/_app/limit")({
  staticData: { title: "限速" },
  head: () => ({ meta: [{ title: "限速策略" }] }),
  component: LimitPage,
});
