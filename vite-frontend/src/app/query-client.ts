import { QueryClient } from "@tanstack/react-query";

export const createAppQueryClient = () =>
  new QueryClient({
    defaultOptions: {
      queries: {
        // 30 秒内的页面切换直接命中缓存，避免列表页来回抖动；
        // 各页面另有手动刷新按钮兜底。
        staleTime: 30_000,
        // 请求失败只重试一次；持续失败交由页面错误区与手动重试处理。
        retry: 1,
        // 窗口聚焦不额外重取；通过手动刷新、业务失效或页面轮询更新数据。
        refetchOnWindowFocus: false,
      },
    },
  });
