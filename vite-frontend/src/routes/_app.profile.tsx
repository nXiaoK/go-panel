import { createFileRoute, redirect } from "@tanstack/react-router";
export const Route = createFileRoute("/_app/profile")({
  // 旧书签保留强制改密参数，并统一进入账户安全分区。
  validateSearch: (search: Record<string, unknown>) => ({
    ...(search.forcePassword === 1 || search.forcePassword === "1" ? { forcePassword: 1 } : {}),
  }),
  beforeLoad: ({ search }) => {
    throw redirect({ to: "/account", search: { tab: "security", ...search }, replace: true });
  },
});
