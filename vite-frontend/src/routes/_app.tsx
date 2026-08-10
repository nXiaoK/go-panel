import { createFileRoute, Outlet, redirect } from "@tanstack/react-router";
import { SidebarInset, SidebarProvider } from "@/components/ui/sidebar";
import { AppSidebar } from "@/components/app-sidebar";
import { AppTopbar } from "@/components/app-topbar";
import { canAccessPath } from "@/lib/capabilities";

export const Route = createFileRoute("/_app")({
  beforeLoad: ({ location }) => {
    if (typeof window === "undefined") return;
    const token = window.localStorage.getItem("token");
    if (!token) {
      throw redirect({ to: "/login" });
    }
    // The backend stays authoritative; this only stops a normal user from
    // opening an administrator route they can never use.
    const rawRole = window.localStorage.getItem("role_id");
    const roleID = rawRole === null || rawRole === "" ? null : Number(rawRole);
    if (!canAccessPath(roleID, location.pathname)) {
      throw redirect({ to: "/" });
    }
  },
  component: AppLayout,
});

function AppLayout() {
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
