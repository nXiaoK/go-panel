// 首屏在样式和应用加载前恢复外观；仅允许 dark/light/system，旧值或不可用存储默认深色。
// 此脚本由 Vite 作为同源带内容哈希的独立资源发布，不需要放宽 CSP 的内联脚本限制。
(() => {
  let preference = "dark";
  try {
    const saved = localStorage.getItem("pref_theme");
    if (["dark", "light", "system"].includes(saved)) preference = saved;
  } catch {
    // 隐私模式禁用存储时仍能显示页面，后续切换只对本次页面有效。
  }
  const theme =
    preference === "system"
      ? window.matchMedia("(prefers-color-scheme: dark)").matches
        ? "dark"
        : "light"
      : preference;
  document.documentElement.classList.toggle("dark", theme === "dark");
  document.documentElement.classList.toggle("light", theme === "light");
  document.documentElement.style.colorScheme = theme;
})();
