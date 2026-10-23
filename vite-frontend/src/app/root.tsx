import { QueryClientProvider } from "@tanstack/react-query";
import {
  HeadContent,
  Link,
  Outlet,
  useRouter,
  useRouterState,
  type ErrorComponentProps,
} from "@tanstack/react-router";
import { useEffect } from "react";

import { ConfirmProvider } from "@/components/patterns/confirm-dialog";
import { Toaster } from "@/components/ui/sonner";
import { Button } from "@/components/ui/button";
import { AlertCircle, FileQuestion } from "lucide-react";
import { SESSION_CHANGED_EVENT, SESSION_INVALIDATED_EVENT } from "@/lib/session";
import { pageTitle, useDocumentTitle } from "@/lib/title";

import { Route } from "@/routes/__root";

export function NotFoundComponent() {
  return (
    <div className="flex min-h-svh items-center justify-center bg-canvas p-4 text-center">
      <div className="max-w-md">
        <FileQuestion className="mx-auto size-5 text-mute" aria-hidden="true" />
        <h1 className="mt-4 text-page-title text-ink">页面不存在</h1>
        <p className="mt-3 text-sm leading-5 text-mute">你要访问的页面不存在或已被移除</p>
        <Button variant="outline" asChild className="mt-6">
          <Link to="/">返回控制台</Link>
        </Button>
      </div>
    </div>
  );
}
export function ErrorComponent({ error, reset }: ErrorComponentProps) {
  const router = useRouter();
  return (
    <div className="flex min-h-svh items-center justify-center bg-canvas p-4">
      <div className="w-full max-w-md text-center">
        <AlertCircle className="mx-auto size-5 text-status-critical-text" aria-hidden="true" />
        <h1 className="mt-4 text-page-title text-ink">页面出现错误</h1>
        <p className="mt-3 text-sm leading-5 text-mute">请重试，或返回控制台</p>
        <div className="mt-6 flex justify-center gap-2">
          <Button
            type="button"
            onClick={() => {
              void router.invalidate();
              reset();
            }}
          >
            重试
          </Button>
          <Button variant="outline" asChild>
            <a href="/">返回控制台</a>
          </Button>
        </div>
        {import.meta.env.DEV && (
          <details className="mt-6 text-left text-caption text-mute">
            <summary className="cursor-pointer">错误详情（开发环境）</summary>
            <pre className="mt-2 max-h-64 overflow-auto whitespace-pre-wrap break-all font-mono">
              {error instanceof Error ? error.stack || error.message : String(error)}
            </pre>
          </details>
        )}
      </div>
    </div>
  );
}

export function RootComponent() {
  const { queryClient } = Route.useRouteContext();
  const router = useRouter();
  const currentTitle = useRouterState({ select: (state) => pageTitle(state.matches) });
  useDocumentTitle(currentTitle);
  useEffect(() => {
    const clearDataCaches = () => {
      queryClient.clear();
      void router.invalidate();
    };
    window.addEventListener(SESSION_CHANGED_EVENT, clearDataCaches);
    window.addEventListener(SESSION_INVALIDATED_EVENT, clearDataCaches);
    return () => {
      window.removeEventListener(SESSION_CHANGED_EVENT, clearDataCaches);
      window.removeEventListener(SESSION_INVALIDATED_EVENT, clearDataCaches);
    };
  }, [queryClient, router]);

  return (
    <QueryClientProvider client={queryClient}>
      <HeadContent />
      <ConfirmProvider>
        <Outlet />
      </ConfirmProvider>
      <Toaster richColors position="top-right" />
    </QueryClientProvider>
  );
}
