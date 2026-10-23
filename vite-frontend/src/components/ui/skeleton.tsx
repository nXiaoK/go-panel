import { cn } from "@/lib/utils";

function Skeleton({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn(
        "animate-pulse [animation-duration:1.2s] rounded-sm bg-hairline-subtle motion-reduce:animate-none",
        className,
      )}
      {...props}
    />
  );
}

export { Skeleton };
