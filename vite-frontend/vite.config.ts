// Lovable 开发环境配置已经内置 TanStack Start、React、Tailwind、路径别名、Nitro、
// 开发标记和错误上报插件，不得重复注册，否则会出现重复插件或构建冲突。
// 正式 Docker/Release 构建使用 vite.spa.config.ts；本文件只保留兼容原开发工作流。
import { defineConfig } from "@lovable.dev/vite-tanstack-config";

export default defineConfig({
  tanstackStart: {
    // SSR 开发构建改用带错误边界的 src/server.ts；生产 SPA 不运行此服务端入口。
    server: { entry: "server" },
  },
});
