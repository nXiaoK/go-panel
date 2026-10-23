import { useConfigs } from "@/features/config/queries";
import { usePanelStatus } from "@/features/system/panel-status-context";
import { useSession } from "@/hooks/use-session";
import { Link, useRouterState } from "@tanstack/react-router";
import { ArrowUpCircle, Zap } from "lucide-react";
import { useEffect, useState } from "react";
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarTrigger,
  useSidebar,
} from "@/components/ui/sidebar";
import { StatusPill } from "@/components/patterns/status-pill";
import { SystemUpdateDialog } from "@/features/system/components/system-update-dialog";
import { checkPanelUpdate, getSystemVersion } from "@/lib/api";
import { accountNavigation, navigationForRole } from "@/lib/navigation";
import { appNameFromConfigs, getCachedAppName, setCachedAppName } from "@/lib/site-config";
import type { BuildInfo, PanelUpdateStatus } from "@/lib/types";
import { displayVersion, shouldPromptForUpdate } from "@/lib/update";

export function AppSidebar() {
  const { state, isMobile, setOpenMobile } = useSidebar();
  const { mustChangePassword, isAdmin, roleId } = useSession();
  const collapsed = state === "collapsed" && !isMobile;
  const currentPath = useRouterState({ select: (router) => router.location.pathname });
  const [appName, setAppName] = useState(getCachedAppName);
  const configs = useConfigs();
  const { failed, uptimeSeconds } = usePanelStatus();
  const [buildInfo, setBuildInfo] = useState<BuildInfo | null>(null);
  const [updateStatus, setUpdateStatus] = useState<PanelUpdateStatus | null>(null);
  const [updateDialogOpen, setUpdateDialogOpen] = useState(false);
  const groups = mustChangePassword
    ? [{ label: "账户", items: [accountNavigation] }]
    : navigationForRole(roleId);
  if (!isAdmin && !mustChangePassword) groups.push({ label: "账户", items: [accountNavigation] });
  useEffect(() => {
    const update = () => setAppName(getCachedAppName());
    window.addEventListener("site-config-updated", update);
    return () => window.removeEventListener("site-config-updated", update);
  }, []);
  useEffect(() => {
    if (!configs.data) return;
    const next = appNameFromConfigs(configs.data);
    setCachedAppName(next);
    setAppName(next);
  }, [configs.data]);
  useEffect(() => {
    if (mustChangePassword || !isAdmin) return;
    let alive = true;
    const controller = new AbortController();
    getSystemVersion({ signal: controller.signal })
      .then((response) => {
        if (alive) setBuildInfo(response);
      })
      .catch(() => undefined);
    const loadUpdate = () => {
      if (document.visibilityState === "hidden") return;
      checkPanelUpdate({ signal: controller.signal })
        .then((response) => {
          if (!alive) return;
          setUpdateStatus(response);
          setBuildInfo(response.current);
          if (response.updateAvailable && shouldPromptForUpdate(response.latestVersion))
            setUpdateDialogOpen(true);
        })
        .catch(() => undefined);
    };
    loadUpdate();
    // 升级状态沿用后端 GitHub 缓存，管理员每 30 分钟读取；普通用户与强制改密不发请求。
    const timer = window.setInterval(loadUpdate, 30 * 60_000);
    return () => {
      alive = false;
      controller.abort();
      window.clearInterval(timer);
    };
  }, [isAdmin, mustChangePassword]);
  return (
    <Sidebar collapsible="icon" className="border-r border-hairline">
      <SidebarHeader className="border-b border-hairline p-2">
        <div className="flex min-h-12 items-center gap-2 px-2">
          <Zap className="size-6 shrink-0 text-primary" aria-hidden="true" />
          {!collapsed && (
            <div className="min-w-0 flex-1">
              <span className="block truncate text-body-strong text-ink">{appName}</span>
              {isAdmin && !mustChangePassword && (
                <div className="flex flex-wrap items-center gap-1.5 text-caption text-mute">
                  <span
                    className="font-mono"
                    title={buildInfo?.commit ? `提交 ${buildInfo.commit}` : undefined}
                  >
                    {displayVersion(buildInfo?.version, buildInfo?.commit)}
                  </span>
                  {updateStatus?.updateAvailable && (
                    <button
                      type="button"
                      aria-label="打开面板升级"
                      onClick={() => setUpdateDialogOpen(true)}
                      className="flex items-center rounded-pill max-md:min-h-10"
                    >
                      <StatusPill tone="warning" label="可更新" icon={ArrowUpCircle} />
                    </button>
                  )}
                </div>
              )}
            </div>
          )}
        </div>
      </SidebarHeader>
      <SidebarContent className="gap-1 py-2">
        {groups.map((group) => (
          <SidebarGroup key={group.label}>
            {!collapsed && (
              <SidebarGroupLabel className="text-caption text-mute">
                {group.label}
              </SidebarGroupLabel>
            )}
            <SidebarGroupContent>
              <SidebarMenu>
                {group.items.map((item) => {
                  const active =
                    item.url === "/"
                      ? currentPath === "/"
                      : currentPath === item.url || currentPath.startsWith(item.url + "/");
                  return (
                    <SidebarMenuItem key={item.url}>
                      <SidebarMenuButton
                        asChild
                        isActive={active}
                        tooltip={item.title}
                        className="relative h-9 max-md:h-10 data-[active=true]:bg-surface-hover data-[active=true]:text-ink"
                      >
                        <Link
                          to={item.url}
                          onClick={() => setOpenMobile(false)}
                          aria-current={active ? "page" : undefined}
                        >
                          {active && (
                            <span
                              className="absolute left-0 top-1/2 h-5 w-0.5 -translate-y-1/2 bg-primary"
                              aria-hidden="true"
                            />
                          )}
                          <item.icon className={active ? "text-primary" : "text-mute"} />
                          <span>{item.title}</span>
                        </Link>
                      </SidebarMenuButton>
                    </SidebarMenuItem>
                  );
                })}
              </SidebarMenu>
            </SidebarGroupContent>
          </SidebarGroup>
        ))}
      </SidebarContent>
      <SidebarFooter className="border-t border-hairline">
        {isAdmin && !mustChangePassword && (
          <div className={collapsed ? "flex justify-center py-2" : "space-y-2 px-2 py-2"}>
            {collapsed ? (
              <span
                role="img"
                aria-label={failed ? "连接异常" : "正常"}
                title={failed ? "连接异常" : "正常"}
                className={`size-2 rounded-full ${failed ? "bg-status-critical" : "bg-status-good"}`}
              />
            ) : (
              <StatusPill
                tone={failed ? "critical" : "good"}
                label={failed ? "连接异常" : "正常"}
              />
            )}
            {!collapsed && (
              <p className="text-caption text-mute">
                面板已运行{" "}
                {uptimeSeconds === null
                  ? "—"
                  : `${Math.floor(uptimeSeconds / 86400)} 天 ${Math.floor((uptimeSeconds % 86400) / 3600)} 小时`}
              </p>
            )}
          </div>
        )}
        <SidebarTrigger className="hidden self-end md:inline-flex" />
      </SidebarFooter>
      {isAdmin && !mustChangePassword && (
        <SystemUpdateDialog
          open={updateDialogOpen}
          status={updateStatus}
          onOpenChange={setUpdateDialogOpen}
        />
      )}
    </Sidebar>
  );
}
