import { resourceSearch } from "@/features/search/route-search";
import { createFileRoute } from "@tanstack/react-router";
import { UserPage } from "@/features/user/page";

export const Route = createFileRoute("/_app/user")({
  validateSearch: resourceSearch,
  staticData: { title: "用户" },
  head: () => ({ meta: [{ title: "用户管理" }] }),
  component: UserPage,
});
