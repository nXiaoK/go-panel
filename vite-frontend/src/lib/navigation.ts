import type { ComponentType } from "react";
import {
  LayoutDashboard,
  Waypoints,
  ArrowRightLeft,
  Gauge,
  ServerCog,
  Users,
  Ticket,
  SlidersHorizontal,
  UserCircle2,
} from "lucide-react";
import { canAccessPath, isAdministrator } from "@/lib/capabilities";

export type NavItem = { title: string; url: string; icon: ComponentType<{ className?: string }> };
export type NavGroup = { label: string; items: NavItem[] };
export const accountNavigation: NavItem = { title: "账户", url: "/account", icon: UserCircle2 };
const navGroups: NavGroup[] = [
  { label: "概览", items: [{ title: "控制台", url: "/", icon: LayoutDashboard }] },
  {
    label: "资源",
    items: [
      { title: "节点", url: "/node", icon: ServerCog },
      { title: "隧道", url: "/tunnel", icon: Waypoints },
      { title: "转发规则", url: "/forward", icon: ArrowRightLeft },
      { title: "限速", url: "/limit", icon: Gauge },
    ],
  },
  {
    label: "用户与订阅",
    items: [
      { title: "用户", url: "/user", icon: Users },
      { title: "订阅", url: "/subscription", icon: Ticket },
    ],
  },
  { label: "系统", items: [{ title: "系统配置", url: "/config", icon: SlidersHorizontal }] },
];

/** 侧栏与命令面板共用角色导航；账户单独放在用户菜单，普通用户侧栏也提供入口。 */
export function navigationForRole(roleID: number | null | undefined): NavGroup[] {
  return navGroups
    .map((group) => ({
      ...group,
      items: group.items
        .filter((item) => canAccessPath(roleID, item.url))
        .map((item) =>
          item.url === "/forward" && !isAdministrator(roleID)
            ? { ...item, title: "我的转发" }
            : item,
        ),
    }))
    .filter((group) => group.items.length > 0);
}

declare module "@tanstack/react-router" {
  interface StaticDataRouteOption {
    title?: string;
  }
}
