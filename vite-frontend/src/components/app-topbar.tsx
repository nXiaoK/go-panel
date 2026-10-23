import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Separator } from "@/components/ui/separator";
import { SidebarTrigger } from "@/components/ui/sidebar";
import { filterGlobalSearchItems, type GlobalSearchItem } from "@/features/search/model";
import { useSearchItems } from "@/features/search/queries";
import { useSession } from "@/hooks/use-session";
import { errorMessage } from "@/lib/api/client";
import { roleLabel } from "@/lib/capabilities";
import { notify } from "@/lib/notify";
import { clearSession } from "@/lib/session";
import { useNavigate } from "@tanstack/react-router";
import { Command, Loader2, Search } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";

export function AppTopbar() {
  const navigate = useNavigate();
  const { mustChangePassword: forcePassword, name: sessionName, roleId } = useSession();
  const name = sessionName || "admin";
  const role = roleLabel(roleId);
  const initials = name.slice(0, 2).toUpperCase();
  const searchRef = useRef<HTMLDivElement | null>(null);
  const inputRef = useRef<HTMLInputElement | null>(null);
  const [searchOpen, setSearchOpen] = useState(false);
  const [query, setQuery] = useState("");
  const {
    items: searchItems,
    isFetching: searchLoading,
    error: searchError,
  } = useSearchItems(searchOpen && !forcePassword);
  useEffect(() => {
    if (searchError) notify.error(errorMessage(searchError));
  }, [searchError]);

  const filteredSearchItems = useMemo(
    () => filterGlobalSearchItems(searchItems, query, 8),
    [query, searchItems],
  );

  const openSearchItem = (item: GlobalSearchItem) => {
    setSearchOpen(false);
    setQuery("");
    navigate({ to: item.href });
  };

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (!forcePassword && (event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setSearchOpen(true);
        inputRef.current?.focus();
      }
      if (event.key === "Escape") setSearchOpen(false);
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [forcePassword]);

  useEffect(() => {
    const onPointerDown = (event: PointerEvent) => {
      if (!searchRef.current?.contains(event.target as Node)) setSearchOpen(false);
    };
    window.addEventListener("pointerdown", onPointerDown);
    return () => window.removeEventListener("pointerdown", onPointerDown);
  }, []);

  const logout = () => {
    clearSession();
    navigate({ to: "/login" });
  };
  return (
    <header className="sticky top-0 z-30 flex h-14 items-center gap-3 border-b border-border bg-background/70 px-4 backdrop-blur-xl lg:px-6">
      <SidebarTrigger className="text-muted-foreground hover:text-foreground" />
      <Separator orientation="vertical" className="mx-1 h-5" />

      {!forcePassword && (
        <div className="hidden flex-1 md:block">
          <div ref={searchRef} className="relative max-w-md">
            <Search className="pointer-events-none absolute left-3 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
            <input
              ref={inputRef}
              type="text"
              value={query}
              onFocus={() => {
                setSearchOpen(true);
              }}
              onChange={(event) => {
                setQuery(event.target.value);
                setSearchOpen(true);
              }}
              onKeyDown={(event) => {
                if (event.key === "Enter" && filteredSearchItems[0]) {
                  event.preventDefault();
                  openSearchItem(filteredSearchItems[0]);
                }
              }}
              placeholder="搜索隧道、节点、用户…"
              className="h-9 w-full rounded-md border border-border bg-card/60 pl-9 pr-16 text-sm outline-none transition placeholder:text-muted-foreground focus:border-primary/60"
            />
            <kbd className="pointer-events-none absolute right-2 top-1/2 flex -translate-y-1/2 items-center gap-1 rounded border border-border bg-muted/60 px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground">
              <Command className="h-3 w-3" />K
            </kbd>
            {searchOpen && (
              <div className="absolute left-0 right-0 top-11 z-50 overflow-hidden rounded-md border border-border bg-popover text-popover-foreground shadow-lg">
                {searchLoading && (
                  <div className="flex items-center gap-2 px-3 py-3 text-sm text-muted-foreground">
                    <Loader2 className="h-3.5 w-3.5 animate-spin" /> 正在加载索引
                  </div>
                )}
                {!searchLoading && filteredSearchItems.length === 0 && (
                  <div className="px-3 py-3 text-sm text-muted-foreground">
                    {query.trim() ? "没有找到匹配结果" : "暂无可搜索数据"}
                  </div>
                )}
                {!searchLoading &&
                  filteredSearchItems.map((item) => (
                    <button
                      key={item.id}
                      type="button"
                      className="flex w-full items-center gap-3 px-3 py-2.5 text-left transition hover:bg-accent focus:bg-accent focus:outline-none"
                      onClick={() => openSearchItem(item)}
                    >
                      <span className="w-12 shrink-0 rounded border border-border bg-muted/50 px-1.5 py-0.5 text-center font-mono text-[10px] uppercase text-muted-foreground">
                        {item.kind}
                      </span>
                      <span className="min-w-0 flex-1">
                        <span className="block truncate text-sm font-medium">{item.title}</span>
                        <span className="block truncate font-mono text-[11px] text-muted-foreground">
                          {item.subtitle}
                        </span>
                      </span>
                    </button>
                  ))}
              </div>
            )}
          </div>
        </div>
      )}

      <div className="ml-auto flex items-center gap-1.5">
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button className="flex items-center gap-2 rounded-md px-1.5 py-1 transition hover:bg-accent">
              <Avatar className="h-7 w-7 border border-border">
                <AvatarFallback className="bg-primary text-[11px] font-semibold text-primary-foreground">
                  {initials}
                </AvatarFallback>
              </Avatar>
              <div className="hidden text-left leading-tight md:block">
                <div className="text-xs font-medium">{name}</div>
                <div className="font-mono text-[10px] text-muted-foreground">{role}</div>
              </div>
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-48">
            <DropdownMenuLabel>账户</DropdownMenuLabel>
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={() => navigate({ to: "/profile" })}>
              个人资料
            </DropdownMenuItem>
            {!forcePassword && (
              <DropdownMenuItem onClick={() => navigate({ to: "/settings" })}>
                面板设置
              </DropdownMenuItem>
            )}
            <DropdownMenuSeparator />
            <DropdownMenuItem
              onClick={logout}
              className="text-status-critical-text focus:text-status-critical-text"
            >
              退出登录
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </header>
  );
}
