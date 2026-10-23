import { useConfigs } from "@/features/config/queries";
import { useSystemStatus } from "@/features/system/queries";
import { useSession } from "@/hooks/use-session";
import { Link, useNavigate, useRouterState } from "@tanstack/react-router";
import { LogOut, RefreshCw, Zap } from "lucide-react";
import { useEffect, useMemo, useState } from "react";

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
  useSidebar,
} from "@/components/ui/sidebar";
import { SystemUpdateDialog } from "@/features/system/components/system-update-dialog";
import { checkPanelUpdate, getSystemVersion } from "@/lib/api";
import { formatDuration } from "@/lib/format";
import { navigationForRole } from "@/lib/navigation";
import { clearSession } from "@/lib/session";
import { appNameFromConfigs, getCachedAppName, setCachedAppName } from "@/lib/site-config";
import type { BuildInfo, PanelUpdateStatus } from "@/lib/types";
import { displayVersion, shouldPromptForUpdate } from "@/lib/update";

export function AppSidebar() {
  const { state } = useSidebar();
  const navigate = useNavigate();
  const { mustChangePassword: forcePassword, isAdmin: administrator, roleId } = useSession();
  const collapsed = state === "collapsed";
  const currentPath = useRouterState({ select: (r) => r.location.pathname });
  const [appName, setAppName] = useState(getCachedAppName);
  const configsQuery = useConfigs();
  const statusQuery = useSystemStatus();
  const systemOnline = !statusQuery.isError;
  const [serverStartedAt, setServerStartedAt] = useState<number | null>(null);
  const [uptimeSeconds, setUptimeSeconds] = useState(0);
  const [buildInfo, setBuildInfo] = useState<BuildInfo | null>(null);
  const [updateStatus, setUpdateStatus] = useState<PanelUpdateStatus | null>(null);
  const [updateDialogOpen, setUpdateDialogOpen] = useState(false);
  // 按角色过滤导航：普通用户不显示管理员专属入口（后端仍是最终权限裁决方）。
  const navGroups = useMemo(() => {
    const groups = navigationForRole(roleId);
    return forcePassword
      ? groups
          .map((group) => ({
            ...group,
            items: group.items.filter((item) => item.url === "/profile"),
          }))
          .filter((group) => group.items.length)
      : groups;
  }, [forcePassword, roleId]);
  const isActive = (url: string) =>
    url === "/" ? currentPath === "/" : currentPath === url || currentPath.startsWith(url + "/");

  useEffect(() => {
    if (forcePassword) return;
    const onSiteConfig = (event: Event) => {
      const next = (event as CustomEvent<{ appName?: string }>).detail?.appName;
      setAppName(next || getCachedAppName());
    };
    window.addEventListener("site-config-updated", onSiteConfig);
    return () => {
      window.removeEventListener("site-config-updated", onSiteConfig);
    };
  }, [forcePassword]);

  useEffect(() => {
    if (forcePassword || !administrator) return;
    let alive = true;
    getSystemVersion()
      .then((response) => {
        if (alive) setBuildInfo(response);
      })
      .catch(() => undefined);
    const loadUpdate = () => {
      checkPanelUpdate()
        .then((response) => {
          if (!alive) return;
          setUpdateStatus(response);
          setBuildInfo(response.current);
          if (response.updateAvailable && shouldPromptForUpdate(response.latestVersion)) {
            setUpdateDialogOpen(true);
          }
        })
        .catch(() => undefined);
    };
    loadUpdate();
    // 后端自行控制 GitHub 请求缓存；前端每 30 分钟读取一次状态，长期开页也能收到提示。
    const timer = window.setInterval(loadUpdate, 30 * 60_000);
    return () => {
      alive = false;
      window.clearInterval(timer);
    };
  }, [administrator, forcePassword]);

  useEffect(() => {
    if (forcePassword || !configsQuery.data) return;
    const next = appNameFromConfigs(configsQuery.data);
    setCachedAppName(next);
    setAppName(next);
  }, [forcePassword, configsQuery.data]);
  useEffect(() => {
    if (forcePassword || !statusQuery.data) return;
    const startedAt = Number(statusQuery.data.startedAt || 0);
    setServerStartedAt(startedAt > 0 ? startedAt : null);
    setUptimeSeconds(Number(statusQuery.data.uptimeSeconds || 0));
  }, [forcePassword, statusQuery.data]);

  useEffect(() => {
    if (!serverStartedAt) return;
    const update = () =>
      setUptimeSeconds(Math.max(0, Math.floor((Date.now() - serverStartedAt) / 1000)));
    const timer = window.setInterval(update, 60_000);
    return () => window.clearInterval(timer);
  }, [serverStartedAt]);

  const statusColor = systemOnline ? "bg-success" : "bg-destructive";
  const statusText = systemOnline ? "系统运行中" : "连接异常";

  return (
    <Sidebar collapsible="icon" className="border-r border-sidebar-border">
      <SidebarHeader className="border-b border-sidebar-border">
        <div className="flex items-center gap-2.5 px-2 py-2">
          <div className="relative flex h-8 w-8 shrink-0 items-center justify-center rounded-md bg-primary">
            <Zap className="h-4 w-4 text-primary-foreground" strokeWidth={2.5} />
            {administrator && !forcePassword && updateStatus?.updateAvailable && (
              <span className="absolute -right-0.5 -top-0.5 h-2.5 w-2.5 rounded-full border-2 border-sidebar bg-warning" />
            )}
          </div>
          {!collapsed && (
            <div className="flex flex-col leading-tight">
              <span className="max-w-36 truncate text-sm font-semibold tracking-tight">
                {appName}
              </span>
              {administrator && !forcePassword && (
                <button
                  type="button"
                  className={`flex items-center gap-1 font-mono text-[10px] tracking-[0.12em] text-muted-foreground ${
                    updateStatus?.updateAvailable ? "cursor-pointer text-warning" : "cursor-default"
                  }`}
                  onClick={() => updateStatus?.updateAvailable && setUpdateDialogOpen(true)}
                  title={buildInfo?.commit ? `commit ${buildInfo.commit}` : undefined}
                >
                  {updateStatus?.updateAvailable && <RefreshCw className="h-2.5 w-2.5" />}
                  {displayVersion(buildInfo?.version, buildInfo?.commit)}
                </button>
              )}
            </div>
          )}
        </div>
      </SidebarHeader>

      <SidebarContent className="gap-1 py-2">
        {navGroups.map((group) => (
          <SidebarGroup key={group.label}>
            {!collapsed && (
              <SidebarGroupLabel className="font-mono text-[10px] uppercase tracking-[0.2em] text-muted-foreground/70">
                {group.label}
              </SidebarGroupLabel>
            )}
            <SidebarGroupContent>
              <SidebarMenu>
                {group.items.map((item) => {
                  const active = isActive(item.url);
                  return (
                    <SidebarMenuItem key={item.url}>
                      <SidebarMenuButton
                        asChild
                        isActive={active}
                        tooltip={item.title}
                        className="group relative data-[active=true]:bg-sidebar-accent data-[active=true]:text-sidebar-accent-foreground"
                      >
                        <Link to={item.url} className="flex items-center gap-2.5">
                          {active && (
                            <span className="absolute left-0 top-1/2 h-5 w-0.5 -translate-y-1/2 rounded-r-full bg-primary" />
                          )}
                          <item.icon
                            className={`h-4 w-4 shrink-0 transition ${
                              active
                                ? "text-primary"
                                : "text-muted-foreground group-hover:text-foreground"
                            }`}
                          />
                          <span className="truncate text-sm">{item.title}</span>
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

      <SidebarFooter className="border-t border-sidebar-border">
        {forcePassword ? (
          <SidebarMenuButton
            onClick={() => {
              clearSession();
              navigate({ to: "/login" });
            }}
            tooltip="退出登录"
          >
            <LogOut className="h-4 w-4" />
            <span>退出登录</span>
          </SidebarMenuButton>
        ) : !collapsed ? (
          <div className="rounded-md border border-sidebar-border bg-sidebar-accent/40 p-3">
            <div className="flex items-center gap-2">
              <span className="relative flex h-2 w-2">
                {systemOnline && (
                  <span
                    className={`absolute inline-flex h-full w-full animate-ping rounded-full ${statusColor} opacity-70`}
                  />
                )}
                <span className={`relative inline-flex h-2 w-2 rounded-full ${statusColor}`} />
              </span>
              <span className="text-xs font-medium">{statusText}</span>
            </div>
            <p className="mt-1 font-mono text-[10px] text-muted-foreground">
              面板已运行 {formatDuration(uptimeSeconds)}
            </p>
          </div>
        ) : (
          <div className="flex justify-center py-2">
            <span className="relative flex h-2 w-2">
              {systemOnline && (
                <span
                  className={`absolute inline-flex h-full w-full animate-ping rounded-full ${statusColor} opacity-70`}
                />
              )}
              <span className={`relative inline-flex h-2 w-2 rounded-full ${statusColor}`} />
            </span>
          </div>
        )}
      </SidebarFooter>
      {administrator && !forcePassword && (
        <SystemUpdateDialog
          open={updateDialogOpen}
          status={updateStatus}
          onOpenChange={setUpdateDialogOpen}
        />
      )}
    </Sidebar>
  );
}
