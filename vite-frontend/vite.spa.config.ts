import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { tanstackRouter } from "@tanstack/router-plugin/vite";
import { defineConfig } from "vite";

// 仅开发服务器读取此目标，生产构建仍同源请求 Go 后端；多实例开发时分别指定对应端口。
const devApiTarget = process.env.VITE_DEV_API_TARGET ?? "http://127.0.0.1:6365";

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
    // npm run dev 将 API、流量上报和节点状态 WebSocket 转发到本地 Go 后端；
    // 默认 6365，VITE_DEV_API_TARGET 可隔离并行实例，不能指向不可信服务以免泄露登录凭据。
    proxy: {
      "/api": { target: devApiTarget },
      "/flow": { target: devApiTarget },
      "/system-info": { target: devApiTarget, ws: true },
    },
  },
  preview: {
    // 预览服务器同样可能暴露到局域网，不能作为生产 Web 服务使用。
    host: "::",
    port: 4173,
  },
});
