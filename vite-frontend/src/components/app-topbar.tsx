import { useEffect, useState } from "react";
import { useNavigate, useRouterState } from "@tanstack/react-router";
import { LogOut, Search, UserCircle2 } from "lucide-react";
import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { SidebarTrigger } from "@/components/ui/sidebar";
import { StatusPill } from "@/components/patterns/status-pill";
import { CommandPalette } from "@/features/search/command-palette";
import { usePanelStatus } from "@/features/system/panel-status-context";
import { useSession } from "@/hooks/use-session";
import { roleLabel } from "@/lib/capabilities";
import { clearSession } from "@/lib/session";

export function AppTopbar() {
  const navigate = useNavigate();
  const { mustChangePassword, name: sessionName, roleId, isAdmin } = useSession();
  const title = useRouterState({
    select: (state) =>
      [...state.matches].reverse().find((match) => match.staticData.title)?.staticData.title ||
      "控制台",
  });
  const { failed } = usePanelStatus();
  const name = sessionName || "用户";
  const [searchOpen, setSearchOpen] = useState(false);
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (
        !mustChangePassword &&
        (event.metaKey || event.ctrlKey) &&
        event.key.toLowerCase() === "k"
      ) {
        event.preventDefault();
        setSearchOpen((value) => !value);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [mustChangePassword]);
  return (
    <header className="sticky top-0 z-30 flex h-14 shrink-0 items-center gap-3 border-b border-hairline bg-canvas px-4 md:px-6">
      <SidebarTrigger className="shrink-0 md:hidden" />
      <h2 className="min-w-0 flex-1 truncate text-section-title text-ink">
        {mustChangePassword ? "账户" : title}
      </h2>
      {mustChangePassword ? (
        <StatusPill tone="warning" label="请先修改密码" />
      ) : (
        <>
          <Button
            type="button"
            variant="outline"
            onClick={() => setSearchOpen(true)}
            aria-haspopup="dialog"
            aria-expanded={searchOpen}
            aria-label="搜索资源 ⌘K"
            className="size-10 p-0 md:h-8 md:w-56 md:justify-start md:px-3"
          >
            <Search className="size-4 text-mute" />
            <span className="hidden text-mute md:inline">搜索资源</span>
            <kbd
              aria-hidden="true"
              className="ml-auto hidden font-mono text-caption text-mute md:inline"
            >
              ⌘K
            </kbd>
          </Button>
          {isAdmin && (
            <div className="hidden md:block">
              <StatusPill
                tone={failed ? "critical" : "good"}
                label={failed ? "连接异常" : "正常"}
              />
            </div>
          )}
        </>
      )}
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <button
            type="button"
            aria-label={`用户菜单 ${name.slice(0, 2).toUpperCase()}`}
            className="flex size-10 shrink-0 items-center justify-center rounded-sm hover:bg-surface-hover"
          >
            <Avatar aria-hidden="true" className="size-8 border border-hairline">
              <AvatarFallback className="bg-canvas-soft text-caption-strong text-ink">
                {name.slice(0, 2).toUpperCase()}
              </AvatarFallback>
            </Avatar>
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-52">
          <DropdownMenuLabel>
            <span className="block truncate text-body-strong">{name}</span>
            <span className="text-caption text-mute">{roleLabel(roleId)}</span>
          </DropdownMenuLabel>
          <DropdownMenuSeparator />
          <DropdownMenuItem
            onSelect={() =>
              void navigate({
                to: "/account",
                search: mustChangePassword
                  ? { tab: "security", forcePassword: 1 }
                  : { tab: "security" },
              })
            }
          >
            <UserCircle2 className="mr-2 size-4" />
            账户
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem
            onSelect={() => {
              clearSession();
              void navigate({ to: "/login" });
            }}
            className="text-status-critical-text focus:text-status-critical-text"
          >
            <LogOut className="mr-2 size-4" />
            退出登录
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <CommandPalette open={searchOpen} onOpenChange={setSearchOpen} />
    </header>
  );
}
