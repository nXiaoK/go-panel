import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { RouterProvider } from "@tanstack/react-router";

import "./fonts.css";
import "./styles.css";
import { getRouter } from "./router";

const root = document.getElementById("root");

if (!root) {
  throw new Error("Root element #root was not found");
}

// 开发预览入口独立挂载；DEV常量在生产构建为false，动态import及预览代码被整体移除。
if (import.meta.env.DEV && window.location.pathname === "/_dev/components") {
  void import("./dev/components").then(({ ComponentPreview }) => {
    createRoot(root).render(
      <StrictMode>
        <ComponentPreview />
      </StrictMode>,
    );
  });
} else {
  createRoot(root).render(
    <StrictMode>
      <RouterProvider router={getRouter()} />
    </StrictMode>,
  );
}
