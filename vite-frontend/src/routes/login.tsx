import { createFileRoute } from "@tanstack/react-router";
import { LoginPage } from "@/features/auth/page";

export const Route = createFileRoute("/login")({
  // 仅接受会话失效和账号禁用两个提示原因；未提供或未知值时沿用普通登录界面。
  validateSearch: (search: Record<string, unknown>): { reason?: "expired" | "disabled" } =>
    search.reason === "expired" || search.reason === "disabled" ? { reason: search.reason } : {},
  head: () => ({ meta: [{ title: "登录" }] }),
  component: LoginPage,
});
