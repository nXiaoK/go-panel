import { resourceSearch } from "@/features/search/route-search";
import { createFileRoute } from "@tanstack/react-router";
import { UserPage } from "@/features/user/page";
import { userSearch } from "@/features/user/model";

export const Route = createFileRoute("/_app/user")({
  validateSearch: (search) => resourceSearch(userSearch(search)),
  staticData: { title: "用户" },
  head: () => ({ meta: [{ title: "用户管理" }] }),
  component: UserPage,
});
