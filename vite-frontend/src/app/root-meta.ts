export const rootHead = () => ({
  meta: [
    { charSet: "utf-8" },
    { name: "viewport", content: "width=device-width, initial-scale=1" },
    { title: "转发管理控制台" },
    {
      name: "description",
      content: "管理控制台：隧道 · 转发 · 节点 · 用户 · 订阅一站式管理。",
    },
    { property: "og:title", content: "转发管理控制台" },
    {
      property: "og:description",
      content: "管理控制台：隧道 · 转发 · 节点 · 用户 · 订阅一站式管理。",
    },
    { property: "og:type", content: "website" },
    { name: "twitter:card", content: "summary_large_image" },
  ],
  links: [{ rel: "icon", href: "/favicon.ico", type: "image/x-icon" }],
});
