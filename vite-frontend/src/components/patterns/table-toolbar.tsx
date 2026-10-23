import { useEffect, useRef, useState, type ReactNode } from "react";
import { Search, X } from "lucide-react";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
export function TableToolbar({
  searchValue = "",
  onSearchChange,
  searchPlaceholder = "搜索…",
  filters,
  count,
  viewSwitch,
  children,
}: {
  searchValue?: string;
  onSearchChange?: (value: string) => void;
  searchPlaceholder?: string;
  filters?: ReactNode;
  count?: ReactNode;
  viewSwitch?: ReactNode;
  children?: ReactNode;
}) {
  const [text, setText] = useState(searchValue);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  useEffect(() => {
    clearTimeout(timer.current);
    setText(searchValue);
  }, [searchValue]);
  useEffect(() => () => clearTimeout(timer.current), []);
  function change(value: string) {
    setText(value);
    clearTimeout(timer.current);
    timer.current = setTimeout(() => onSearchChange?.(value), 200);
  }
  return (
    <div className="flex flex-wrap items-center justify-between gap-3">
      <div className="flex min-w-0 flex-wrap items-center gap-2">
        {onSearchChange && (
          <div className="relative min-w-0 max-w-full">
            <Search aria-hidden="true" className="absolute left-3 top-2.5 size-4 text-mute" />
            <Input
              aria-label="搜索"
              placeholder={searchPlaceholder}
              value={text}
              onChange={(event) => change(event.target.value)}
              className="w-64 max-w-full pl-9 pr-10"
            />
            {text && (
              <Button
                type="button"
                aria-label="清除搜索"
                variant="ghost"
                size="icon-sm"
                className="absolute right-0.5 top-0.5"
                onClick={() => {
                  clearTimeout(timer.current);
                  setText("");
                  onSearchChange?.("");
                }}
              >
                <X aria-hidden="true" />
              </Button>
            )}
          </div>
        )}
        {filters}
        {children}
      </div>
      <div className="flex flex-wrap items-center gap-3 text-caption text-mute">
        {count}
        {viewSwitch}
      </div>
    </div>
  );
}
