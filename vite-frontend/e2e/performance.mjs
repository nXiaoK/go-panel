import { chromium } from "@playwright/test";
import { writeFile } from "node:fs/promises";
// 仅向明确的回环测试实例发送已有临时凭据，禁止对公网运行性能夹具。
const origin = "http://127.0.0.1:6401";
const output = process.env.U12_PERFORMANCE_OUTPUT;
if (!output) throw new Error("请设置 U12_PERFORMANCE_OUTPUT 为外部结果 JSON 路径");
const browser = await chromium.launch();
const context = await browser.newContext({
  viewport: { width: 1440, height: 900 },
  locale: "zh-CN",
});
const page = await context.newPage();
const session = await context.newCDPSession(page);
await session.send("Network.enable");
// Chrome DevTools Fast 3G：150ms 延迟，下载1.6Mbps，上传750Kbps；CPU为4倍降速。
await session.send("Network.emulateNetworkConditions", {
  offline: false,
  latency: 150,
  downloadThroughput: 1_600_000 / 8,
  uploadThroughput: 750_000 / 8,
  connectionType: "cellular3g",
});
await session.send("Emulation.setCPUThrottlingRate", { rate: 4 });
await session.send("Network.setCacheDisabled", { cacheDisabled: true });
await page.addInitScript(() => {
  window.__u12Lcp = [];
  new PerformanceObserver((list) => {
    window.__u12Lcp.push(
      ...list.getEntries().map((entry) => ({
        startTime: entry.startTime,
        size: entry.size,
        element: entry.element?.tagName,
      })),
    );
  }).observe({ type: "largest-contentful-paint", buffered: true });
});
const measurements = [];
for (const [name, path] of [
  ["login", "/login"],
  ["dashboard", "/"],
]) {
  if (name === "dashboard") {
    const response = await context.request.post(`${origin}/api/v1/user/login`, {
      data: {
        username: "admin_user",
        password: process.env.U12_ADMIN_PASSWORD || "U12-local-changed-only!94",
      },
    });
    const body = await response.json();
    if (body.code !== 0 || !body.data?.token) throw new Error("临时管理员登录失败");
    await page.addInitScript(({ token, role_id, name }) => {
      localStorage.setItem("token", token);
      localStorage.setItem("role_id", String(role_id));
      localStorage.setItem("name", name);
      localStorage.setItem("must_change_pwd", "0");
    }, body.data);
  }
  const external = [];
  const listener = (request) => {
    if (new URL(request.url()).origin !== origin) external.push(request.url());
  };
  page.on("request", listener);
  await page.goto(`${origin}${path}`, { waitUntil: "networkidle" });
  await page
    .getByRole("heading", {
      name: name === "login" ? "登录控制台" : "控制台",
      level: 1,
      exact: true,
    })
    .waitFor();
  if (new URL(page.url()).pathname !== path) throw new Error("性能页面发生非预期跳转");
  await page.waitForTimeout(3000);
  const lcp = await page.evaluate(() => window.__u12Lcp.at(-1) ?? null);
  if (!lcp || lcp.startTime <= 0) throw new Error("未获得有效LCP，不能记为测量成功");
  measurements.push({
    name,
    url: page.url(),
    lcp,
    externalRequests: external,
  });
  page.off("request", listener);
}
await writeFile(
  output,
  JSON.stringify(
    {
      browser: browser.version(),
      method: "Chromium CDP Fast 3G + CPU4x, cold cache, production Go embed",
      network: { latencyMs: 150, downloadBitsPerSecond: 1_600_000, uploadBitsPerSecond: 750_000 },
      cpuRate: 4,
      measurements,
    },
    null,
    2,
  ),
);
await browser.close();
if (measurements.some((item) => item.externalRequests.length > 0))
  throw new Error("生产页面出现外源请求，完整请求证据已保存");
