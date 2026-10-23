import { readSidebarDefaultOpen } from "@/lib/sidebar-state";
import { AppSidebar } from "@/components/app-sidebar";
import { AppTopbar } from "@/components/app-topbar";
import { AppContent } from "./app-content";
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
          <AppContent>
            <Outlet />
          </AppContent>
        </SidebarInset>
      </SidebarProvider>
    </PanelStatusProvider>
  );
}
