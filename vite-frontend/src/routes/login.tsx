import { createFileRoute } from "@tanstack/react-router";
import { LoginPage } from "@/features/auth/login-page";
import { redirectAuthenticated } from "@/features/auth/guard";
import { parseLoginSearch } from "@/features/auth/login-search";
export const Route = createFileRoute("/login")({
  beforeLoad: redirectAuthenticated,
  validateSearch: parseLoginSearch,
  head: () => ({ meta: [{ title: "登录" }] }),
  component: LoginPage,
});
