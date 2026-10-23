import { useMemo, useRef, useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import {
  ArrowRight,
  ArrowRightLeft,
  File,
  Loader2,
  Plus,
  Search,
  ServerCog,
  Users,
  Waypoints,
  X,
} from "lucide-react";
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { StatusPill } from "@/components/patterns/status-pill";
import { forwardStatus, nodeStatus, tunnelStatus, userStatus } from "@/lib/status";
import { useSession } from "@/hooks/use-session";
import { accountNavigation, navigationForRole } from "@/lib/navigation";
import { errorMessage } from "@/lib/api/client";
import { cn } from "@/lib/utils";
import { filterGlobalSearchItems, type GlobalSearchKind } from "./model";
import { useSearchItems } from "./queries";

const statusFor = {
  node: nodeStatus,
  tunnel: tunnelStatus,
  forward: forwardStatus,
  user: userStatus,
};
const kinds = [
  { kind: "node", title: "节点", icon: ServerCog },
  { kind: "tunnel", title: "隧道", icon: Waypoints },
  { kind: "forward", title: "转发", icon: ArrowRightLeft },
  { kind: "user", title: "用户", icon: Users },
] as const;
const actions = [
  { title: "新建转发", to: "/forward", admin: false },
  { title: "新建节点", to: "/node", admin: true },
  { title: "新建隧道", to: "/tunnel", admin: true },
  { title: "新建用户", to: "/user", admin: true },
] as const;
type SearchScope = "all" | GlobalSearchKind | "navigation";
const resultClass =
  "group min-h-14 cursor-pointer gap-3 border-l-2 border-transparent px-3 py-2.5 data-[selected=true]:border-primary data-[selected=true]:bg-surface-selected data-[selected=true]:text-ink";
const shortcutClass =
  "inline-flex min-w-5 items-center justify-center rounded-xs border border-hairline bg-canvas px-1 font-mono text-caption text-body";

export function CommandPalette({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { roleId, isAdmin, mustChangePassword } = useSession();
  const [query, setQuery] = useState("");
  const [scope, setScope] = useState<SearchScope>("all");
  const inputRef = useRef<HTMLInputElement>(null);
  const navigate = useNavigate();
  const search = useSearchItems(open && !mustChangePassword);
  // 先匹配完整索引再按类型限量，避免大量隧道挤掉节点/转发结果；限量不影响分类计数。
  const items = useMemo(
    () => filterGlobalSearchItems(search.items, query, Infinity),
    [query, search.items],
  );
  const allowedKinds = kinds.filter(({ kind }) => kind === "forward" || isAdmin);
  const matches = (text: string) =>
    query
      .trim()
      .toLowerCase()
      .split(/\s+/)
      .every((token) => text.toLowerCase().includes(token));
  const pages = [
    ...navigationForRole(roleId).flatMap((group) => group.items),
    accountNavigation,
  ].filter((item) => matches(item.title));
  const availableActions = actions.filter(
    (item) => (!item.admin || isAdmin) && matches(item.title),
  );
  const scopes = [
    { kind: "all", title: "全部" },
    ...allowedKinds,
    { kind: "navigation", title: "页面与操作" },
  ] as const;
  const groups = allowedKinds.map((group) => ({
    ...group,
    items: items.filter((item) => item.kind === group.kind),
  }));
  // 全部视图每类预览 3 条；指定分类最多展示 100 条，更多结果引导缩小关键词。
  const limit = scope === "all" ? 3 : 100;
  const visibleGroups = groups.filter(
    (group) => (scope === "all" || scope === group.kind) && group.items.length > 0,
  );
  const showNavigation = scope === "all" || scope === "navigation";
  const resultCount =
    visibleGroups.reduce((sum, group) => sum + group.items.length, 0) +
    (showNavigation ? pages.length + availableActions.length : 0);
  const reset = () => {
    setQuery("");
    setScope("all");
  };
  const close = () => {
    onOpenChange(false);
    reset();
  };
  const chooseScope = (value: SearchScope) => {
    setScope(value);
    inputRef.current?.focus();
  };

  return (
    <Dialog
      open={open && !mustChangePassword}
      onOpenChange={(value) => {
        onOpenChange(value);
        if (!value) reset();
      }}
    >
      <DialogContent className="h-[min(680px,calc(100dvh-4rem))] max-w-160 gap-0 p-0 max-md:left-0 max-md:top-0 max-md:h-dvh max-md:max-h-dvh max-md:w-full max-md:max-w-none max-md:translate-x-0 max-md:translate-y-0 max-md:rounded-none [&>div.min-h-0]:flex [&>div.min-h-0]:flex-col [&>div.min-h-0]:overflow-hidden">
        <DialogHeader className="border-b border-hairline px-5 py-4 pr-16">
          <DialogTitle className="text-section-title">全局搜索</DialogTitle>
          <DialogDescription className="text-caption text-mute">
            搜索名称、ID、IP 或端口，快速定位资源
          </DialogDescription>
        </DialogHeader>
        <Command
          shouldFilter={false}
          label="搜索资源"
          loop
          className="min-h-0 rounded-none bg-canvas"
        >
          <CommandInput
            ref={inputRef}
            aria-label="搜索资源"
            placeholder="搜索资源、页面或操作…"
            value={query}
            onValueChange={setQuery}
            className="h-14 text-base md:text-sm"
            trailing={
              query ? (
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  aria-label="清空搜索"
                  onClick={() => {
                    setQuery("");
                    inputRef.current?.focus();
                  }}
                >
                  <X aria-hidden="true" />
                </Button>
              ) : (
                <kbd className={cn(shortcutClass, "hidden md:inline-flex")}>Esc</kbd>
              )
            }
          />
          <div
            role="group"
            aria-label="搜索类型"
            className="flex shrink-0 gap-1 overflow-x-auto border-b border-hairline bg-canvas-soft px-3 py-2"
          >
            {scopes.map(({ kind, title }) => (
              <button
                key={kind}
                type="button"
                aria-pressed={scope === kind}
                onClick={() => chooseScope(kind)}
                className={cn(
                  "min-h-8 shrink-0 cursor-pointer rounded-sm border border-transparent px-3 text-caption-strong text-mute hover:bg-surface-hover hover:text-ink max-md:min-h-10",
                  scope === kind && "border-border-control bg-canvas text-ink",
                )}
              >
                {title}
              </button>
            ))}
          </div>
          {search.isFetching && (
            <div
              role="status"
              className="flex shrink-0 items-center gap-2 px-5 py-2 text-caption text-mute"
            >
              <Loader2 className="size-3 animate-spin" aria-hidden="true" />
              正在加载资源
            </div>
          )}
          {search.error && (
            <div
              role="alert"
              className="flex shrink-0 items-center justify-between gap-3 bg-status-critical-bg px-5 py-2 text-caption text-status-critical-text"
            >
              <span>{errorMessage(search.error)}</span>
              <Button
                type="button"
                variant="danger"
                size="sm"
                disabled={search.isFetching}
                onClick={() => void search.retry()}
              >
                重试
              </Button>
            </div>
          )}
          <CommandList
            className="max-h-none min-h-0 flex-1 overscroll-contain p-2"
            aria-label="搜索结果"
            aria-busy={search.isFetching}
          >
            {!search.isFetching && (
              <CommandEmpty className="px-4 py-12 text-center">
                <Search className="mx-auto mb-3 size-6 text-mute" aria-hidden="true" />
                <p className="text-body-strong text-ink">没有匹配的资源、页面或操作</p>
                <p className="mt-2 text-caption text-mute">
                  试试其他名称、IP 或端口，或切换搜索类型
                </p>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="mt-4"
                  onClick={() => {
                    reset();
                    inputRef.current?.focus();
                  }}
                >
                  清除筛选
                </Button>
              </CommandEmpty>
            )}
            {visibleGroups.map(({ kind, title, icon: Icon, items: groupItems }) => (
              <CommandGroup
                key={kind}
                heading={
                  <span className="flex items-center justify-between">
                    <span>{title}</span>
                    <span className="font-mono tabular">{groupItems.length}</span>
                  </span>
                }
                className="mb-2"
              >
                {groupItems.slice(0, limit).map((item) => (
                  <CommandItem
                    key={item.id}
                    value={item.id}
                    onSelect={() => {
                      close();
                      void navigate({ to: item.href, search: { focus: Number(item.entityId) } });
                    }}
                    className={resultClass}
                  >
                    <span className="flex size-8 shrink-0 items-center justify-center rounded-sm border border-hairline bg-canvas-soft text-mute">
                      <Icon aria-hidden="true" />
                    </span>
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-body-strong" title={item.title}>
                        {item.title}
                      </span>
                      <span
                        className="mt-0.5 block truncate font-mono text-caption text-mute"
                        title={item.subtitle}
                      >
                        {item.subtitle}
                      </span>
                    </span>
                    <StatusPill {...statusFor[item.kind]({ status: item.statusValue })} />
                    <ArrowRight
                      className="invisible hidden text-mute group-data-[selected=true]:visible md:block"
                      aria-hidden="true"
                    />
                  </CommandItem>
                ))}
                {groupItems.length > limit &&
                  (scope === "all" ? (
                    <CommandItem
                      value={`more-${kind}`}
                      className="min-h-10 cursor-pointer justify-center text-caption text-body data-[selected=true]:bg-surface-selected"
                      onSelect={() => chooseScope(kind)}
                    >
                      查看全部 {groupItems.length} 条{title}
                      <ArrowRight aria-hidden="true" />
                    </CommandItem>
                  ) : (
                    <p className="px-3 py-2 text-caption text-mute">
                      仅显示前 100 条，请输入更具体的关键词
                    </p>
                  ))}
              </CommandGroup>
            ))}
            {showNavigation && pages.length > 0 && (
              <CommandGroup heading="页面" className="mb-2">
                {pages.map((item) => (
                  <CommandItem
                    key={item.url}
                    value={`page-${item.url}`}
                    onSelect={() => {
                      close();
                      void navigate({ to: item.url });
                    }}
                    className={cn(resultClass, "min-h-10")}
                  >
                    <File className="text-mute" aria-hidden="true" />
                    <span className="flex-1">{item.title}</span>
                    <span className="text-caption text-mute">打开页面</span>
                  </CommandItem>
                ))}
              </CommandGroup>
            )}
            {showNavigation && availableActions.length > 0 && (
              <CommandGroup heading="操作">
                {availableActions.map((item) => (
                  <CommandItem
                    key={item.to}
                    value={`create-${item.to}`}
                    onSelect={() => {
                      close();
                      void navigate({ to: item.to, search: { create: 1 } });
                    }}
                    className={cn(resultClass, "min-h-10")}
                  >
                    <Plus className="text-mute" aria-hidden="true" />
                    <span className="flex-1">{item.title}</span>
                    <span className="text-caption text-mute">创建</span>
                  </CommandItem>
                ))}
              </CommandGroup>
            )}
          </CommandList>
          <div className="flex shrink-0 items-center justify-between gap-3 border-t border-hairline bg-canvas-soft px-5 py-3 text-caption text-mute">
            <span role="status" aria-live="polite">
              {resultCount} 条匹配{scope === "all" && " · 按类型预览"}
            </span>
            <span className="hidden items-center gap-3 md:flex">
              <span className="flex gap-1">
                <kbd className={shortcutClass}>↑</kbd>
                <kbd className={shortcutClass}>↓</kbd>选择
              </span>
              <span className="flex gap-1">
                <kbd className={shortcutClass}>↵</kbd>打开
              </span>
              <span className="flex gap-1">
                <kbd className={shortcutClass}>Esc</kbd>关闭
              </span>
            </span>
            <span className="md:hidden">点按结果打开</span>
          </div>
        </Command>
      </DialogContent>
    </Dialog>
  );
}
