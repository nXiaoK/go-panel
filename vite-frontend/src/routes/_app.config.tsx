import { createFileRoute } from "@tanstack/react-router";
import { ConfigPage } from "@/features/config/page";

export const Route = createFileRoute("/_app/config")({
  staticData: { title: "系统配置" },
  head: () => ({ meta: [{ title: "系统配置" }] }),
  component: ConfigPage,
});
