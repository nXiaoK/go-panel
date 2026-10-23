import { createFileRoute } from "@tanstack/react-router";
import { SettingsPage } from "@/features/account/settings-page";

export const Route = createFileRoute("/_app/settings")({
  head: () => ({ meta: [{ title: "面板设置" }] }),
  component: SettingsPage,
});
