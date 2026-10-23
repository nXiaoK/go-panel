import * as React from "react";

import { cn } from "@/lib/utils";

const Input = React.forwardRef<HTMLInputElement, React.ComponentProps<"input">>(
  ({ className, type, ...props }, ref) => {
    return (
      <input
        type={type}
        className={cn(
          "flex h-9 max-md:min-h-10 w-full rounded-sm border border-border-control bg-canvas-soft px-3 py-2 text-sm leading-5 text-ink transition-colors placeholder:text-mute focus-visible:border-primary-soft focus-visible:ring-2 focus-visible:ring-primary-soft/35 aria-invalid:border-status-critical disabled:cursor-not-allowed disabled:opacity-40",
          className,
        )}
        ref={ref}
        {...props}
      />
    );
  },
);
Input.displayName = "Input";

export { Input };
