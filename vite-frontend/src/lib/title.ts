import { useLayoutEffect } from "react";
import { getCachedAppName } from "@/lib/site-config";

export function pageTitle(
  matches: ReadonlyArray<{ meta?: ReadonlyArray<{ title?: string } | undefined> }>,
): string {
  for (let i = matches.length - 1; i >= 0; i--) {
    const title = matches[i].meta?.find((meta) => typeof meta?.title === "string")?.title;
    if (title) return title;
  }
  return "转发管理控制台";
}

export function buildDocumentTitle(pageName: string): string {
  return `${pageName} · ${getCachedAppName()}`;
}

/** HeadContent 继续管理 meta；标题写入由这里独占，路由异步提交也不能覆盖自定义品牌。 */
export function useDocumentTitle(pageName: string): void {
  useLayoutEffect(() => {
    const update = () => {
      const next = buildDocumentTitle(pageName);
      if (document.title !== next) document.title = next;
    };
    update();
    window.addEventListener("site-config-updated", update);
    window.addEventListener("storage", update);
    const observer = new MutationObserver(update);
    observer.observe(document.head, { childList: true, subtree: true, characterData: true });
    return () => {
      observer.disconnect();
      window.removeEventListener("site-config-updated", update);
      window.removeEventListener("storage", update);
    };
  }, [pageName]);
}
