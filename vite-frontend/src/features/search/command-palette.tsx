import { useMemo, useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { ArrowRightLeft, File, Loader2, Plus, ServerCog, Users, Waypoints } from "lucide-react";
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
import { StatusPill } from "@/components/patterns/status-pill";
import { forwardStatus, nodeStatus, tunnelStatus, userStatus } from "@/lib/status";
import { useSession } from "@/hooks/use-session";
import { accountNavigation, navigationForRole } from "@/lib/navigation";
import { errorMessage } from "@/lib/api/client";
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
export function CommandPalette({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { roleId, isAdmin, mustChangePassword } = useSession();
  const [query, setQuery] = useState("");
  const navigate = useNavigate();
  const search = useSearchItems(open && !mustChangePassword);
  const items = useMemo(
    () => filterGlobalSearchItems(search.items, query, 100),
    [query, search.items],
  );
  const pages = [...navigationForRole(roleId).flatMap((group) => group.items), accountNavigation];
  const matches = (text: string) =>
    query
      .trim()
      .toLowerCase()
      .split(/\s+/)
      .every((token) => text.toLowerCase().includes(token));
  const close = () => {
    onOpenChange(false);
    setQuery("");
  };
  const allowed = (kind: GlobalSearchKind) => kind === "forward" || isAdmin;
  return (
    <Dialog
      open={open && !mustChangePassword}
      onOpenChange={(value) => {
        onOpenChange(value);
        if (!value) setQuery("");
      }}
    >
      <DialogContent className="h-[min(620px,calc(100dvh-2rem))] max-w-140 gap-0 p-0 max-md:left-0 max-md:top-0 max-md:h-dvh max-md:max-h-dvh max-md:w-full max-md:max-w-none max-md:translate-x-0 max-md:translate-y-0 max-md:rounded-none [&>div.min-h-0]:flex [&>div.min-h-0]:flex-col">
        <DialogHeader className="sr-only">
          <DialogTitle>搜索资源</DialogTitle>
          <DialogDescription>按类型搜索资源，使用方向键选择并回车打开</DialogDescription>
        </DialogHeader>
        <Command
          shouldFilter={false}
          label="搜索资源"
          className="min-h-0 rounded-none bg-canvas-soft"
        >
          <CommandInput
            aria-label="搜索资源"
            placeholder="搜索资源、页面或操作…"
            value={query}
            onValueChange={setQuery}
            className="h-14 pr-10"
          />
          {search.isFetching && (
            <div role="status" className="flex items-center gap-2 px-4 py-2 text-caption text-mute">
              <Loader2 className="size-3 animate-spin" />
              正在加载资源
            </div>
          )}
          {search.error && (
            <div role="alert" className="px-4 py-2 text-caption text-status-critical-text">
              {errorMessage(search.error)}
            </div>
          )}
          <CommandList className="max-h-none min-h-0 flex-1 p-2" aria-label="搜索结果">
            <CommandEmpty>没有匹配的资源、页面或操作</CommandEmpty>
            {kinds
              .filter(({ kind }) => allowed(kind) && items.some((item) => item.kind === kind))
              .map(({ kind, title, icon: Icon }) => (
                <CommandGroup key={kind} heading={title}>
                  {items
                    .filter((item) => item.kind === kind)
                    .map((item) => (
                      <CommandItem
                        key={item.id}
                        value={item.id}
                        onSelect={() => {
                          close();
                          void navigate({
                            to: item.href,
                            search: { focus: Number(item.entityId) },
                          });
                        }}
                        className="min-h-12 gap-3 px-3 py-2"
                      >
                        <Icon className="text-mute" />
                        <span className="min-w-0 flex-1">
                          <span className="block truncate text-body-strong">{item.title}</span>
                          <span className="block truncate font-mono text-caption text-mute">
                            {item.subtitle}
                          </span>
                        </span>
                        <StatusPill {...statusFor[item.kind]({ status: item.statusValue })} />
                      </CommandItem>
                    ))}
                </CommandGroup>
              ))}
            {pages.some((item) => matches(item.title)) && (
              <CommandGroup heading="页面">
                {pages
                  .filter((item) => matches(item.title))
                  .map((item) => (
                    <CommandItem
                      key={item.url}
                      value={`page-${item.url}`}
                      onSelect={() => {
                        close();
                        void navigate({ to: item.url });
                      }}
                      className="min-h-10 px-3"
                    >
                      <File className="text-mute" />
                      {item.title}
                    </CommandItem>
                  ))}
              </CommandGroup>
            )}
            {actions.some((item) => (!item.admin || isAdmin) && matches(item.title)) && (
              <CommandGroup heading="操作">
                {actions
                  .filter((item) => (!item.admin || isAdmin) && matches(item.title))
                  .map((item) => (
                    <CommandItem
                      key={item.to}
                      value={`create-${item.to}`}
                      onSelect={() => {
                        close();
                        void navigate({ to: item.to, search: { create: 1 } });
                      }}
                      className="min-h-10 px-3"
                    >
                      <Plus className="text-mute" />
                      {item.title}
                    </CommandItem>
                  ))}
              </CommandGroup>
            )}
          </CommandList>
          <p className="border-t border-hairline px-4 py-3 text-caption text-mute">
            ↑ ↓ 选择 · Enter 打开 · Esc 关闭
          </p>
        </Command>
      </DialogContent>
    </Dialog>
  );
}
