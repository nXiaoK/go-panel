import { Profiler, useRef, type ReactNode, type ProfilerOnRenderCallback } from "react";

/** 仅开发构建挂载，记录真实 React commit；生产构建不加载夹具或启用 Profiler。 */
export function NodeProfiler({ children }: { children: ReactNode }) {
  const output = useRef<HTMLOutputElement>(null);
  const samples = useRef<{ phase: string; actualDuration: number; commitTime: number }[]>([]);
  const record: ProfilerOnRenderCallback = (
    _id,
    phase,
    actualDuration,
    _baseDuration,
    _startTime,
    commitTime,
  ) => {
    samples.current.push({ phase, actualDuration, commitTime });
    if (output.current) output.current.textContent = JSON.stringify(samples.current);
  };
  return (
    <>
      <Profiler id="node-page" onRender={record}>
        {children}
      </Profiler>
      <output
        ref={output}
        id="node-profiler-samples"
        aria-label="开发节点性能记录"
        className="sr-only"
      />
    </>
  );
}
