import { createFileRoute } from "@tanstack/react-router";
import { AccountPage } from "@/features/account/account-page";

export const Route = createFileRoute("/_app/account")({
  // 账户标签默认资料与安全；强制改密始终进入安全标签，持久标记仍由会话守卫裁决。
  validateSearch: (search: Record<string, unknown>) => ({
    tab:
      search.tab === "preferences" || search.tab === "security"
        ? search.tab
        : ("security" as "security" | "preferences"),
    ...(search.forcePassword === 1 || search.forcePassword === "1" ? { forcePassword: 1 } : {}),
  }),
  head: () => ({ meta: [{ title: "账户" }] }),
  staticData: { title: "账户" },
  component: AccountPage,
});
