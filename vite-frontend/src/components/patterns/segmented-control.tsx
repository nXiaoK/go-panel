import { cn } from "@/lib/utils";
export function SegmentedControl<T extends string>({
  value,
  onChange,
  options,
  label,
}: {
  value: T;
  onChange: (value: T) => void;
  options: readonly { value: T; label: string; disabled?: boolean }[];
  label: string;
}) {
  return (
    <div
      role="group"
      aria-label={label}
      className="inline-flex min-h-8 flex-wrap rounded-sm bg-canvas-soft p-0.5"
    >
      {options.map((option) => (
        <button
          key={option.value}
          type="button"
          aria-pressed={option.value === value}
          disabled={option.disabled}
          onClick={() => onChange(option.value)}
          className={cn(
            "min-h-7 rounded-sm border border-transparent px-3 text-caption-strong text-mute disabled:opacity-40 max-md:min-h-10",
            option.value === value && "border-hairline bg-canvas text-ink",
          )}
        >
          {option.label}
        </button>
      ))}
    </div>
  );
}
