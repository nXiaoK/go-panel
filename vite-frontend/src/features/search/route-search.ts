/**
 * 命令面板与列表页的 URL 契约：导航写 focus 使用数字 ID，避免路由把数字字符串编码成带引号 JSON；
 * 路由与 useUrlState 保持数字；页面在 DataTable 的 focusRowId 边界显式 String 转换。create=1 表示打开新建表单。
 * 保留页面自己的参数（例如隧道 action=create）；列表页负责将 focus 交给 DataTable，
 * 并在定位时清空 q/status 筛选。校验只规范本模块的已知键，不丢弃其他合法书签参数。
 */
export function resourceSearch(search: Record<string, unknown>) {
  const { q, status, sort, focus, create, ...pageSearch } = search;
  const focusId = typeof focus === "string" || typeof focus === "number" ? Number(focus) : NaN;
  return {
    ...pageSearch,
    // TanStack 会合并原始 search：已提供的非法键需显式覆盖为 undefined，防止页面继续读取原始非法值。
    // 未提供的键仍省略，避免覆盖 useUrlState 的页面默认值；非法搜索词回退空串保证字符串筛选安全。
    ...("q" in search
      ? {
          q:
            typeof q === "string" || (typeof q === "number" && Number.isFinite(q)) ? String(q) : "",
        }
      : {}),
    ...("status" in search ? { status: typeof status === "string" ? status : undefined } : {}),
    ...("sort" in search ? { sort: typeof sort === "string" ? sort : undefined } : {}),
    ...("focus" in search
      ? { focus: Number.isSafeInteger(focusId) && focusId > 0 ? focusId : undefined }
      : {}),
    ...("create" in search ? { create: create === 1 || create === "1" ? 1 : undefined } : {}),
  };
}
