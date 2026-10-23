import { spawn, spawnSync } from "node:child_process";
import { existsSync, writeFileSync } from "node:fs";
import { randomBytes } from "node:crypto";
import { fileURLToPath } from "node:url";
const root = fileURLToPath(new URL("../../", import.meta.url));
// 只使用专用临时库，已有库必须由操作者先备份/移开；不自动删库或恢复生产数据。
const database = "/tmp/go-panel-U12.db";
if (existsSync(database) || existsSync(`${database}.u12-test-fixture`))
  throw new Error(`${database} 或夹具标记已存在，请先保留并移开该临时测试库`);
// 固定值仅为本机测试账号；JWT 每次随机，仅监听回环，不能用于正式部署。
const child = spawn("go", ["run", "."], {
  cwd: root,
  stdio: "inherit",
  env: {
    ...process.env,
    GOTOOLCHAIN: "go1.26.10",
    LISTEN_ADDR: "127.0.0.1:6401",
    DB_PATH: database,
    ADMIN_PASSWORD: "U12-local-initial-only!93",
    JWT_SECRET: randomBytes(48).toString("hex"),
  },
});
for (const signal of ["SIGTERM", "SIGINT"]) process.on(signal, () => child.kill(signal));
child.on("exit", (code) => process.exit(code ?? 1));
// 专用新库初始化后仅设置测试管理员的强制改密标记，以覆盖真实会话守卫。
let initialized = false;
for (let attempt = 0; attempt < 300; attempt++) {
  const result = spawnSync("python3", [
    "-c",
    "import sqlite3,sys; c=sqlite3.connect(sys.argv[1]); r=c.execute(\"UPDATE user SET must_change_pwd=1 WHERE user='admin_user'\"); c.commit(); sys.exit(0 if r.rowcount==1 else 1)",
    database,
  ]);
  if (result.status === 0) {
    writeFileSync(`${database}.u12-test-fixture`, "U12 disposable local fixture\n", {
      mode: 0o600,
    });
    initialized = true;
    break;
  }
  await new Promise((resolve) => setTimeout(resolve, 200));
}

if (!initialized) {
  child.kill("SIGTERM");
  throw new Error("U12 临时管理员初始化超时");
}
