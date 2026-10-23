import { chromium } from "@playwright/test";
import { writeFile } from "node:fs/promises";
const output = process.env.U12_NODE_PERFORMANCE_OUTPUT;
if (!output) throw new Error("请设置 U12_NODE_PERFORMANCE_OUTPUT 为外部结果 JSON 路径");
const browser = await chromium.launch();
const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
const page = await context.newPage();
const nodes = Array.from({ length: 50 }, (_, index) => ({
  id: index + 1,
  name: `性能节点 ${index + 1}`,
  ip: "127.0.0.1",
  serverIp: "127.0.0.1",
  status: 1,
  forwardMode: "gost",
  version: "1.0",
  syncState: "synced",
}));
await page.addInitScript(() => {
  localStorage.setItem("token", "local-profile-fixture");
  localStorage.setItem("role_id", "0");
  localStorage.setItem("must_change_pwd", "0");
  localStorage.setItem("pref_auto_refresh", "0");
});
await page.route("**/api/v1/**", async (route) => {
  const path = new URL(route.request().url()).pathname;
  const build = { version: "dev", commit: "unknown", buildTime: "", sourceUrl: "" };
  const data = path.endsWith("/system/version")
    ? build
    : path.endsWith("/system/update/check")
      ? {
          current: build,
          enabled: false,
          updateAvailable: false,
          releaseName: "",
          releaseUrl: "",
          releaseNotes: "",
          publishedAt: "",
          autoUpdateConfigured: false,
        }
      : path.endsWith("/node/list")
        ? nodes
        : path.endsWith("/config/list")
          ? { app_name: "go-panel" }
          : path.endsWith("/config/get")
            ? { value: "go-panel" }
            : path.endsWith("/system/status")
              ? { status: "normal" }
              : [];
  await route.fulfill({ json: { code: 0, msg: "成功", data } });
});
let rounds = 0;
const timers = new Set();
await page.routeWebSocket("**/system-info**", (socket) => {
  const push = () => {
    rounds++;
    for (const node of nodes)
      socket.send(
        JSON.stringify({
          id: String(node.id),
          type: "info",
          data: {
            cpu_usage: (rounds + node.id) % 100,
            memory_usage: 40,
            bytes_transmitted: rounds * 1000,
            bytes_received: rounds * 2000,
            uptime: rounds * 2,
          },
        }),
      );
  };
  const timer = setInterval(push, 2000);
  timers.add(timer);
  socket.onClose(() => {
    clearInterval(timer);
    timers.delete(timer);
  });
});
await page.goto("http://127.0.0.1:5201/node?__profileNodes=1");
await page.getByRole("heading", { name: "节点", exact: true, level: 1 }).waitFor();
await page.getByText("性能节点 50", { exact: true }).waitFor();
await page.locator('[data-node-id="50"] [role="meter"]').first().waitFor();
const ready = await page.evaluate(() => performance.now());
await page.waitForTimeout(22_000);
const samples = await page.locator("#node-profiler-samples").textContent();
const allSamples = JSON.parse(samples || "[]");
const commits = allSamples.filter(
  (item) => item.phase !== "mount" && item.actualDuration > 0 && item.commitTime >= ready,
);
const max = commits.length ? Math.max(...commits.map((item) => item.actualDuration)) : null;
const renderedNodes = await page.locator("[data-node-id]").count();
const lastNodeCpu = await page
  .locator('[data-node-id="50"] [role="meter"]')
  .first()
  .getAttribute("aria-valuenow");
const observedCpu = await page
  .locator('[data-node-id] [role="meter"][aria-label$=" CPU"]')
  .evaluateAll((elements) =>
    elements.map((element) => ({
      id: Number(element.closest("[data-node-id]")?.getAttribute("data-node-id")),
      value: Number(element.getAttribute("aria-valuenow")),
    })),
  );
const receivedAllNodes =
  renderedNodes === 50 &&
  observedCpu.length === 50 &&
  new Set(observedCpu.map((item) => item.id)).size === 50 &&
  observedCpu.every(
    (item) => item.id >= 1 && item.id <= 50 && item.value === (rounds + item.id) % 100,
  );
const passesTarget =
  rounds >= 10 && receivedAllNodes && commits.length >= 10 && max !== null && max < 8;
await writeFile(
  output,
  JSON.stringify(
    {
      browser: browser.version(),
      mode: "React development Profiler around actual NodePage; 50 mocked REST nodes + real browser WebSocket events every 2s",
      rounds,
      readyMs: ready,
      totalSamples: allSamples.length,
      firstSample: allSamples[0] ?? null,
      lastSample: allSamples.at(-1) ?? null,
      renderedNodes,
      receivedAllNodes,
      observedCpu,
      lastNodeCpu,
      commits,
      maxCommitMs: max,
      targetMs: 8,
      passesTarget,
    },
    null,
    2,
  ),
);
// Playwright 关闭浏览器不会保证 Node 端模拟定时器触发 onClose，必须主动释放。
for (const timer of timers) clearInterval(timer);
await browser.close();
if (!passesTarget) throw new Error("节点性能未达到完整样本或8ms目标，失败证据已保存");
