import { createFileRoute } from "@tanstack/react-router";
import { LimitPage } from "@/features/limit/page";

export const Route = createFileRoute("/_app/limit")({
  head: () => ({ meta: [{ title: "限速策略" }] }),
  component: LimitPage,
});
