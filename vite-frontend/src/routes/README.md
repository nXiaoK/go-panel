# 路由与功能模块

本项目使用 TanStack Router 的浏览器文件路由。`routeTree.gen.ts` 由构建插件生成，不手动编辑。

- `src/routes/*.tsx` 仅定义路径、搜索参数、页面元信息与页面组件引用；页面测试随资源放置，避免被路由生成器扫描。
- `src/app/` 放应用外壳、根 providers 与根元信息；根组件保留 `HeadContent`、会话/面板地址切换清缓存及统一标题管理。
- `src/features/<资源>/` 放 API、查询 hooks、纯函数与页面/子组件；`src/lib/api/client.ts` 只负责统一传输，`src/lib/api/index.ts` 保留公开 API 导出兼容层。
- 每个资源导出查询键工厂。节点原始列表和含 WebSocket 运行态列表使用不同子键。控制台和搜索直接复用实体查询，只有流量查询键随时间范围及隧道变化。
- `src/lib/query-keys.ts` 的失效矩阵是变更成功后的唯一失效来源；页面手动刷新可按资源查询键执行。搜索不再拥有独立缓存。
- `role_id === 0` 表示管理员。会话、强制改密与角色路由守卫位于 `features/auth/guard.ts`；实体查询 hooks 同样禁止无会话/强制改密请求管理员资源。后端权限校验始终有效。
- 系统配置保存 `app_name` 后立即更新本地品牌事件，统一标题管理在 `HeadContent` 提交后继续保持“页面 · 品牌”。
