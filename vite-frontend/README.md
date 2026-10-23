# 前端构建配置

本目录是 go-panel 的 React/Vite SPA。`package.json` 与 `package-lock.json` 都是严格 JSON，语法不允许内联注释，因此在本文按模块说明配置和风险。

## 运行版本

- GitHub Actions 与 Dockerfile 固定使用 Node.js 22；Vite 8 / Vitest 4 的本地开发基线为 Node.js 22.12+。
- `package.json` 的 `engines.node: ">=22.12.0"` 声明最低版本；`.nvmrc` 的 `22` 选择 Node 22 的可用补丁版本。在本目录执行 `nvm install && nvm use` 后再安装。
- `.npmrc` 的 `engine-strict=true` 使版本不符的 `npm ci` 直接以 `EBADENGINE` 失败，避免安装后才在构建阶段报错。不要关闭该约束来绕过版本要求。
- `package-lock.json` 锁定完整依赖树，确保 CI、Docker 和本地 `npm ci` 得到一致版本。锁文件根节点的 `engines` 与 `package.json` 同步；不要手工编辑锁文件，应通过 npm 命令更新并重新执行测试与安全审计。

## npm scripts 与依赖范围

- `npm run dev`：仅用于本地开发的 Vite 服务，不应直接暴露到公网。
- `npm run build`：生成静态 SPA；Docker/Release 构建随后将其同步到 `web/dist` 并嵌入 Go 二进制。`build:start` 只是兼容已有调用的同一 SPA 构建别名。
- `npm run test`：依次执行 Node 测试和 Vitest 单元测试。
- `npm run typecheck`、`npm run lint:ci`、`npm run format:check`：GitHub Actions 的合并与发布门禁。
- `npm audit --omit=dev --audit-level=moderate`：检查生产依赖；发现中高危问题时发布会失败，修复后必须重新跑全套验证。

`package.json` 只保留 SPA 所需的组件、查询、路由和构建依赖，已移除未使用的 SSR 开发链与抽屉依赖。`cmdk` 与 `src/components/ui/command.tsx` 保留，供命令面板使用。版本范围允许 npm 获取兼容补丁，实际安装以 `package-lock.json` 为准；升级 Vite、React、TanStack Router 或 Tailwind 主版本可能改变路由生成和样式输出，必须单独评估。

## 本地开发代理

先运行 Go 后端，再启动 Vite：

```bash
# 多实例并行时为每个 Vite 服务指定对应的后端端口。
VITE_DEV_API_TARGET=http://127.0.0.1:6401 npm run dev -- --port 5201
```

`vite.spa.config.ts` 的开发代理将 `/api`、`/flow` 和 `/system-info` 转发到 `VITE_DEV_API_TARGET`，其中 `/system-info` 支持 WebSocket 升级。默认目标为 `http://127.0.0.1:6365`。此变量由 Vite 进程环境读取，可在启动命令中设置；根目录部署用的 `.env` 不会自动传给 Vite。

代理仅在 `npm run dev` 时生效，生产构建和 Compose 容器不读取此变量，正式部署仍同源访问 Go 后端。不要把目标指向不可信服务器，否则 API 请求与登录凭据也会被转发。开发/预览默认监听 `::`，可能暴露到局域网，非可信网络应传入 `--host 127.0.0.1`。

## 浏览器兼容性依赖

`npm ls browserslist` 显示 `browserslist@4.28.9` 仍由 `@tanstack/router-plugin → @babel/core → @babel/helper-compilation-targets` 引入，并在生产依赖树中。它与已删除的 SSR 入口无关。`4.28.6` 及更早版本受 GHSA-c83g-rgw3-j3cx 和 GHSA-73wf-gq98-2v4g 影响，可能因查询缓存无限增长或异常自定义统计数据导致内存耗尽、进程崩溃或原型写入；不能回退版本或降低审计门槛。

同一锁文件中的 `baseline-browser-mapping`、`caniuse-lite`、`electron-to-chromium`、`node-releases` 和 `update-browserslist-db` 提供浏览器/运行时版本数据及更新工具，随 Browserslist 的依赖要求同步升级。默认构建读取锁定的数据版本；数据变化可能影响浏览器目标选择，更新后需使用 Node.js 22 执行完整检查及生产依赖审计。

## 安全依赖锁定

默认 `npm ci` 严格安装锁定版本。删除锁文件或回退版本可能重新引入已修复的问题；更新后必须重新执行完整前端检查和生产依赖审计，不得降低审计门槛。

- 网络请求模块：`axios@1.20.0` 与 `follow-redirects@1.16.1` 修复请求选项、请求头、代理规则和重定向处理中的安全问题。后续升级需验证登录凭据、错误处理及下载请求。
- 路由模块：`@tanstack/router-plugin@1.168.48` 与 `@tanstack/react-router@1.170.46` 保留已有安全锁定版本，`seroval` / `seroval-plugins` 为 `1.7.0`。路由生成依赖随树更新，可能改变生成代码和格式检查结果，需完整复验。根路由错误边界使用 `ErrorComponentProps` 处理未知抛出值，只输出浏览器控制台错误并提供重试。
- 构建与源码映射模块：`vite@8.3.5` 与 `source-map-js@1.2.2` 保留已有修复。开发服务只用于本地调试；更新构建链可能改变静态文件哈希、分块和 CSS 输出，必须重新构建、同步 `web/dist` 并验证 Go 内嵌页面。
- YAML 工具：`npm ls js-yaml` 显示只剩开发依赖 `eslint → @eslint/eslintrc → js-yaml@4.3.2`；`npm ls --omit=dev js-yaml` 已为空，生产树不再包含它。`4.0.0` 至 `4.3.1` 受 GHSA-2883-xcq3-v3hh 影响，空 YAML 合并源可绕过资源限制并耗尽 CPU；开发依赖同样不能回退到已知受影响版本。

锁文件通过 Node.js 22 / npm 10 的标准卸载与锁文件更新命令生成，没有使用 `--force`、忽略 peer 依赖或依赖覆盖规则。

## 其他构建配置

- `vite.spa.config.ts` 是 Docker、GitHub Release、本地开发与构建的统一入口，输出到 Go 嵌入的 `dist/`。
- `tsconfig.json` 的 `include` 仅检查实际 SPA 源码及现存配置，已移除废弃开发入口；开启严格类型与 Bundler 解析。`tsconfig.audit.json` 额外把未使用变量和参数视为错误。两者都只检查、不生成 JavaScript。
- `eslint.config.js` 禁止浏览器代码引入 `server-only` 标记，避免误把服务端假设带入客户端；此约束不会新增服务端入口。
- `vitest.config.ts` 使用 jsdom 模拟浏览器，并把 `@` 映射到 `src/`；jsdom 通过不代表所有真实浏览器行为都已覆盖。
- `components.json` 固定 shadcn/ui 风格、Tailwind 入口和源码别名；修改别名必须同步 TypeScript、Vite 和测试配置。
- `.prettierrc` 规定 100 列、分号、双引号和尾逗号；`.prettierignore` 排除依赖、锁文件、生成路由及构建缓存，不能借此排除手写源码。

废弃的自动刷新 hook 测试已迁移到 `src/routes/-_app.node-refresh.test.tsx`，直接挂载节点页与真实 React Query 客户端，覆盖定时刷新、慢请求去重、隐藏暂停/恢复、偏好关闭/启用、卸载清理及离线恢复。

## API 客户端与接口类型

`src/lib/api/client.ts` 维护唯一的 Axios 实例。每次请求重新读取面板端点与令牌；`RequestOptions.signal` 透传取消信号，React Query 的查询都消费该信号，切换查询条件或卸载时中止传输。普通请求默认 30 秒、通用上传保留 60 秒；节点升级 100 秒、诊断 95 秒、测速按测试时长加 50 秒、备份传输 10 分钟，避免缩短已有长请求窗口。

成功响应直接返回 `data`，业务失败、HTTP 失败、网络错误和超时统一抛出 `ApiError`；`code` 保留业务码（网络错误为 -1，超时为 -2），`httpStatus` 保留 HTTP 状态。错误优先显示后端中文 `msg`，缺失时使用中文兜底。下载遇到 JSON 错误体也按相同规则解析，正常下载采用后端 `Content-Disposition` 文件名。

受保护接口的 HTTP 401 优先于响应体业务码，清除会话并回到登录页。业务 401、账号禁用 4031、强制改密 4032 及旧版两条明确的 403 中文消息沿用兼容行为；普通权限 403 保留会话。登录等公开请求失败不会撤销已有登录状态。

资源接口位于 `src/lib/api/{auth,user,node,tunnel,forward,limit,config,system,backup,subscription}.ts`，请求字段按后端 DTO、响应按 JSON tag 定义；数组或 `{ records }` 列表在资源边界转换，页面只消费数组。`/user/list` 返回全部非管理员用户，不再发送无效分页参数。表单、诊断、测速与批量操作在失败后恢复操作状态，且保留普通用户转发编辑时省略出口配置的权限语义。
