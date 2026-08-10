import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Construction } from "lucide-react";
import type { ReactNode } from "react";

export function ComingSoon({
  title,
  description,
  icon,
  phase = "Phase 3",
}: {
  title: string;
  description: string;
  icon?: ReactNode;
  phase?: string;
}) {
  return (
    <div className="space-y-6">
      <div>
        <div className="flex items-center gap-3">
          {icon && (
            <div className="flex h-10 w-10 items-center justify-center rounded-md border border-border bg-card text-primary">
              {icon}
            </div>
          )}
          <div>
            <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
            <p className="mt-1 text-sm text-muted-foreground">{description}</p>
          </div>
        </div>
      </div>

      <Card className="relative overflow-hidden border-dashed border-border bg-card/60 p-12 shadow-card">
        <div className="bg-grid pointer-events-none absolute inset-0 opacity-30" />
        <div className="relative flex flex-col items-center justify-center text-center">
          <div className="flex h-14 w-14 items-center justify-center rounded-full border border-primary/30 bg-primary/10 text-primary shadow-glow">
            <Construction className="h-6 w-6" />
          </div>
          <Badge
            variant="outline"
            className="mt-4 border-primary/40 bg-primary/10 font-mono text-[10px] uppercase tracking-widest text-primary"
          >
            {phase} · 建设中
          </Badge>
          <h2 className="mt-4 text-lg font-semibold">此模块正在按计划迁移</h2>
          <p className="mt-2 max-w-md text-sm text-muted-foreground">
            设计系统与外壳已完成。此业务页将在下一轮迭代中按 go-panel 的逻辑重写为 shadcn + TanStack
            Query 版本。
          </p>
        </div>
      </Card>
    </div>
  );
}
