import { createFileRoute } from "@tanstack/react-router";
import { ProfilePage } from "@/features/account/page";

export const Route = createFileRoute("/_app/profile")({
  // forcePassword=1 仅开启改密引导；默认不强制，真实权限始终由后端与持久会话标记判定。
  validateSearch: (search: Record<string, unknown>): { forcePassword?: number } =>
    search.forcePassword === 1 || search.forcePassword === "1" ? { forcePassword: 1 } : {},
  head: () => ({ meta: [{ title: "个人资料" }] }),
  component: ProfilePage,
});
