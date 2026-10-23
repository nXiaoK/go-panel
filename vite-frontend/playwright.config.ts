import { defineConfig } from "@playwright/test";

export default defineConfig({
  // 冒烟只操作回环临时实例；串行执行避免同一 SQLite 夹具被并行修改。
  testDir: "./e2e",
  testMatch: "smoke.spec.mjs",
  workers: 1,
  // 不自动重试掩盖不稳定交互，截图基线必须由人工审查后更新。
  retries: 0,
  timeout: 120_000,
  expect: { timeout: 10_000 },
  use: {
    baseURL: "http://127.0.0.1:6401",
    // 本地关键动作超过15秒即失败，不用长等待掩盖表单或路由问题。
    actionTimeout: 15_000,
    browserName: "chromium",
    viewport: { width: 1440, height: 900 },
    locale: "zh-CN",
    timezoneId: "Asia/Shanghai",
    colorScheme: "dark",
    reducedMotion: "reduce",
    // 失败保留浏览器记录；其中可能含测试凭据，仅供受控 CI artifact 下载。
    trace: "retain-on-failure",
  },
  webServer: {
    // 先独立构建并同步 web/dist；启动器拒绝覆盖已有数据库，避免触及真实数据。
    command: "node e2e/start-backend.mjs",
    url: "http://127.0.0.1:6401/login",
    reuseExistingServer: false,
    timeout: 120_000,
  },
});
