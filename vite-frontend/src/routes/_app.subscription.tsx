import { createFileRoute } from "@tanstack/react-router";
import { SubscriptionPage } from "@/features/subscription/page";

export const Route = createFileRoute("/_app/subscription")({
  head: () => ({ meta: [{ title: "订阅管理" }] }),
  component: SubscriptionPage,
});
