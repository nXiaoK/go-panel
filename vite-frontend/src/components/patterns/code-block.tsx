import type { ReactNode } from "react";
import { CopyButton } from "./copy-button";

export function CodeBlock({
  code,
  label = "复制命令",
  singleLine = false,
  trailingActions,
}: {
  code: string;
  label?: string;
  singleLine?: boolean;
  trailingActions?: ReactNode;
}) {
  if (singleLine) {
    return (
      <div className="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-2 rounded-md border border-hairline bg-canvas-soft p-3">
        <code title={code} className="min-w-0 truncate font-mono text-code text-canvas-text-soft">
          {code}
        </code>
        <div className="flex min-w-0 flex-wrap items-center justify-end gap-1">
          <CopyButton text={code} label={label} iconOnly />
          {trailingActions}
        </div>
      </div>
    );
  }
  return (
    <div className="min-w-0 rounded-md border border-hairline bg-canvas-soft p-3">
      <div className="mb-2 flex flex-wrap items-center justify-end gap-1">
        <CopyButton text={code} label={label} />
        {trailingActions}
      </div>
      <pre className="whitespace-pre-wrap break-all font-mono text-code text-canvas-text-soft">
        <code>{code}</code>
      </pre>
    </div>
  );
}
