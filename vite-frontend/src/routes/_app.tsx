import { createFileRoute } from "@tanstack/react-router";
import { AppLayout } from "@/app/app-layout";
import { requireSession } from "@/features/auth/guard";

export const Route = createFileRoute("/_app")({
  beforeLoad: requireSession,
  component: AppLayout,
});
