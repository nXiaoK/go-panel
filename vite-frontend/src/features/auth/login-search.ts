// 提示原因集中校验，后续改密/恢复流程可以在此扩展，未知来源不展示任意文本。
export const loginReasons = {
  "password-changed": "登录信息已更新，请重新登录",
  expired: "登录已失效，请重新登录",
  disabled: "账号已被禁用，请联系管理员",
} as const;
export function parseLoginSearch(search: Record<string, unknown>): {
  reason?: keyof typeof loginReasons;
} {
  return typeof search.reason === "string" && Object.hasOwn(loginReasons, search.reason)
    ? { reason: search.reason as keyof typeof loginReasons }
    : {};
}
