import { AppSidebar } from "@/components/app-sidebar";
import { AppTopbar } from "@/components/app-topbar";
import { SidebarInset, SidebarProvider } from "@/components/ui/sidebar";
import { useSession } from "@/hooks/use-session";
import { Outlet } from "@tanstack/react-router";

export function AppLayout() {
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
