import { chromium, expect } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { mkdir, writeFile } from "node:fs/promises";

// 仅访问本机 5205 的生产预览；所有业务接口与 WebSocket 均使用内存夹具，不连接真实面板。
// 先执行 npm run build 与 npm run preview -- --host 127.0.0.1 --port 5205 --strictPort。
// 证据写入已忽略的 test-results/theme-search，不修改截图基线。
const output = "test-results/theme-search";
await mkdir(output, { recursive: true });
const browser = await chromium.launch();
const context = await browser.newContext({
  viewport: { width: 1440, height: 1000 },
  colorScheme: "light",
  reducedMotion: "reduce",
});
const page = await context.newPage();
const errors = [];
page.on("pageerror", (error) => errors.push(error.message));
const nodes = ["上海入口", "香港入口", "东京出口"].map((name, index) => ({
  id: index + 1,
  name,
  serverIp: `192.0.2.${index + 1}`,
  ip: `192.0.2.${index + 1}`,
  status: 1,
  forwardMode: "nftables",
}));
const tunnels = ["香港端口转发", "上海 → 香港", "上海 → 东京", "上海端口转发"].map(
  (name, index) => ({
    id: index + 1,
    name,
    type: (index % 2) + 1,
    protocol: "tcp+udp",
    inNodeId: 1,
    outNodeId: 2,
    status: 1,
  }),
);
const forwards = Array.from({ length: 18 }, (_, index) => ({
  id: index + 1,
  name: index === 0 ? "转发面板" : `订阅节点 · 东京 ${index}`,
  tunnelId: 2,
  tunnelName: tunnels[1].name,
  inPort: 20000 + index,
  remoteAddr: `198.51.100.10:${443 + index}`,
  status: index === 4 ? -1 : 1,
  userName: "xiaok",
  inFlow: 1024 ** 3,
  outFlow: 1024 ** 2,
  inx: index,
}));
await context.addInitScript(() => {
  localStorage.setItem("token", "local-ui-fixture");
  localStorage.setItem("role_id", "0");
  localStorage.setItem("name", "xiaok");
  localStorage.setItem("must_change_pwd", "0");
  localStorage.setItem("pref_auto_refresh", "0");
});
await context.route("**/api/v1/**", async (route) => {
  const path = new URL(route.request().url()).pathname;
  if (path.endsWith("/user/package")) {
    const totals = { inFlow: 20 * 1024 ** 3, outFlow: 10 * 1024 ** 3, totalFlow: 30 * 1024 ** 3 };
    await route.fulfill({
      json: {
        code: 0,
        data: {
          userInfo: {
            id: 1,
            user: "xiaok",
            flow: 100,
            num: 100,
            inFlow: totals.inFlow,
            outFlow: totals.outFlow,
          },
          forwards,
          tunnelPermissions: [],
          trafficTunnels: [],
          trafficRange: "24h",
          trafficScope: "system",
          trafficTotals: totals,
          todayTraffic: totals,
          trafficTrendTotals: totals,
          statisticsFlows: Array.from({ length: 24 }, (_, index) => ({
            time: `${23 - index}:00`,
            inFlow: ((index % 5) + 1) * 1024 ** 3,
            outFlow: ((index % 3) + 1) * 1024 ** 3,
          })),
        },
      },
    });
    return;
  }
  const build = { version: "dev", commit: "local", buildTime: "", sourceUrl: "" };
  const data = path.endsWith("/system/version")
    ? build
    : path.endsWith("/system/update/check")
      ? { current: build, enabled: false, updateAvailable: false }
      : path.endsWith("/node/list")
        ? nodes
        : path.endsWith("/tunnel/list") || path.endsWith("/tunnel/user/tunnel")
          ? tunnels
          : path.endsWith("/forward/list")
            ? forwards
            : path.endsWith("/user/list")
              ? [{ id: 1, user: "xiaok", name: "xiaok", status: 1 }]
              : path.endsWith("/config/list")
                ? { app_name: "go-panel" }
                : path.endsWith("/config/get")
                  ? { value: "go-panel" }
                  : path.endsWith("/system/status")
                    ? { status: "normal" }
                    : [];
  await route.fulfill({ json: { code: 0, msg: "成功", data } });
});
await context.routeWebSocket("**/system-info**", () => {});
async function checkAxe(name) {
  const results = await new AxeBuilder({ page }).analyze();
  const violations = results.violations.filter((item) =>
    ["critical", "serious"].includes(item.impact),
  );
  await writeFile(`${output}/${name}-axe.json`, JSON.stringify(violations, null, 2));
  expect(violations, name).toEqual([]);
}
async function setTheme(label) {
  await page.getByRole("button", { name: /^切换主题/ }).click();
  await page.getByRole("menuitemradio", { name: label, exact: true }).click();
}
try {
  await page.goto("http://127.0.0.1:5205/forward");
  await expect(page.getByRole("heading", { name: "转发规则", level: 1 })).toBeVisible();
  await expect(page.locator("html")).toHaveClass(/dark/);
  for (const [theme, label] of [
    ["dark", "深色"],
    ["light", "浅色"],
  ]) {
    await setTheme(label);
    await expect(page.locator("html")).toHaveClass(new RegExp(theme));
    await checkAxe(`${theme}-page`);
    await page.screenshot({ path: `${output}/${theme}-page.png` });
    await page.getByRole("button", { name: "搜索资源 ⌘K" }).click();
    await expect(page.getByRole("dialog")).toBeVisible();
    const search = page.getByRole("combobox", { name: "搜索资源" });
    await expect(search).toBeFocused();
    await expect(page.getByRole("option", { name: /上海入口/ })).toBeVisible();
    await checkAxe(`${theme}-search`);
    await page.screenshot({ path: `${output}/${theme}-search.png` });
    await page.getByRole("button", { name: "转发", exact: true }).click();
    await expect(page.getByRole("option")).toHaveCount(18);
    await search.fill("不存在的资源");
    await expect(page.getByText("没有匹配的资源、页面或操作")).toBeVisible();
    await page.getByRole("button", { name: "清除筛选" }).click();
    await search.fill("198.51.100.10:443");
    await expect(page.getByRole("option")).toHaveCount(1);
    await search.press("ArrowDown");
    await search.press("Enter");
    await expect(page).toHaveURL(/focus=1/);
    await expect(page.getByRole("dialog")).toHaveCount(0);
  }
  await page.reload();
  await expect(page.locator("html")).toHaveClass(/light/);
  const second = await context.newPage();
  await second.goto("http://127.0.0.1:5205/forward");
  await second.evaluate(() => localStorage.setItem("pref_theme", "dark"));
  await expect(page.locator("html")).toHaveClass(/dark/);
  await second.close();
  await setTheme("跟随系统");
  await expect(page.locator("html")).toHaveClass(/light/);
  await page.emulateMedia({ colorScheme: "dark" });
  await expect(page.locator("html")).toHaveClass(/dark/);
  for (const width of [390, 320]) {
    await page.setViewportSize({ width, height: 844 });
    for (const [theme, label] of [
      ["light", "浅色"],
      ["dark", "深色"],
    ]) {
      await setTheme(label);
      await page.getByRole("button", { name: "搜索资源 ⌘K" }).click();
      await expect(page.getByRole("dialog")).toBeVisible();
      await page.getByRole("button", { name: "转发", exact: true }).click();
      const fits = await page
        .getByRole("dialog")
        .evaluate((dialog) => dialog.scrollWidth <= dialog.clientWidth);
      expect(fits).toBe(true);
      await checkAxe(`${theme}-mobile-${width}`);
      await page.screenshot({ path: `${output}/${theme}-mobile-${width}.png` });
      await page.getByRole("button", { name: "关闭对话框" }).click();
    }
  }
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto("http://127.0.0.1:5205/");
  await expect(page.locator('svg path[stroke="var(--chart-series-1)"]')).toBeVisible();
  for (const [theme, label] of [
    ["light", "浅色"],
    ["dark", "深色"],
  ]) {
    await setTheme(label);
    await checkAxe(`${theme}-dashboard`);
    await page.screenshot({ path: `${output}/${theme}-dashboard.png` });
  }
  await page.getByRole("button", { name: /用户菜单/ }).click();
  await page.getByRole("menuitem", { name: "退出登录" }).click();
  await expect(page.getByRole("heading", { name: "登录控制台" })).toBeVisible();
  await expect(page.locator("html")).toHaveClass(/dark/);
  await setTheme("浅色");
  await expect(page.locator("html")).toHaveClass(/light/);
  await checkAxe("light-login");
  await page.screenshot({ path: `${output}/light-login.png` });
  expect(errors).toEqual([]);
  console.log(
    "通过：深浅主题、持久化、系统与跨页同步、搜索分类/清空/键盘跳转、320/390px 布局及 axe 检查",
  );
} finally {
  await browser.close();
}
