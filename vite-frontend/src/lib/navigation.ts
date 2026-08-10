import type { ComponentType } from "react";
import {
  LayoutDashboard,
  Waypoints,
  ArrowRightLeft,
  Gauge,
  ServerCog,
  Users,
  Ticket,
  Settings2,
  SlidersHorizontal,
  UserCircle2,
} from "lucide-react";

import { canAccessPath } from "@/lib/capabilities";

export type NavItem = {
  title: string;
  url: string;
  icon: ComponentType<{ className?: string }>;
};

export type NavGroup = { label: string; items: NavItem[] };

const navGroups: NavGroup[] = [
  {
    label: "概览",
    items: [{ title: "控制台", url: "/", icon: LayoutDashboard }],
  },
  {
    label: "转发管理",
    items: [
      { title: "隧道", url: "/tunnel", icon: Waypoints },
      { title: "转发规则", url: "/forward", icon: ArrowRightLeft },
      { title: "限速策略", url: "/limit", icon: Gauge },
    ],
  },
  {
    label: "节点管理",
    items: [{ title: "节点", url: "/node", icon: ServerCog }],
  },
  {
    label: "用户与订阅",
    items: [
      { title: "用户", url: "/user", icon: Users },
      { title: "订阅", url: "/subscription", icon: Ticket },
    ],
  },
  {
    label: "系统",
    items: [
      { title: "系统配置", url: "/config", icon: SlidersHorizontal },
      { title: "面板设置", url: "/settings", icon: Settings2 },
      { title: "个人资料", url: "/profile", icon: UserCircle2 },
    ],
  },
];

/**
 * navigationForRole returns only the groups/items the role may open. Empty
 * groups (all items filtered out) are dropped so the sidebar shows no bare
 * section headers.
 */
export function navigationForRole(roleID: number | null | undefined): NavGroup[] {
  return navGroups
    .map((group) => ({
      label: group.label,
      items: group.items.filter((item) => canAccessPath(roleID, item.url)),
    }))
    .filter((group) => group.items.length > 0);
}
