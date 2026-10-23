/** cookie 只接受显式布尔值；未保存时平板默认图标栏，桌面默认展开。 */
export function readSidebarDefaultOpen() {
  if (typeof document === "undefined") return true;
  const stored = document.cookie
    .split(";")
    .map((part) => part.trim())
    .find((part) => part.startsWith("sidebar_state="))
    ?.split("=")[1];
  if (stored === "true" || stored === "false") return stored === "true";
  return window.innerWidth >= 1024;
}
