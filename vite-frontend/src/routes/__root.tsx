import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  Outlet,
  Link,
  HeadContent,
  createRootRouteWithContext,
  useRouter,
} from "@tanstack/react-router";
import { useEffect, useState } from "react";

import { reportLovableError } from "../lib/lovable-error-reporting";
import { initTheme } from "../lib/theme";
import { Toaster } from "@/components/ui/sonner";
import { PANEL_ADDRESS_CHANGED_EVENT } from "@/lib/api/network";
import { getGlobalSearchCache } from "@/lib/search-cache";
import { SESSION_CHANGED_EVENT, SESSION_INVALIDATED_EVENT } from "@/lib/session";

function NotFoundComponent() {
  return (
    <div className="flex min-h-screen items-center justify-center bg-background px-4">
      <div className="max-w-md text-center">
        <p className="font-mono text-xs uppercase tracking-[0.3em] text-primary">
          404 · path not found
        </p>
        <h1 className="mt-4 text-4xl font-semibold tracking-tight text-foreground">
          此路径未在面板中注册
        </h1>
        <p className="mt-3 text-sm text-muted-foreground">你要访问的页面不存在或已被移除。</p>
        <div className="mt-6">
          <Link
            to="/"
            className="inline-flex items-center justify-center rounded-md bg-gradient-primary px-4 py-2 text-sm font-medium text-primary-foreground shadow-glow transition hover:opacity-90"
          >
            返回控制台
          </Link>
        </div>
      </div>
    </div>
  );
}

function ErrorComponent({ error, reset }: { error: Error; reset: () => void }) {
  console.error(error);
  const router = useRouter();
  useEffect(() => {
    reportLovableError(error, { boundary: "tanstack_root_error_component" });
  }, [error]);

  return (
    <div className="flex min-h-screen items-center justify-center bg-background px-4">
      <div className="max-w-md text-center">
        <p className="font-mono text-xs uppercase tracking-[0.3em] text-destructive">
          runtime error
        </p>
        <h1 className="mt-4 text-2xl font-semibold tracking-tight text-foreground">页面渲染失败</h1>
        <p className="mt-2 text-sm text-muted-foreground">请稍后重试，或返回控制台。</p>
        <div className="mt-6 flex flex-wrap justify-center gap-2">
          <button
            onClick={() => {
              router.invalidate();
              reset();
            }}
            className="inline-flex items-center justify-center rounded-md bg-gradient-primary px-4 py-2 text-sm font-medium text-primary-foreground shadow-glow transition hover:opacity-90"
          >
            重试
          </button>
          <a
            href="/"
            className="inline-flex items-center justify-center rounded-md border border-border bg-card px-4 py-2 text-sm font-medium text-foreground transition hover:bg-accent"
          >
            返回控制台
          </a>
        </div>
      </div>
    </div>
  );
}

export const Route = createRootRouteWithContext<{ queryClient: QueryClient }>()({
  head: () => ({
    meta: [
      { charSet: "utf-8" },
      { name: "viewport", content: "width=device-width, initial-scale=1" },
      { title: "Flux Panel — 转发管理控制台" },
      {
        name: "description",
        content: "Flux Panel 深色科技风控制台：隧道 · 转发 · 节点 · 用户 · 订阅一站式管理。",
      },
      { property: "og:title", content: "Flux Panel — 转发管理控制台" },
      {
        property: "og:description",
        content: "Flux Panel 深色科技风控制台：隧道 · 转发 · 节点 · 用户 · 订阅一站式管理。",
      },
      { property: "og:type", content: "website" },
      { name: "twitter:card", content: "summary_large_image" },
    ],
    links: [{ rel: "icon", href: "/favicon.ico", type: "image/x-icon" }],
  }),
  component: RootComponent,
  notFoundComponent: NotFoundComponent,
  errorComponent: ErrorComponent,
});

function RootComponent() {
  const { queryClient } = Route.useRouteContext();
  const [themeMode, setThemeMode] = useState<"light" | "dark">(() =>
    typeof document !== "undefined" && document.documentElement.classList.contains("light")
      ? "light"
      : "dark",
  );

  useEffect(() => {
    initTheme();
    const sync = () =>
      setThemeMode(document.documentElement.classList.contains("dark") ? "dark" : "light");
    sync();
    window.addEventListener("themechange", sync);
    return () => window.removeEventListener("themechange", sync);
  }, []);

  useEffect(() => {
    const clearDataCaches = () => {
      queryClient.clear();
      getGlobalSearchCache().clear();
    };
    window.addEventListener(SESSION_CHANGED_EVENT, clearDataCaches);
    window.addEventListener(SESSION_INVALIDATED_EVENT, clearDataCaches);
    window.addEventListener(PANEL_ADDRESS_CHANGED_EVENT, clearDataCaches);
    return () => {
      window.removeEventListener(SESSION_CHANGED_EVENT, clearDataCaches);
      window.removeEventListener(SESSION_INVALIDATED_EVENT, clearDataCaches);
      window.removeEventListener(PANEL_ADDRESS_CHANGED_EVENT, clearDataCaches);
    };
  }, [queryClient]);

  return (
    <QueryClientProvider client={queryClient}>
      <HeadContent />
      <Outlet />
      <Toaster richColors theme={themeMode} position="top-right" />
    </QueryClientProvider>
  );
}
