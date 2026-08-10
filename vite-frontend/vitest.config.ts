import { fileURLToPath } from "node:url";
import { defineConfig } from "vitest/config";

export default defineConfig({
  resolve: {
    alias: {
      // 与生产构建保持一致，测试中的 @ 始终解析到前端源码目录。
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
  test: {
    // 组件测试依赖浏览器 DOM 模拟；jsdom 不等同真实浏览器，关键交互仍需人工冒烟。
    environment: "jsdom",
    include: ["test/**/*.test.{ts,tsx}", "src/**/*.test.{ts,tsx}"],
  },
});
