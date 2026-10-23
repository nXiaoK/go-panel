import { createFileRoute } from "@tanstack/react-router";
import { TunnelPage } from "@/features/tunnel/page";

export const Route = createFileRoute("/_app/tunnel")({
  validateSearch: (search: Record<string, unknown>) => ({
    action: search.action === "create" ? "create" : undefined,
  }),
  head: () => ({ meta: [{ title: "隧道管理" }] }),
  component: TunnelPage,
});
