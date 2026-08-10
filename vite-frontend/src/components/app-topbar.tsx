import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Search, Command, Loader2 } from "lucide-react";
import { useNavigate } from "@tanstack/react-router";
import { SidebarTrigger } from "@/components/ui/sidebar";
import { Separator } from "@/components/ui/separator";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import { ThemeToggle } from "@/components/theme-toggle";
import { getAllUsers, getForwardList, getNodeList, getTunnelList } from "@/lib/api";
import { isAdministrator } from "@/lib/capabilities";
import { clearSession, getRoleID } from "@/lib/session";
import {
  buildGlobalSearchItems,
  filterGlobalSearchItems,
  normalizeApiList,
  type GlobalSearchItem,
} from "@/lib/global-search";
import { getGlobalSearchCache, searchCacheKeyForRole } from "@/lib/search-cache";

function getStoredName() {
  if (typeof window === "undefined") return "admin";
  return window.localStorage.getItem("name") || "admin";
}
function getStoredRole() {
  if (typeof window === "undefined") return "user";
  const r = window.localStorage.getItem("role_id");
  return r === "0" ? "super-admin" : r === "2" ? "operator" : "user";
}

export function AppTopbar() {
  const navigate = useNavigate();
  const name = getStoredName();
  const role = getStoredRole();
  const initials = name.slice(0, 2).toUpperCase();
  const searchRef = useRef<HTMLDivElement | null>(null);
  const inputRef = useRef<HTMLInputElement | null>(null);
  const [searchOpen, setSearchOpen] = useState(false);
  const [searchLoading, setSearchLoading] = useState(false);
  const [query, setQuery] = useState("");
  const [searchItems, setSearchItems] = useState<GlobalSearchItem[]>([]);

  const loadSearchItems = useCallback(
    async (force = false) => {
      if (searchLoading) return;
      setSearchLoading(true);
      try {
        const admin = isAdministrator(getRoleID());
        const cache = getGlobalSearchCache();
        const key = searchCacheKeyForRole(admin);
        if (force) cache.invalidate("all");

        const items = (await cache.load(key, async () => {
          // 普通用户只加载自己的转发列表；节点/用户/隧道是管理员数据集。
          const [tunnels, nodes, users, forwards] = await Promise.all([
            admin ? getTunnelList() : Promise.resolve({ code: -1, data: null } as any),
            admin ? getNodeList() : Promise.resolve({ code: -1, data: null } as any),
            admin
              ? getAllUsers({ current: 1, size: 500 })
              : Promise.resolve({ code: -1, data: null } as any),
            getForwardList(),
          ]);
          return buildGlobalSearchItems({
            tunnels: tunnels.code === 0 ? normalizeApiList(tunnels.data) : [],
            nodes: nodes.code === 0 ? normalizeApiList(nodes.data) : [],
            users: users.code === 0 ? normalizeApiList(users.data) : [],
            forwards: forwards.code === 0 ? normalizeApiList(forwards.data) : [],
          });
        })) as GlobalSearchItem[];

        setSearchItems(items);
      } finally {
        setSearchLoading(false);
      }
    },
    [searchLoading],
  );

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
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setSearchOpen(true);
        inputRef.current?.focus();
        void loadSearchItems();
      }
      if (event.key === "Escape") setSearchOpen(false);
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [loadSearchItems]);

  useEffect(() => {
    const onPointerDown = (event: PointerEvent) => {
      if (!searchRef.current?.contains(event.target as Node)) setSearchOpen(false);
    };
    window.addEventListener("pointerdown", onPointerDown);
    return () => window.removeEventListener("pointerdown", onPointerDown);
  }, []);

  useEffect(() => {
    const onInvalidate = () => {
      // Cache already cleared by invalidateGlobalSearch; drop local rows so
      // the next open reloads through SearchCache.
      setSearchItems([]);
    };
    window.addEventListener("global-search-invalidate", onInvalidate);
    return () => window.removeEventListener("global-search-invalidate", onInvalidate);
  }, []);

  const logout = () => {
    clearSession();
    getGlobalSearchCache().clear();
    navigate({ to: "/login" });
  };
  return (
    <header className="sticky top-0 z-30 flex h-14 items-center gap-3 border-b border-border bg-background/70 px-4 backdrop-blur-xl lg:px-6">
      <SidebarTrigger className="text-muted-foreground hover:text-foreground" />
      <Separator orientation="vertical" className="mx-1 h-5" />

      <div className="hidden flex-1 md:block">
        <div ref={searchRef} className="relative max-w-md">
          <Search className="pointer-events-none absolute left-3 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
          <input
            ref={inputRef}
            type="text"
            value={query}
            onFocus={() => {
              setSearchOpen(true);
              void loadSearchItems();
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
            className="h-9 w-full rounded-md border border-border bg-card/60 pl-9 pr-16 text-sm outline-none transition placeholder:text-muted-foreground focus:border-primary/60 focus:shadow-glow"
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

      <div className="ml-auto flex items-center gap-1.5">
        <ThemeToggle />

        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button className="flex items-center gap-2 rounded-md px-1.5 py-1 transition hover:bg-accent">
              <Avatar className="h-7 w-7 border border-border">
                <AvatarFallback className="bg-gradient-primary text-[11px] font-semibold text-primary-foreground">
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
            <DropdownMenuItem onClick={() => navigate({ to: "/settings" })}>
              面板设置
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={logout} className="text-destructive focus:text-destructive">
              退出登录
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </header>
  );
}
