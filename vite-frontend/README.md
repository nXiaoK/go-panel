# 前端构建配置

本目录是 go-panel 的 React/Vite SPA。`package.json` 与 `package-lock.json` 都是严格 JSON，语法不允许内联注释，因此在本文说明关键配置和风险。

## 运行版本

- GitHub Actions 与 Dockerfile 固定使用 Node.js 22。
- 当前 TanStack Start 相关依赖要求 Node.js 22.12.0 或更高版本；使用 Node.js 20 可能只显示警告，也可能在后续依赖升级后直接构建失败。
- `package-lock.json` 锁定完整依赖树，确保 CI、Docker 和本地 `npm ci` 得到一致版本。不要手工编辑锁文件，应通过 npm 命令更新并重新执行测试与安全审计。

## npm scripts

- `npm run dev`：仅用于本地开发的 Vite 服务，不应直接暴露到公网。
- `npm run build`：生成静态 SPA；Docker/Release 构建随后将其同步到 `web/dist` 并嵌入 Go 二进制。
- `npm run test`：依次执行 Node 测试和 Vitest 单元测试。
- `npm run typecheck`、`npm run lint:ci`、`npm run format:check`：GitHub Actions 的合并与发布门禁。
- `npm audit --omit=dev --audit-level=moderate`：检查生产依赖；发现中高危问题时发布会失败，修复后必须重新跑全套验证。

`package.json` 中的版本范围允许 npm 获取兼容补丁，`package-lock.json` 决定实际安装版本。升级 Vite、React、TanStack Router/Start 或 Tailwind 主版本可能改变路由生成、SSR/SPA 行为和样式输出，必须单独评估，不能只依赖编译通过。

## 其他构建配置

- `vite.spa.config.ts` 是 Docker、GitHub Release 和本地 `npm run build` 的正式入口，输出到会被 Go 嵌入的 `dist/`。开发/预览默认监听 `::`，可能暴露到局域网，非可信网络应改成 `127.0.0.1`。
- `vite.config.ts` 仅兼容原 Lovable/TanStack Start 开发环境，不参与正式 SPA 构建；其中的开发桥接和错误上报不能视为生产监控。
- `tsconfig.json` 开启严格类型并使用 Bundler 解析，`tsconfig.audit.json` 额外把未使用变量和参数视为错误。两者都只检查、不生成 JavaScript。
- `vitest.config.ts` 使用 jsdom 模拟浏览器，并把 `@` 映射到 `src/`；jsdom 通过不代表所有真实浏览器行为都已覆盖。
- `components.json` 固定 shadcn/ui 风格、Tailwind 入口和源码别名；修改别名必须同步 TypeScript、Vite 和测试配置，否则生成组件会写入错误目录。
- `.prettierrc` 规定 100 列、分号、双引号和尾逗号；`.prettierignore` 排除依赖、锁文件、生成路由及构建缓存，不能借此排除手写源码。
