import { test, expect } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { existsSync } from "node:fs";
import { createDecipheriv, createHash } from "node:crypto";
import AxeBuilder from "@axe-core/playwright";
const initialPassword = "U12-local-initial-only!93";
const adminPassword = "U12-local-changed-only!94";
const userPassword = "U12-local-viewer-only!95";
let mockNode;
const mockCommands = [];
test.afterEach(() => mockNode?.close());

async function connectMockNode(nodeId) {
  // 只读取启动器标记的新建回环测试库；模拟命令回执，不建立任何转发数据链路。
  expect(existsSync("/tmp/go-panel-U12.db.u12-test-fixture")).toBe(true);
  const secret = execFileSync(
    "python3",
    [
      "-c",
      "import sqlite3,sys; c=sqlite3.connect('/tmp/go-panel-U12.db'); print(c.execute('SELECT secret FROM node WHERE id=?',(int(sys.argv[1]),)).fetchone()[0])",
      String(nodeId),
    ],
    { encoding: "utf8" },
  ).trim();
  mockNode = new WebSocket(
    `ws://127.0.0.1:6401/system-info?type=1&secret=${encodeURIComponent(secret)}&version=u12-mock`,
  );
  mockNode.addEventListener("message", ({ data }) => {
    let command = JSON.parse(String(data));
    if (command.encrypted) {
      const raw = Buffer.from(command.data, "base64");
      const decipher = createDecipheriv(
        "aes-256-gcm",
        createHash("sha256").update(secret).digest(),
        raw.subarray(0, 12),
      );
      decipher.setAuthTag(raw.subarray(-16));
      command = JSON.parse(
        Buffer.concat([decipher.update(raw.subarray(12, -16)), decipher.final()]).toString(),
      );
    }
    if (!command.requestId) return;
    mockCommands.push(command);
    mockNode.send(
      JSON.stringify({ requestId: command.requestId, type: command.type, message: "OK", data: {} }),
    );
  });
  await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error("模拟节点回环连接超时")), 5_000);
    mockNode.addEventListener(
      "open",
      () => {
        clearTimeout(timer);
        resolve();
      },
      { once: true },
    );
    mockNode.addEventListener(
      "error",
      (event) => {
        clearTimeout(timer);
        reject(event);
      },
      { once: true },
    );
  });
}

async function login(page, username, password) {
  await page.goto("/login");
  await page.getByLabel("用户名", { exact: true }).fill(username);
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
}
async function axe(page, name) {
  // axe 不检查所有触控尺寸；真实布局测量防止中文短标签小于设计的40px下限。
  for (const tab of await page.getByRole("tab").all()) {
    const box = await tab.boundingBox();
    expect(box, `${name} 的页签应有可见布局`).not.toBeNull();
    expect(box.width, `${name} 页签宽度`).toBeGreaterThanOrEqual(40);
    expect(box.height, `${name} 页签高度`).toBeGreaterThanOrEqual(40);
  }
  const result = await new AxeBuilder({ page }).analyze();
  const violations = result.violations.filter((item) =>
    ["serious", "critical"].includes(item.impact),
  );
  expect(violations, `${name}: ${JSON.stringify(violations)}`).toEqual([]);
}
async function api(page, path, data) {
  const token = await page.evaluate(() => localStorage.getItem("token"));
  const response = await page.request.post(`/api/v1${path}`, {
    data,
    headers: { Authorization: token },
  });
  expect(response.ok()).toBeTruthy();
  const body = await response.json();
  expect(body.code, `${path}: ${body.msg}`).toBe(0);
  return body.data;
}
async function settled(page, title) {
  await expect(page.getByRole("heading", { name: title, exact: true, level: 1 })).toBeVisible();
  await expect(page.locator('[aria-busy="true"]')).toHaveCount(0);
  await page.evaluate(() => document.fonts.ready);
  await expect(page.locator("body")).not.toContainText("加载中");
}

test("真实临时后端：强制改密、空态截图、各页 axe、写入与普通用户边界", async ({ page }) => {
  await expect.poll(() => existsSync("/tmp/go-panel-U12.db.u12-test-fixture")).toBe(true);
  await page.clock.setFixedTime(new Date("2026-10-23T00:00:00+08:00"));
  await page.goto("/login");
  await axe(page, "登录");
  await login(page, "admin_user", initialPassword);
  await expect(page).toHaveURL(/forcePassword=1/);
  await axe(page, "强制改密");
  await page.getByLabel(/^原密码/).fill(initialPassword);
  await page.getByLabel(/^新密码/).fill(adminPassword);
  await page.getByLabel(/^确认新密码/).fill(adminPassword);
  await page.getByRole("button", { name: "更新登录信息" }).click();
  await expect(page).toHaveURL(/login/);
  await login(page, "admin_user", adminPassword);
  await expect(page).toHaveURL(/\/$/);

  for (const [path, title, filename] of [
    ["/", "控制台", "dashboard"],
    ["/forward", "转发规则", "forwards"],
    ["/node", "节点", "nodes"],
    ["/user", "用户", "users"],
  ]) {
    await page.goto(path);
    await settled(page, title);
    await axe(page, `管理员${title}`);
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: 900 });
      await expect(page).toHaveScreenshot(`${filename}-${width}.png`, {
        animations: "disabled",
        mask: [page.locator("time")],
      });
    }
    await page.setViewportSize({ width: 1440, height: 900 });
  }
  for (const [path, title] of [
    ["/tunnel", "隧道"],
    ["/limit", "限速策略"],
    ["/subscription", "订阅"],
    ["/config", "系统配置"],
    ["/account", "账户"],
  ]) {
    await page.goto(path);
    await settled(page, title);
    await axe(page, `管理员${title}`);
  }
  for (const tab of ["site", "security", "backup", "r2", "advanced"]) {
    await page.goto(`/config?tab=${tab}`);
    await settled(page, "系统配置");
    await axe(page, `系统配置${tab}`);
  }
  // 在实际窄屏布局复验短标签，桌面配置页的纵向拉伸会掩盖36px宽度回归。
  await page.setViewportSize({ width: 390, height: 900 });
  await page.goto("/config");
  await settled(page, "系统配置");
  await axe(page, "移动端系统配置页签");
  await page.goto("/account?tab=preferences");
  await settled(page, "账户");
  await axe(page, "移动端账户页签");
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/config");
  const appName = page.getByLabel("应用名称", { exact: true });
  await appName.fill("U12 草稿站点");
  await page.getByRole("button", { name: "放弃修改", exact: true }).click();
  await expect(appName).toHaveValue("go-panel");
  await appName.fill("U12 测试站点");
  await page.getByRole("button", { name: "保存配置" }).click();
  await expect(page.getByRole("region", { name: "未保存的系统配置" })).toHaveCount(0);
  await page.reload();
  await expect(appName).toHaveValue("U12 测试站点");

  await page.goto("/user");
  await page.getByRole("button", { name: "新增用户", exact: true }).first().click();
  const userSheet = page.getByRole("dialog");
  await userSheet.getByLabel("用户名", { exact: false }).fill("u12_viewer");
  await userSheet.getByLabel("初始密码", { exact: false }).fill(userPassword);
  await userSheet.getByLabel(/^到期日期/).fill("2035-12-31");
  await axe(page, "新建用户抽屉");
  await userSheet.getByRole("button", { name: "创建用户", exact: true }).click();
  await expect(
    userSheet.getByRole("heading", { name: "u12_viewer (@u12_viewer)", exact: true }),
  ).toBeVisible();
  await expect(userSheet.getByRole("tab", { name: "隧道权限 0", exact: true })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  await axe(page, "创建后隧道权限");
  await userSheet.getByRole("button", { name: "关闭", exact: true }).last().click();
  await expect(userSheet).toHaveCount(0);
  await expect(page.getByText("@u12_viewer")).toBeVisible();

  // 本地模拟控制面只回执命令，验证真实限速 API，不运行真实节点或数据平面。
  await api(page, "/node/create", {
    name: "U12 模拟夹具",
    ip: "127.0.0.1",
    serverIp: "127.0.0.1",
    portSta: 20000,
    portEnd: 21000,
    forwardMode: "gost",
  });
  const [node] = await api(page, "/node/list", {});
  await connectMockNode(node.id);
  await expect.poll(async () => (await api(page, "/node/list", {}))[0].status).toBe(1);
  await api(page, "/tunnel/create", {
    name: "U12 本地隧道",
    type: 1,
    inNodeId: node.id,
    flow: 1,
    tcpListenAddr: "127.0.0.1",
    udpListenAddr: "127.0.0.1",
  });
  await page.goto("/limit");
  await page.getByRole("button", { name: "新建策略", exact: true }).first().click();
  await page.getByLabel(/^策略名称/).fill("U12 测试限速");
  await page.getByRole("combobox", { name: /^关联隧道/ }).click();
  await page.getByRole("option", { name: "U12 本地隧道" }).click();
  await page.getByLabel(/^限速速率/).fill("25");
  await axe(page, "新建限速弹窗");
  await page.getByRole("dialog").getByRole("button", { name: "保存策略" }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.getByText("U12 测试限速")).toBeVisible();
  expect(mockCommands.some((command) => command.type === "AddLimiters")).toBe(true);
  await page.reload();
  await expect(page.getByText("U12 测试限速")).toBeVisible();

  await page.goto("/account");
  await page.getByRole("button", { name: "退出登录", exact: true }).click();
  await expect(page).toHaveURL(/login/);
  await login(page, "u12_viewer", userPassword);
  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByRole("link", { name: "用户", exact: true })).toHaveCount(0);
  await expect(page.getByRole("link", { name: "系统配置", exact: true })).toHaveCount(0);
  for (const [path, title] of [
    ["/", "控制台"],
    ["/forward", "转发规则"],
    ["/account", "账户"],
  ]) {
    await page.goto(path);
    await settled(page, title);
    await axe(page, `普通用户${title}`);
  }
  const userTunnels = page.waitForResponse(
    (response) =>
      response.url().includes("/tunnel/user/tunnel") && response.request().method() === "POST",
  );
  await page.goto("/forward");
  await userTunnels;
  await page.getByRole("button", { name: "新建转发", exact: true }).first().click();
  await expect(page.getByRole("dialog")).toBeVisible();
  await expect(page.getByRole("dialog").getByRole("combobox", { name: /隧道/ })).toBeVisible();
  await axe(page, "普通用户新建转发");
  await page.getByRole("dialog").getByRole("button", { name: "取消", exact: true }).click();
  await page.goto("/account?tab=preferences");
  await axe(page, "普通用户偏好");
  await page.goto("/account");
  await page.getByRole("button", { name: "退出登录", exact: true }).click();
  await expect(page).toHaveURL(/login/);
});
