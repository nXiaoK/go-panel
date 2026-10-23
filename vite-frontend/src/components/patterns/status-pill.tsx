import { createElement, isValidElement, type ReactNode } from "react";
import type { LucideIcon } from "lucide-react";
import type { StatusTone } from "@/lib/status";
import { cn } from "@/lib/utils";
const tones: Record<StatusTone, string> = {
  good: "bg-status-good-bg text-status-good",
  warning: "bg-status-warning-bg text-status-warning",
  serious: "bg-status-serious-bg text-status-serious",
  critical: "bg-status-critical-bg text-status-critical-text",
  neutral: "bg-canvas-soft text-status-neutral",
};
export function StatusPill({
  tone,
  label,
  icon,
  hollow = false,
  className,
}: {
  tone: StatusTone;
  label: string;
  icon?: LucideIcon | ReactNode;
  hollow?: boolean;
  className?: string;
}) {
  return (
    <span
      className={cn(
        "inline-flex h-[22px] shrink-0 items-center gap-1.5 rounded-pill px-2 text-caption-strong",
        tones[tone],
        className,
      )}
    >
      {icon ? (
        <span aria-hidden="true" className="[&_svg]:size-3">
          {isValidElement(icon) || typeof icon === "string"
            ? icon
            : createElement(icon as LucideIcon, { size: 12 })}
        </span>
      ) : (
        <span
          aria-hidden="true"
          className={cn("size-1.5 rounded-full", hollow ? "border border-current" : "bg-current")}
        />
      )}
      {label}
    </span>
  );
}
