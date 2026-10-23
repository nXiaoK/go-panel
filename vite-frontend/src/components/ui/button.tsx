import * as React from "react";
import { Slot } from "@radix-ui/react-slot";
import { LoaderCircle } from "lucide-react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";

const primary = "bg-primary text-on-primary hover:brightness-105";
const solid = "bg-destructive-solid text-on-destructive hover:brightness-105";
const buttonVariants = cva(
  "relative inline-flex shrink-0 items-center justify-center gap-2 whitespace-nowrap rounded-sm text-button cursor-pointer transition-colors duration-150 focus-visible:ring-2 focus-visible:ring-primary-soft disabled:pointer-events-none disabled:opacity-40 disabled:cursor-not-allowed [&_svg]:pointer-events-none [&_svg]:size-4 [&_svg]:shrink-0 max-md:min-h-10 max-md:min-w-10",
  {
    variants: {
      variant: {
        primary,
        default: primary,
        outline: "border border-hairline bg-transparent text-ink hover:bg-surface-hover",
        ghost: "text-body hover:bg-surface-hover hover:text-ink",
        danger:
          "border border-status-critical text-status-critical-text hover:bg-status-critical-bg",
        "danger-solid": solid,
        destructive: solid,
        secondary: "bg-canvas-soft text-ink hover:bg-surface-hover",
        link: "text-primary-deep underline-offset-4 hover:underline",
      },
      size: {
        default: "h-9 px-4",
        md: "h-9 px-4",
        sm: "h-8 px-4",
        lg: "h-10 px-5",
        icon: "size-9",
        "icon-sm": "size-8",
      },
    },
    defaultVariants: { variant: "default", size: "default" },
  },
);
export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>, VariantProps<typeof buttonVariants> {
  asChild?: boolean;
  loading?: boolean;
}
const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  (
    {
      className,
      variant,
      size,
      asChild = false,
      loading = false,
      disabled,
      children,
      onClick,
      onClickCapture,
      ...props
    },
    ref,
  ) => {
    const Comp = asChild ? Slot : "button";
    const blocked = Boolean(disabled || loading);
    const iconOnly = size === "icon" || size === "icon-sm";
    const content = (label: React.ReactNode) => (
      <>
        {loading && (
          <LoaderCircle
            aria-hidden="true"
            className={cn(
              "absolute left-1 size-3.5! animate-spin motion-reduce:animate-none",
              iconOnly && "left-1/2 -translate-x-1/2",
            )}
          />
        )}
        <span
          className={cn(
            "inline-flex items-center justify-center gap-2",
            loading && (iconOnly ? "opacity-0" : "translate-x-1.5"),
          )}
        >
          {label}
        </span>
      </>
    );
    let renderedChildren = content(children);
    if (
      asChild &&
      React.isValidElement<
        React.HTMLAttributes<HTMLElement> & { children?: React.ReactNode; tabIndex?: number }
      >(children)
    ) {
      // Slot的bubble handler先调用child；捕获阶段在子动作/链接默认导航之前拦截禁用状态。
      const child = children;
      renderedChildren = React.cloneElement(
        child,
        {
          "aria-busy": loading || undefined,
          "aria-disabled": blocked || undefined,
          tabIndex: blocked ? -1 : child.props.tabIndex,
          onClickCapture: (event) => {
            if (blocked) {
              event.preventDefault();
              event.stopPropagation();
              return;
            }
            child.props.onClickCapture?.(event);
          },
        },
        content(child.props.children),
      );
    }
    return (
      <Comp
        {...props}
        className={cn(buttonVariants({ variant, size, className }))}
        ref={ref}
        disabled={blocked}
        aria-disabled={blocked || undefined}
        aria-busy={loading || undefined}
        onClickCapture={(event) => {
          if (blocked) {
            event.preventDefault();
            event.stopPropagation();
            return;
          }
          onClickCapture?.(event);
        }}
        onClick={(event) => {
          if (blocked) {
            event.preventDefault();
            return;
          }
          onClick?.(event);
        }}
      >
        {renderedChildren}
      </Comp>
    );
  },
);
Button.displayName = "Button";
export { Button, buttonVariants };
