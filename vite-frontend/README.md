# 前端构建配置

本目录是 go-panel 的 React/Vite SPA。`package.json` 与 `package-lock.json` 都是严格 JSON，语法不允许内联注释，因此在本文按模块说明配置和风险。

## 视觉规范

界面开发以仓库根目录的 [DESIGN.md](../DESIGN.md) 为准。控制台只保留暗色主题，使用语义令牌、自托管字体和统一组件；不得使用 Tailwind 原生色板。新增颜色组合必须计算对比度，文字至少 4.5:1，控件边界和图形至少 3:1。

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
- 路由模块：`@tanstack/router-plugin@1.168.48` 与 `@tanstack/react-router@1.170.46` 保留已有安全锁定版本，`seroval` / `seroval-plugins` 为 `1.7.0`。路由生成依赖随树更新，可能改变生成代码和格式检查结果，需完整复验。根路由错误边界使用 `ErrorComponentProps` 处理未知抛出值，提供重试；错误堆栈只在开发环境的折叠区展示。
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

### 构建预压缩（package.json 的 scripts.build）

`npm run build` 先运行 Vite，再执行 `scripts/precompress.mjs`。脚本递归为 `dist/assets/` 中的 JS、CSS、SVG 生成 gzip level 9 的 `.gz` 和 Brotli quality 11 的 `.br` 文件，不添加依赖；原始文件始终保留，供不支持压缩的客户端回退。高压缩级别只增加构建时间，不增加服务时的压缩开销。`scripts/sync-web-dist.sh` 会一起复制这些旁路文件；带哈希资源缓存一年，页面入口保持 `no-store`，因此发布时必须同步完整构建目录。

### 会话、通知与站点标题

身份通过 `lib/session.ts` 的快照和 `useSession()` 订阅，沿用 `token`、`role_id`、`name`、`must_change_pwd` 四个持久化键，旧会话无需迁移。只有角色 `0` 显示为“管理员”，其他角色均显示“普通用户”。跨标签页身份变化会同步界面并清空查询和搜索缓存；强制改密期间继续停止业务请求。

所有业务通知使用 `lib/notify.ts`，ESLint 禁止业务代码直接导入 `sonner`。本地 `pref_notify=0` 会关闭成功、信息及长操作加载提示；错误与警告始终显示，避免隐藏失败。`notify.promise()` 保留原操作结果与 rejection。

`app_name` 优先使用已有数据库配置，空值回退 `go-panel`；新库只在该项缺失时写入默认值，已有名称不会迁移。页面标题由根组件统一组合为“页面名 · 站点名称”，配置保存和路由切换即时更新，`HeadContent` 继续管理其余 meta。

字节和速率统一使用 1024 进制，数值小于 10 保留两位、小于 100 保留一位，否则取整；测速保留原有 Mbps 转 MB/s 的两位小数。日期使用 `zh-CN`，无效值显示破折号。危险操作使用共享异步确认框，节点删除及备份恢复需输入名称或备份文件名；这只加强操作确认，不改变后端权限。

## 字体、主题与设计令牌配置

`package.json` / `package-lock.json` 新增的 `@fontsource-variable/inter` 和 `@fontsource-variable/jetbrains-mono` 用于自托管字体，不访问 Google Fonts 或字体 CDN。锁定版本的 `wght.css` 含所有语种，且未提供 latin 专用 CSS 入口，因此 `src/main.tsx` 导入本地 `src/fonts.css`，只声明两包 `files/*-latin-wght-normal.woff2`，保留上游的字重范围、`unicode-range` 与 `font-display: swap`；不导入 cyrillic、greek、latin-ext 等子集，中文回退系统字体。实际分发的 Inter latin 子集已移除 `cv02/cv03/cv04/cv11/ss01`，正文只配置实际保留的 `calt`，等宽字体不继承这些字形配置。字体许可均为 SIL OFL 1.1，版权和完整许可见根 [THIRD_PARTY_NOTICES.md](../THIRD_PARTY_NOTICES.md) 以及随页面分发的 `public/font-licenses/`，升级字体时必须同步这些声明。

`index.html` 固定 `class="dark"`、`color-scheme: dark` 和暗色元数据；主题无脚本、无切换或持久化读写，旧浏览器 `theme` 值不影响结果。`src/styles.css` 按根设计规范提供语义颜色与 shadcn 兼容映射，圆角 `sm=6px`、`md=8px`，命名间距使用 `space-` 前缀（例如 `p-space-sm`），避免与 `max-w-sm` / `max-w-xl` 等容器尺寸冲突；普通卡片只使用边线，只有浮层和交互卡片悬停使用阴影。`destructive` 仅用于图形/边界，危险确认实心按钮使用 `destructive-solid` 与 `ink`，避免文字对比不足。焦点轮廓始终显示；减弱动态偏好关闭动画与过渡但保留布局定位。

`test/design-tokens.test.mjs` 对所有 TSX 字符串、模板和词法常量拼接执行零违规门禁，覆盖完整 Tailwind 色板、渐变/光晕/网格特效、小于12px的px/rem字号。历史额度收紧回归仍保留，不能新增任何允许项。全部业务目录统一执行 no-explicit-any，不再豁免路由边界。

## U02 共享组件与开发预览

`package.json` / `package-lock.json` 新增 `@tanstack/react-table`，供统一 DataTable 使用；新增 `@radix-ui/react-popover`，提供具备焦点管理与键盘关闭的 Popover。两者均为本地打包的运行依赖，不访问外部服务，不改变业务权限或生产审计门槛。

Node 22 下运行 `npm run dev` 后访问 `/_dev/components`，可检查基础控件、列表、表单、状态、配额、复制、诊断与抽屉。入口仅在 `import.meta.env.DEV` 下动态挂载，生产构建会移除入口与整个预览模块；不将它添加到正式路由树。发布前扫描 `dist/assets`，不得包含唯一标记“组件验收实验室 U02”。

开发预览可用 `/_dev/components?batch=1` 单独验证 50 行长列表：它复用正式 `AppContent` 容器，包含手机长卡片、换行批量操作、抽屉和确认框；数据全部是内存夹具，不访问业务接口。加 `&legacy=1` 仅在开发环境复现旧 `overflow-x-hidden` 滚动祖先行为，不用于正式布局。两个模式都随整个预览模块从生产构建移除。正式内容区使用 `overflow-x-clip` 只裁切横向溢出，避免浏览器将纵向溢出隐式变为 `auto`，让共享列表的吸底批量条继续跟随页面滚动；不要在页面外层重新添加不实际滚动的 `overflow-hidden/auto` 容器。

页面从 `components/patterns/` 按组件路径导入。`DataTable` 必须提供稳定 `getRowId` 和 `renderMobileCard`；`sorting/onSortingChange`、`rowSelection/onRowSelectionChange` 为 TanStack 受控状态，`initialRowSelection` 可预选。`onRowActivate` 同时支持鼠标和回车/空格，内嵌按钮、复选框、链接不触发行激活。操作列使用 `meta.actions`，数值列使用 `meta.numeric` 与 `meta.align="right"`。筛选空态传 `filtered/onClearFilters`。`focusRowId` 等待异步数据后清选择、滚动、聚焦并高亮2秒。

`useUrlState(defaults)` 返回 `{state,setState}`，只合并修改的 search 字段并 replace 历史。各页面必须在路由 `validateSearch` 中验证并保留需要的 `q/status/sort/focus` 等字段，避免路由丢弃共享链接中的状态；分页同样由页面声明。该工具不会重载整页。

`EntitySheet` 的 `onSubmit` 可省略，以支持只读详情与自定义表单，避免嵌套 form。默认底栏通过 `footerActions` 加左侧操作；完全自定义时用 `footer={({requestClose,submitting}) => ...}`，关闭操作必须调用 `requestClose` 以保留 dirty 确认。提交期间控件禁止重复提交与关闭。确认层会隔离内部点击，避免关闭动画期间误触发底层抽屉的外部点击。`useConfirm` / `usePrompt` 在整组队列结束后回焦到最初的页面控件；排队期间不把焦点移出当前确认框。

`CodeBlock` 默认保留多行命令并自动换行；订阅链接可传 `singleLine`，完整文本通过 title 与内建复制按钮保留，视觉上仅单行省略。`label` 用作复制按钮说明，`trailingActions` 只传打开链接、二维码等其他动作，不再额外添加复制按钮。单行复制使用图标按钮，复制成功与失败都通过状态文字反馈；`CopyButton` 的 `iconOnly` 是可选紧凑展示，默认文字按钮行为保持不变。

`DiagnosisSheet` 直接接收 `DiagnosisResult | NodeStatusCheck`；advisory 外网失败只显示警告，不计链路失败。`lib/batch.runBatch(items,action,{nameOf,onProgress,concurrency})` 默认且最多4并发，结果按输入排序并逐项保留 success/reason；进度提供 done/total。增加并发会同时发送写请求，页面应展示部分失败摘要，禁止无限并发。

字体注意：`text-body` 是正文颜色令牌；由于同名的颜色/字号歧义，Tailwind 会把该类解析为颜色。正文14px/20px请显式使用 `text-sm leading-5`，并搭配 `text-ink`、`text-body` 或 `text-mute`；不要用 `text-body` 代替字号。

`FormField` 会给单个真实控件或可透传 DOM 属性的代理控件注入 id/aria，复用控件已有 id。Select 根通过上下文把关联传给 Trigger，RadioGroup 通过 `aria-labelledby` 关联组标题。布局 div/多控件字段必须使用 render prop，为各控件设置唯一 id；第一项可关联主 label，其余设置独立 aria-label，错误说明可共享。基础 Input 不再向嵌套的多个字段传播同一个 id。复制、清搜索和重试等共享动作明确使用 `type="button"`，避免位于实体表单时误提交。

## 外壳、端点与命令面板

正式构建的 REST、WebSocket 与订阅地址始终使用当前页面同源。已移除登录页与账户页的“面板地址”入口，也不再读取浏览器历史的 `panel_address`；不会迁移或删除该旧键。仅 `import.meta.env.DEV` 为真时允许 `VITE_API_BASE` 覆盖开发后端地址，该值可能接收登录凭据，只能指向可信开发实例。优先使用前述同源开发代理，生产构建即使设置该变量也不会改变同源地址。

侧栏 `sidebar_state` cookie 只保存展开布尔值，有效期 7 天、路径 `/`、`SameSite=Lax`，不包含身份信息。无历史偏好时，桌面默认展开，平板默认折叠，手机使用导航抽屉。管理员系统状态每 60 秒轮询，页面隐藏时暂停；连续两次失败才显示连接异常。普通用户与强制改密模式不读取系统状态、版本或升级接口。

旧 `/profile`、`/settings` 书签分别转至 `/account?tab=security` 与 `/account?tab=preferences`；`forcePassword=1` 会继续保留，真实改密要求仍来自持久会话。账户默认资料与安全分区。命令面板只在打开时加载有权查询的资源，共用资源缓存；资源深链写入使用数字 ID，生成 `focus=<id>`，路由与列表的 `useUrlState` 读取仍为数字，页面在 DataTable 的 `focusRowId` 边界显式转换为字符串，新建操作使用 `create=1`。列表页接收这些参数并在定位时清空筛选；隧道原 `action=create` 与其他页面参数继续保留。已提供但非法的筛选/定位键被规范为空值，页面读取可选参数时使用 `??` 回退自己的默认值；缺省键不会覆盖页面默认值。

### 系统配置分区

系统配置页使用 `?tab=site|security|backup|r2|advanced` 保存当前分区。站点、节点与安全、高级分区共用未保存修改计数；只有实际值不同的键计入数量，保存后才生效。放弃修改回到当前服务端值，切换分区保留草稿，离开页面前需确认。R2 使用独立备份接口和自己的保存按钮，不计入全局保存栏；切换分区也保留其草稿。

- `ip` 是节点服务器可访问的面板公网地址；未配置时节点安装命令无法生成，可使用当前地址快捷填写。`app_name` 留空仍显示默认 `go-panel`，不迁移旧数据库名称。
- `allow_insecure_node_downloads` 默认关闭并强制 HTTPS；开启会让节点密钥和下载程序通过明文链路传输。部署环境覆盖继续优先，未保存的开关不会提前改变实际策略或告警。
- `github_download_proxy` 默认留空直连；代理能够接触下载程序，只使用可信 HTTPS 服务。`diagnose_internet_probe` 留空沿用 `www.google.com:443`，只影响参考探测。
- 自定义键名仅允许小写字母、数字和下划线，不能与已有键重复；新增项需统一保存。删除通过独立接口立即从服务器移除，可能使引用它的功能回退默认行为；内部凭据、迁移标记和关键站点策略不提供删除入口。
- 恢复只接受本面板导出的 `.db`、`.sqlite` 或 `.sqlite3` 文件，最大 256 MB；后端继续校验 SQLite 内容。输入“恢复”确认后将覆盖当前数据库和节点配置，过程不可取消。成功页仅显示后端真实的恢复时间与恢复前数据库备份名，3 秒后清理会话并要求重新登录。

## 新界面目录与新增页面

`src/features/<资源>/` 保存该资源的页面、API、React Query hooks、纯模型和行为测试；`src/routes/` 只维护路由、权限、URL 参数与标题。共享表单、列表、状态、诊断、确认与反馈来自 `src/components/patterns/`。业务界面遵守根 `DESIGN.md`，只使用语义颜色令牌。

新增页面时先定义类型化 API 和查询键，再实现主状态、空态、加载、错误及真实 Router 行为测试，最后增加路由和按角色可见的导航。可分享筛选在 `validateSearch` 规范化并保留无关键，通过 `useUrlState` 更新。不得复制共享组件，不得在普通用户查询路径调用管理员接口。

## 无障碍与端到端测试配置

`package.json` / `package-lock.json` 的开发依赖 `@playwright/test` 和 `@axe-core/playwright` 提供真实 Chromium 冒烟和 axe 检查，不进入生产 JS。`scripts.test:e2e` 运行真实 Go 后端的登录、强制改密、创建用户和限速、配置保存和放弃、普通用户导航及转发表单、退出和四页两宽截图。axe 对主要页面、配置分区及关键表单执行 serious/critical 零违规断言，不跳过规则。另在真实390px配置和账户布局检查页签宽高至少40px，防止桌面拉伸掩盖中文短标签的触控缺陷。Vitest 同主版本更新到4.1.12；锁文件的 mocker、brace-expansion、undici 采用兼容安全补丁。更新后仍需 Node22 正常 `npm ci` 和全部门禁，不使用强制依赖覆盖或降低审计级别。

```bash
npm ci
npx playwright install chromium
npm run build
cd .. && bash scripts/sync-web-dist.sh && cd vite-frontend
npm run test:e2e
```

`playwright.config.ts` 串行运行，固定中文、上海时区、暗色与减弱动态，只访问回环6401端口。`e2e/start-backend.mjs` 用 `GOTOOLCHAIN=go1.26.10 go run .`、固定本地测试密码及每次随机的 JWT_SECRET 启动临时后端，仅操作 `/tmp/go-panel-U12.db`。已有该路径或夹具标记时立即失败；操作者应确认是自己的临时库后完整保留移开。脚本不会删库或覆盖现有数据。它只给新库设置强制改密夹具。冒烟在该标记存在时读取临时节点密钥，以本机 WebSocket 模拟节点命令回执，再通过真实 API 创建隧道和限速，并断言收到 AddLimiters；测试结束关闭连接，不运行真实代理或转发数据平面，不改正式在线门槛。

截图基线位于 `e2e/smoke.spec.mjs-snapshots/`，由 macOS26 arm64 和锁定的 Playwright Chromium 实际生成。时间使用固定测试时钟，动态 `<time>` 文本明确遮蔽，不增加像素差容忍。`npm run test:e2e -- --update-snapshots` 后必须人工审图。不同系统、字体或浏览器版本需单独审查基线，不能宣称跨平台逐像素一致。`test-results/` 与 `playwright-report/` 是忽略的临时证据，trace 可能含测试凭据。`.github/workflows/ui-smoke.yml` 仅手动触发、只读权限、匹配 macOS26 runner，失败证据保留一天，不改变 PR 门槛或发布流程。

## 性能采集工具

`scripts.test:performance` 读取仓库外 `U12_PERFORMANCE_OUTPUT`，在已启动的生产内嵌回环实例上用官方 Chromium CDP 关闭缓存，设置 Fast3G（延迟150ms、下载1.6Mbps、上传750Kbps）及4倍 CPU 降速，通过真实 PerformanceObserver 记录登录页和控制台 LCP 及外源请求。`U12_ADMIN_PASSWORD` 覆盖临时管理员密码，只用于测试实例；不得上传凭据。存在外源请求时保存完整测量结果并以非0退出。数值属于本机单次测量，不能代表真实用户分位数。

开发模式访问 `/node?__profileNodes=1` 加载 `src/dev/node-profiler.tsx`，对实际节点页面记录 React Profiler commit，结果在 `#node-profiler-samples` 的可访问输出。DEV 常量使整个测量器从生产构建移除；不能把生产版 Profiler 的0ms当作证据。运行 `npm run dev -- --host 127.0.0.1 --port 5201 --strictPort` 后，设置仓库外 `U12_NODE_PERFORMANCE_OUTPUT` 并运行 `node e2e/node-performance.mjs`：仅拦截本地测试 API 和 WebSocket，建立50个内存节点，每2秒发送50条指标事件，记录真实浏览器开发构建的 commit（不含初次加载）。它不连接真实节点，不证明数据面性能；每次 commit 目标小于8ms，轮数不足、未确认50个节点、有效commit不足或最大耗时达到8ms时保存结果并以非0退出，必须报告实际结果或能力边界。

`tsconfig.json` 的 `include` 同时覆盖 `playwright.config.ts`，新增测试配置也必须通过正式 typecheck；生产源码的严格检查范围不会缩小。
