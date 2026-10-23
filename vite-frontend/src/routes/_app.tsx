import { createFileRoute, Outlet, redirect } from "@tanstack/react-router";
import { SidebarInset, SidebarProvider } from "@/components/ui/sidebar";
import { AppSidebar } from "@/components/app-sidebar";
import { AppTopbar } from "@/components/app-topbar";
import { passwordChangeRedirect, readSession } from "@/lib/session";
import { useSession } from "@/hooks/use-session";
import { canAccessPath } from "@/lib/capabilities";

export const Route = createFileRoute("/_app")({
  beforeLoad: ({ location }) => {
    if (typeof window === "undefined") return;
    const { token, roleId } = readSession();
    if (!token) {
      throw redirect({ to: "/login" });
    }
    // 持久改密要求优先于角色导航；资料页允许访问，刷新或手动输入其他路径也无法绕过。
    if (passwordChangeRedirect(location.pathname)) {
      throw redirect({ to: "/profile", search: { forcePassword: 1 } });
    }
    // The backend stays authoritative; this only stops a normal user from
    // opening an administrator route they can never use.
    if (!canAccessPath(roleId, location.pathname)) {
      throw redirect({ to: "/" });
    }
  },
  component: AppLayout,
});

function AppLayout() {
  const { token } = useSession();
  // 退出/失效到路由守卫完成导航之间立即卸载业务树，避免空令牌重建查询。
  if (!token) return null;
  return (
    <SidebarProvider>
      <div className="flex min-h-screen w-full bg-background text-foreground">
        <AppSidebar />
        <SidebarInset className="flex min-h-screen flex-1 flex-col bg-transparent">
          <AppTopbar />
          <main className="flex-1 overflow-x-hidden">
            <div className="mx-auto w-full max-w-[1600px] p-6 lg:p-8">
              <Outlet />
            </div>
          </main>
        </SidebarInset>
      </div>
    </SidebarProvider>
  );
}
