import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { RouterProvider } from "@tanstack/react-router";

import "./fonts.css";
import "./styles.css";
import { installPreloadRecovery } from "./lib/preload-recovery";
import { getRouter } from "./router";

const root = document.getElementById("root");

if (!root) {
  throw new Error("Root element #root was not found");
}

// 面板更新后旧页面可能加载不到已被替换的代码分块：最多自动刷新一次，规则见 preload-recovery.ts。
installPreloadRecovery();

// 开发预览入口独立挂载；DEV常量在生产构建为false，动态import及预览代码被整体移除。
if (import.meta.env.DEV && window.location.pathname === "/_dev/components") {
  void import("./dev/components").then(({ ComponentPreview }) => {
    createRoot(root).render(
      <StrictMode>
        <ComponentPreview />
      </StrictMode>,
    );
  });
} else if (
  import.meta.env.DEV &&
  new URLSearchParams(window.location.search).has("__profileNodes")
) {
  // 显式开发参数只增加真实页面的测量器，不生成虚拟性能值；正式构建整体移除。
  void import("./dev/node-profiler").then(({ NodeProfiler }) => {
    createRoot(root).render(
      <NodeProfiler>
        <RouterProvider router={getRouter()} />
      </NodeProfiler>,
    );
  });
} else {
  createRoot(root).render(
    <StrictMode>
      <RouterProvider router={getRouter()} />
    </StrictMode>,
  );
}
