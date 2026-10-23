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

## 浏览器兼容性依赖

`package-lock.json` 中的 `node_modules/browserslist` 由 Babel/TanStack 构建链间接引入，当前锁定为 `4.28.9`。`4.28.6` 及更早版本受 GHSA-c83g-rgw3-j3cx 和 GHSA-73wf-gq98-2v4g 影响，可能因查询缓存无限增长或异常自定义统计数据导致内存耗尽、进程崩溃或原型写入；不能为通过构建而回退版本或降低审计门槛。

同一锁文件中的 `baseline-browser-mapping`、`caniuse-lite`、`electron-to-chromium`、`node-releases` 和 `update-browserslist-db` 提供浏览器/运行时版本数据及更新工具，随 Browserslist 的依赖要求同步升级。默认构建读取锁定的数据版本；数据变化可能影响浏览器目标选择，因此更新后需使用 Node.js 22 执行 `npm ci`、前端完整检查及生产依赖审计。

## 安全依赖锁定

`package-lock.json` 的安全更新继续使用 `package.json` 已有的兼容版本范围，不增加运行功能或依赖覆盖规则。默认 `npm ci` 严格安装以下锁定版本；删除锁文件或回退版本可能重新引入已修复的问题。更新后必须重新执行完整前端检查和 `npm audit --omit=dev --audit-level=moderate`，不得降低审计门槛。

- 网络请求模块：`axios` 锁定为 `1.20.0`，其 Node.js 重定向依赖 `follow-redirects` 为 `1.16.1`。这些更新修复请求选项、请求头、代理规则和重定向处理中的安全问题；浏览器 API 调用仍使用既有 Axios 配置。后续升级需验证登录凭据、错误处理及下载请求，不能依赖受影响版本的请求继承行为。
- TanStack 路由和 Start 模块：`@tanstack/react-start` 为 `1.169.4`、`@tanstack/router-plugin` 为 `1.168.48`、`@tanstack/react-router` 为 `1.170.46`，连带锁定匹配的 `start-server-core`、`start-plugin-core`、客户端、存储和路由生成依赖。Start 服务端响应与 `seroval` / `seroval-plugins` 的序列化安全修复分别由 `start-server-core@1.169.45` 和 `1.7.0` 提供；即使正式构建使用静态 SPA，也保留这些遗留依赖的修复版本，避免其他开发入口误用受影响版本。路由生成所需的 Babel、Prettier、Zod、文件匹配与同步存储依赖随依赖树更新，可能改变生成代码和格式检查结果，需完整复验。根路由错误边界采用库导出的 `ErrorComponentProps`，兼容新版把抛出值定义为 `unknown` 的类型约束，不改变错误上报和重试行为。新版 Router 生成器还会重排 `src/routeTree.gen.ts` 中的导入与声明，路由路径、父子关系和可访问页面保持不变，生成结果随锁文件提交以保持构建可复现。
- 构建与源码映射模块：`vite` 为 `8.3.5`、`source-map-js` 为 `1.2.2`，相应 Rolldown 原生平台包、Lightning CSS、PostCSS 及构建辅助包按 Vite / Start 的依赖要求同步锁定。修复覆盖开发服务文件访问、编辑器集成和源码映射资源消耗问题；开发服务器仍只用于本地调试。更新构建链可能改变静态文件哈希、分块和 CSS 输出，必须重新构建并同步 `web/dist` 后验证 Go 内嵌页面。

本次锁文件由临时 npm `11.21.1` 在 Node.js 22 下定向更新，再用 npm `10.9.9` 的 `npm install --package-lock-only` 校正跨版本锁文件条目，原因是 npm `10.9.9` 的更新解析出现内部异常；没有使用 `--force`、忽略 peer 依赖或新增 npm 配置。日常及 CI 仍可使用原 npm 版本运行 `npm ci`，以锁文件和完整检查结果为准。

## 其他构建配置

`package-lock.json` 中的 `node_modules/js-yaml` 由 TanStack Start → `xmlbuilder2` 和 ESLint 间接引入，当前锁定为兼容现有 `^4.1.1` 范围的 `4.3.2`。`4.0.0` 至 `4.3.1` 受 GHSA-2883-xcq3-v3hh 影响，空 YAML 合并源可绕过 `maxTotalMergeKeys` 限制并造成 CPU 资源耗尽。默认 `npm ci` 按锁文件安装修复版本；不要回退到受影响版本或降低生产依赖审计门槛，后续更新需重新执行前端完整检查及安全审计。

- `vite.spa.config.ts` 是 Docker、GitHub Release 和本地 `npm run build` 的正式入口，输出到会被 Go 嵌入的 `dist/`。开发/预览默认监听 `::`，可能暴露到局域网，非可信网络应改成 `127.0.0.1`。
- `vite.config.ts` 仅兼容原 Lovable/TanStack Start 开发环境，不参与正式 SPA 构建；其中的开发桥接和错误上报不能视为生产监控。
- `tsconfig.json` 开启严格类型并使用 Bundler 解析，`tsconfig.audit.json` 额外把未使用变量和参数视为错误。两者都只检查、不生成 JavaScript。
- `vitest.config.ts` 使用 jsdom 模拟浏览器，并把 `@` 映射到 `src/`；jsdom 通过不代表所有真实浏览器行为都已覆盖。
- `components.json` 固定 shadcn/ui 风格、Tailwind 入口和源码别名；修改别名必须同步 TypeScript、Vite 和测试配置，否则生成组件会写入错误目录。
- `.prettierrc` 规定 100 列、分号、双引号和尾逗号；`.prettierignore` 排除依赖、锁文件、生成路由及构建缓存，不能借此排除手写源码。
