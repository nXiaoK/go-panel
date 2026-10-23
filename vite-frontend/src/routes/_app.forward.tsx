import { createFileRoute } from "@tanstack/react-router";
import { ForwardPage } from "@/features/forward/page";

export const Route = createFileRoute("/_app/forward")({
  head: () => ({ meta: [{ title: "转发管理" }] }),
  component: ForwardPage,
});
