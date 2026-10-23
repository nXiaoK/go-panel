import { createFileRoute } from "@tanstack/react-router";
import { UserPage } from "@/features/user/page";

export const Route = createFileRoute("/_app/user")({
  head: () => ({ meta: [{ title: "用户管理" }] }),
  component: UserPage,
});
