import { resourceSearch } from "@/features/search/route-search";
import { createFileRoute } from "@tanstack/react-router";
import { ForwardPage } from "@/features/forward/page";

export const Route = createFileRoute("/_app/forward")({
  validateSearch: resourceSearch,
  staticData: { title: "转发规则" },
  head: () => ({ meta: [{ title: "转发管理" }] }),
  component: ForwardPage,
});
