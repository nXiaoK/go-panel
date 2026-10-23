import { readSidebarDefaultOpen } from "@/lib/sidebar-state";
import { AppSidebar } from "@/components/app-sidebar";
import { AppTopbar } from "@/components/app-topbar";
import { SidebarInset, SidebarProvider } from "@/components/ui/sidebar";
import { PanelStatusProvider } from "@/features/system/panel-status";
import { useSession } from "@/hooks/use-session";
import { Outlet } from "@tanstack/react-router";

export function AppLayout() {
  const { token } = useSession();
  if (!token) return null;
  return (
    <PanelStatusProvider>
      <SidebarProvider defaultOpen={readSidebarDefaultOpen()}>
        <AppSidebar />
        <SidebarInset className="min-h-svh min-w-0 bg-canvas">
          <AppTopbar />
          <main className="min-w-0 flex-1 overflow-x-hidden">
            <div className="mx-auto w-full max-w-(--content-max-width) p-4 md:p-6">
              <Outlet />
            </div>
          </main>
        </SidebarInset>
      </SidebarProvider>
    </PanelStatusProvider>
  );
}
