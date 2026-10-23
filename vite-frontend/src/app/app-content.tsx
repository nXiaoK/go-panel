import type { ComponentProps } from "react";
import { cn } from "@/lib/utils";

export function AppContent({ children, className, ...props }: ComponentProps<"main">) {
  return (
    // 横向只裁切、不创建滚动容器；纵向继续随页面滚动，让列表批量条相对视口吸底。
    <main className={cn("min-w-0 flex-1 overflow-x-clip", className)} {...props}>
      <div className="mx-auto w-full max-w-(--content-max-width) p-4 md:p-6">{children}</div>
    </main>
  );
}
