import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { tanstackRouter } from "@tanstack/router-plugin/vite";
import { defineConfig } from "vite";

export default defineConfig({
  plugins: [
    tanstackRouter({
      target: "react",
      autoCodeSplitting: true,
    }),
    react(),
    tailwindcss(),
  ],
  resolve: {
    tsconfigPaths: true,
  },
  build: {
    // Docker 与 Release 只读取此目录；构建时先清空，避免旧哈希资源混入新版本。
    outDir: "dist",
    emptyOutDir: true,
  },
  server: {
    // 仅供本地开发；:: 会监听全部 IPv4/IPv6 接口，非可信网络应改为 127.0.0.1。
    host: "::",
    port: 5173,
  },
  preview: {
    // 预览服务器同样可能暴露到局域网，不能作为生产 Web 服务使用。
    host: "::",
    port: 4173,
  },
});
