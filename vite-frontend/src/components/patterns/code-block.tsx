import { CopyButton } from "./copy-button";
export function CodeBlock({ code, label = "复制命令" }: { code: string; label?: string }) {
  return (
    <div className="rounded-md border border-hairline bg-canvas-soft p-3">
      <div className="mb-2 flex justify-end">
        <CopyButton text={code} label={label} />
      </div>
      <pre className="whitespace-pre-wrap break-all font-mono text-code text-canvas-text-soft">
        <code>{code}</code>
      </pre>
    </div>
  );
}
