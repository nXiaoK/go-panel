import { createFileRoute } from "@tanstack/react-router";
import { subscriptionSearch } from "@/features/subscription/search";
import { SubscriptionPage } from "@/features/subscription/page";

export const Route = createFileRoute("/_app/subscription")({
  validateSearch: subscriptionSearch,
  staticData: { title: "订阅" },
  head: () => ({ meta: [{ title: "订阅管理" }] }),
  component: SubscriptionPage,
});
