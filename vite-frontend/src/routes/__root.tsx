import { createRootRouteWithContext } from "@tanstack/react-router";
import type { QueryClient } from "@tanstack/react-query";
import { RootComponent, NotFoundComponent, ErrorComponent } from "@/app/root";
import { rootHead } from "@/app/root-meta";

export const Route = createRootRouteWithContext<{ queryClient: QueryClient }>()({
  head: rootHead,
  component: RootComponent,
  notFoundComponent: NotFoundComponent,
  errorComponent: ErrorComponent,
});
