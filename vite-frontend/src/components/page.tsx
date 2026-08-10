import type { ReactNode } from "react";
import { AlertTriangle, RefreshCcw } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Label } from "@/components/ui/label";
import { cn } from "@/lib/utils";

/**
 * Shared page scaffolding: headers, KPI tiles, and form field wrappers that
 * were previously copy-pasted across route files.
 */

type PageHeaderProps = {
  title: string;
  description?: ReactNode;
  /**
   * Small mono eyebrow shown above the title (ops style, e.g. "forwards").
   * Mutually exclusive with `icon`.
   */
  eyebrow?: ReactNode;
  /** Icon rendered in a bordered box beside the title. Mutually exclusive with `eyebrow`. */
  icon?: ReactNode;
  actions?: ReactNode;
};

export function PageHeader({ title, description, eyebrow, icon, actions }: PageHeaderProps) {
  const left = eyebrow ? (
    <div>
      <div className="flex items-center gap-2 font-mono text-xs uppercase tracking-widest text-muted-foreground">
        {eyebrow}
      </div>
      <h1 className="mt-1 text-2xl font-semibold tracking-tight">{title}</h1>
      {description ? <p className="mt-0.5 text-sm text-muted-foreground">{description}</p> : null}
    </div>
  ) : (
    <div className="flex items-center gap-3">
      {icon ? (
        <div className="flex h-10 w-10 items-center justify-center rounded-md border border-border bg-card text-primary">
          {icon}
        </div>
      ) : null}
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
        {description ? <p className="mt-1 text-sm text-muted-foreground">{description}</p> : null}
      </div>
    </div>
  );

  if (!actions) {
    return <div className="flex items-center gap-3">{left}</div>;
  }
  return (
    <div className="flex flex-wrap items-center justify-between gap-4">
      {left}
      <div className="flex flex-wrap items-center gap-2">{actions}</div>
    </div>
  );
}

type KpiTone = "ok" | "warn";

const toneClass: Record<KpiTone, string> = {
  ok: "text-emerald-400",
  warn: "text-amber-400",
};

export function KpiCard({
  label,
  value,
  icon,
  tone,
  description,
}: {
  label: string;
  value: number | string;
  icon?: ReactNode;
  tone?: KpiTone;
  description?: ReactNode;
}) {
  const color = tone ? toneClass[tone] : "text-primary";
  return (
    <Card className="border-border/60 bg-card/60">
      <CardContent className="flex items-center justify-between p-4">
        <div>
          <div className="text-xs uppercase tracking-widest text-muted-foreground">{label}</div>
          <div className={cn("mt-1 font-mono text-2xl", color)}>{value}</div>
          {description ? (
            <div className="mt-0.5 text-xs text-muted-foreground">{description}</div>
          ) : null}
        </div>
        {icon ? (
          <div className={cn("rounded-md border border-border/60 bg-background/60 p-2", color)}>
            {icon}
          </div>
        ) : null}
      </CardContent>
    </Card>
  );
}

export function QueryErrorNotice({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  if (!error) return null;
  const message = error instanceof Error ? error.message : "请求失败，请稍后重试";

  return (
    <div
      role="alert"
      className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-destructive/30 bg-destructive/10 px-4 py-3 text-sm"
    >
      <div className="flex min-w-0 items-start gap-2">
        <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-destructive" />
        <div className="min-w-0">
          <div className="font-medium text-destructive">数据加载失败</div>
          <div className="break-words text-xs text-muted-foreground">{message}</div>
        </div>
      </div>
      {onRetry ? (
        <Button type="button" size="sm" variant="outline" onClick={onRetry}>
          <RefreshCcw className="h-3.5 w-3.5" />
          重试
        </Button>
      ) : null}
    </div>
  );
}

/**
 * Labelled form field with optional hint / inline error. Covers the old
 * `Field` (label only) and `FormField` (label + hint + error) variants.
 */
export function FormField({
  label,
  htmlFor,
  hint,
  error,
  children,
}: {
  label: string;
  htmlFor?: string;
  hint?: string;
  error?: string;
  children: ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <Label htmlFor={htmlFor} className="text-xs text-muted-foreground">
        {label}
      </Label>
      {children}
      {hint && !error ? <div className="text-[10px] text-muted-foreground">{hint}</div> : null}
      {error ? <div className="text-[10px] text-destructive">{error}</div> : null}
    </div>
  );
}
