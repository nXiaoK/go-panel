import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  Link,
  RouterProvider,
} from "@tanstack/react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ConfirmProvider } from "@/components/patterns/confirm-dialog";
import { apiDownload, apiPost, apiUpload } from "@/lib/api/client";
import * as session from "@/lib/session";
import { notify } from "@/lib/notify";
import { loginReasons, parseLoginSearch } from "@/features/auth/login-search";
import { ConfigPage } from "./page";
import {
  configSearch,
  customConfigNameError,
  isProtectedConfigName,
  maxRestoreBytes,
  restoreFileError,
} from "./model";
const post = vi.mocked(apiPost),
  upload = vi.mocked(apiUpload),
  download = vi.mocked(apiDownload);
vi.mock("@/lib/api/client", () => ({
  apiPost: vi.fn(),
  apiUpload: vi.fn(),
  apiDownload: vi.fn(),
  errorMessage: (error: unknown) => (error instanceof Error ? error.message : "请求失败"),
}));
let client: QueryClient;
let stored: Record<string, string>;
const r2 = {
  enabled: false,
  accountId: "",
  accessKeyId: "",
  bucket: "",
  objectPrefix: "flux-panel/backups",
  scheduleTime: "03:00",
  retentionCount: 30,
  secretConfigured: false,
  secretUsable: false,
  credentialEncryptionAvailable: true,
  lastAttemptAt: 0,
  lastSuccessAt: 0,
  lastSize: 0,
};
beforeEach(() => {
  localStorage.clear();
  session.setSession({ token: "admin", roleId: 0, name: "管理员", mustChangePassword: false });
  client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity }, mutations: { retry: false } },
  });
  stored = {
    ip: "panel.example.com:6365",
    app_name: "go-panel",
    github_download_proxy: "",
    allow_insecure_node_downloads: "false",
    diagnose_internet_probe: "",
    custom_key: "original",
  };
  post.mockImplementation(async (path, body) => {
    if (path === "/config/list") return { ...stored };
    if (path === "/config/update") {
      Object.assign(stored, body);
      return "ok";
    }
    if (path === "/config/delete") {
      delete stored[(body as { name: string }).name];
      return "ok";
    }
    if (path === "/backup/r2/settings") return r2;
    if (path === "/backup/r2/update") return { ...r2, ...(body as object) };
    return "ok";
  });
  vi.spyOn(window, "scrollTo").mockImplementation(() => {});
  vi.stubGlobal("matchMedia", () => ({
    matches: false,
    addEventListener() {},
    removeEventListener() {},
  }));
  vi.stubGlobal(
    "ResizeObserver",
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  );
});
afterEach(() => {
  cleanup();
  session.clearSession();
  client.clear();
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});
async function mount(url = "/config?tab=site") {
  const root = createRootRoute({
    component: () => (
      <QueryClientProvider client={client}>
        <ConfirmProvider>
          <Link to="/elsewhere">离开系统配置</Link>
          <Outlet />
        </ConfirmProvider>
      </QueryClientProvider>
    ),
  });
  const config = createRoute({
    getParentRoute: () => root,
    path: "/config",
    validateSearch: configSearch,
    component: ConfigPage,
  });
  const other = createRoute({
    getParentRoute: () => root,
    path: "/elsewhere",
    component: () => <p>其他页面</p>,
  });
  const login = createRoute({
    getParentRoute: () => root,
    path: "/login",
    validateSearch: parseLoginSearch,
    component: () => <p>登录页面</p>,
  });
  const router = createRouter({
    routeTree: root.addChildren([config, other, login]),
    history: createMemoryHistory({ initialEntries: [url] }),
  });
  await router.load();
  render(<RouterProvider router={router} />);
  await screen.findByRole("heading", { level: 1, name: "系统配置" });
  await waitFor(() => expect(post.mock.calls.some(([path]) => path === "/config/list")).toBe(true));
  return router;
}
async function tab(name: string) {
  fireEvent.mouseDown(screen.getByRole("tab", { name }), { button: 0, ctrlKey: false });
  await waitFor(() =>
    expect(screen.getByRole("tab", { name }).getAttribute("aria-selected")).toBe("true"),
  );
}
function change(label: string, value: string) {
  fireEvent.change(screen.getByLabelText(label, { exact: false }), { target: { value } });
}
function choose(file: File) {
  fireEvent.change(screen.getByLabelText("SQLite 备份文件"), { target: { files: [file] } });
}
async function acceptRestore() {
  fireEvent.click(screen.getByRole("button", { name: "恢复备份" }));
  const dialog = await screen.findByRole("alertdialog", { name: "恢复站点备份" });
  expect(within(dialog).getByRole("button", { name: "恢复备份" }).hasAttribute("disabled")).toBe(
    true,
  );
  fireEvent.change(within(dialog).getByRole("textbox"), { target: { value: "恢复" } });
  fireEvent.click(within(dialog).getByRole("button", { name: "恢复备份" }));
}
describe("系统配置页的真实路由和草稿", () => {
  it("跨标签累计两个修改、URL标签和放弃恢复", async () => {
    const router = await mount();
    await screen.findByDisplayValue("go-panel");
    change("应用名称", "新名称");
    await tab("节点与安全");
    change("GitHub 下载代理", "https://proxy.example.com");
    expect(screen.getByText("2 项未保存的修改")).toBeTruthy();
    expect(router.state.location.search.tab).toBe("security");
    fireEvent.click(screen.getByRole("button", { name: "放弃修改" }));
    expect(screen.queryByRole("region", { name: "未保存的系统配置" })).toBeNull();
    expect((screen.getByLabelText("GitHub 下载代理") as HTMLInputElement).value).toBe("");
    await tab("站点");
    expect((screen.getByLabelText("应用名称") as HTMLInputElement).value).toBe("go-panel");
  });
  it("改回原值不计dirty，保存只发送真实变更并保留服务端结果", async () => {
    await mount();
    await screen.findByDisplayValue("go-panel");
    change("应用名称", "新名称");
    change("应用名称", "go-panel");
    expect(screen.queryByRole("region", { name: "未保存的系统配置" })).toBeNull();
    change("应用名称", "保存的名称");
    await tab("节点与安全");
    change("GitHub 下载代理", " https://proxy.example.com/ ");
    fireEvent.click(screen.getByRole("button", { name: "保存配置" }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith(
        "/config/update",
        { app_name: "保存的名称", github_download_proxy: "https://proxy.example.com" },
        undefined,
      ),
    );
    await waitFor(() =>
      expect(screen.queryByRole("region", { name: "未保存的系统配置" })).toBeNull(),
    );
    await tab("站点");
    expect((screen.getByLabelText("应用名称") as HTMLInputElement).value).toBe("保存的名称");
  });
  it("离开页面被拦截、取消保留草稿、确认才离开", async () => {
    const router = await mount();
    await screen.findByDisplayValue("go-panel");
    change("应用名称", "未保存");
    // 通过用户实际点击链接触发导航；被 history blocker 取消的 navigate Promise 不会完成。
    fireEvent.click(screen.getByRole("link", { name: "离开系统配置" }));
    let dialog = await screen.findByRole("alertdialog", { name: "放弃未保存的修改？" });
    expect(router.state.location.pathname).toBe("/config");
    fireEvent.click(within(dialog).getByRole("button", { name: "取消" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
    expect(router.state.location.pathname).toBe("/config");
    expect((screen.getByLabelText("应用名称") as HTMLInputElement).value).toBe("未保存");
    fireEvent.click(screen.getByRole("link", { name: "离开系统配置" }));
    dialog = await screen.findByRole("alertdialog", { name: "放弃未保存的修改？" });
    fireEvent.click(within(dialog).getByRole("button", { name: "放弃修改并离开" }));
    await screen.findByText("其他页面");
    expect(router.state.location.pathname).toBe("/elsewhere");
  });
  it("加载失败可重试、后台重取不抹除草稿", async () => {
    const initial = post.getMockImplementation()!;
    let configLoadFailed = false;
    post.mockImplementation((path, ...args) => {
      if (path === "/config/list" && !configLoadFailed) {
        configLoadFailed = true;
        return Promise.reject(new Error("配置服务暂不可用"));
      }
      return initial(path, ...args);
    });
    await mount();
    expect(await screen.findByText("配置服务暂不可用")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "重试" }));
    await screen.findByDisplayValue("go-panel");
    change("应用名称", "保留草稿");
    await act(async () => {
      await client.invalidateQueries({ queryKey: ["config"] });
    });
    expect((screen.getByLabelText("应用名称") as HTMLInputElement).value).toBe("保留草稿");
  });
  it("缺少公网地址可填当前地址、非法地址展示关联错误", async () => {
    stored.ip = "";
    await mount();
    await screen.findByRole("button", { name: "使用当前地址" });
    fireEvent.click(screen.getByRole("button", { name: "使用当前地址" }));
    expect(
      (screen.getByLabelText("面板公网地址", { exact: false }) as HTMLInputElement).value,
    ).not.toBe("");
    change("面板公网地址", "not a host");
    fireEvent.click(screen.getByRole("button", { name: "保存配置" }));
    expect(
      screen.getByLabelText("面板公网地址", { exact: false }).getAttribute("aria-invalid"),
    ).toBe("true");
    expect(screen.getByText(/面板公网地址格式不正确/)).toBeTruthy();
    expect(post.mock.calls.some(([path]) => path === "/config/update")).toBe(false);
  });
  it("R2独立保存、不计全局dirty，跨标签保留草稿", async () => {
    await mount("/config?tab=r2");
    await screen.findByLabelText("R2 存储桶");
    change("R2 存储桶", "panel-backups");
    expect(screen.queryByRole("region", { name: "未保存的系统配置" })).toBeNull();
    await tab("站点");
    await tab("云端备份 R2");
    expect((screen.getByLabelText("R2 存储桶") as HTMLInputElement).value).toBe("panel-backups");
    fireEvent.click(screen.getByRole("button", { name: "保存 R2 设置" }));
    await waitFor(() =>
      expect(post.mock.calls.some(([path]) => path === "/backup/r2/update")).toBe(true),
    );
    expect(post.mock.calls.some(([path]) => path === "/config/update")).toBe(false);
  });
});
describe("自定义配置项", () => {
  it("新增校验非法、重复和内部键，支持多行并统一保存", async () => {
    await mount("/config?tab=advanced");
    await screen.findByText("custom_key");
    fireEvent.click(screen.getByRole("button", { name: "新增配置项" }));
    const dialog = await screen.findByRole("dialog", { name: "新增配置项" });
    const name = within(dialog).getByLabelText("键名", { exact: false });
    const submit = within(dialog).getByRole("button", { name: "添加配置项" });
    fireEvent.change(name, { target: { value: "Invalid-key" } });
    fireEvent.click(submit);
    expect(await screen.findByText("键名只允许小写字母、数字和下划线")).toBeTruthy();
    expect(name.getAttribute("aria-invalid")).toBe("true");
    fireEvent.change(name, { target: { value: "custom_key" } });
    fireEvent.click(submit);
    expect(await screen.findByText("键名已存在")).toBeTruthy();
    fireEvent.change(name, { target: { value: "backup_r2_secret_access_key" } });
    fireEvent.click(submit);
    expect(await screen.findByText(/该键名由系统内部维护/)).toBeTruthy();
    fireEvent.change(name, { target: { value: "new_template" } });
    fireEvent.change(within(dialog).getByLabelText("值"), { target: { value: "first\nsecond" } });
    fireEvent.click(submit);
    const textarea = await screen.findByLabelText("new_template 的值");
    expect(textarea.tagName).toBe("TEXTAREA");
    expect(textarea.getAttribute("rows")).toBe("6");
    fireEvent.click(screen.getByRole("button", { name: "保存配置" }));
    await waitFor(() => expect(stored.new_template).toBe("first\nsecond"));
  });
  it("删除先确认再调用服务器，取消不写入", async () => {
    await mount("/config?tab=advanced");
    await screen.findByText("custom_key");
    fireEvent.click(screen.getByRole("button", { name: "删除配置项 custom_key" }));
    let dialog = await screen.findByRole("alertdialog", { name: "删除配置项" });
    fireEvent.click(within(dialog).getByRole("button", { name: "取消" }));
    expect(post.mock.calls.some(([path]) => path === "/config/delete")).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: "删除配置项 custom_key" }));
    dialog = await screen.findByRole("alertdialog", { name: "删除配置项" });
    fireEvent.change(within(dialog).getByRole("textbox"), { target: { value: "custom_key" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "删除配置项" }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/config/delete", { name: "custom_key" }, undefined),
    );
    await waitFor(() => expect(screen.queryByText("custom_key")).toBeNull());
    expect(stored.custom_key).toBeUndefined();
  });
  it("受保护键无删除按钮，删除失败保留配置和草稿", async () => {
    stored.backup_r2_enabled = "false";
    stored.subscription_api_key = "redacted";
    stored.subscription_profiles_backfilled = "true";
    await mount("/config?tab=advanced");
    await screen.findByText("custom_key");
    for (const name of [
      "ip",
      "app_name",
      "github_download_proxy",
      "allow_insecure_node_downloads",
      "backup_r2_enabled",
      "subscription_api_key",
      "subscription_profiles_backfilled",
    ])
      expect(screen.queryByRole("button", { name: `删除配置项 ${name}` })).toBeNull();
    change("custom_key 的值", "保留");
    const initial = post.getMockImplementation()!;
    post.mockImplementation((path, ...args) =>
      path === "/config/delete" ? Promise.reject(new Error("删除失败")) : initial(path, ...args),
    );
    fireEvent.click(screen.getByRole("button", { name: "删除配置项 custom_key" }));
    const dialog = await screen.findByRole("alertdialog");
    fireEvent.change(within(dialog).getByRole("textbox"), { target: { value: "custom_key" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "删除配置项" }));
    await waitFor(() =>
      expect(post.mock.calls.some(([path]) => path === "/config/delete")).toBe(true),
    );
    expect((screen.getByLabelText("custom_key 的值") as HTMLInputElement).value).toBe("保留");
    expect(screen.getByText("1 项未保存的修改")).toBeTruthy();
  });
});
describe("SQLite备份恢复", () => {
  it("拒绝zip和超256MB文件，accept只列SQLite且不上传", async () => {
    await mount("/config?tab=backup");
    expect(screen.getByLabelText("SQLite 备份文件").getAttribute("accept")).toBe(
      ".db,.sqlite,.sqlite3",
    );
    choose(new File(["zip"], "backup.zip"));
    expect(await screen.findByText(/只支持 .db、.sqlite 或 .sqlite3/)).toBeTruthy();
    const large = new File(["sqlite"], "large.db");
    Object.defineProperty(large, "size", { value: maxRestoreBytes + 1 });
    choose(large);
    expect(await screen.findByText("备份文件不能超过 256 MB")).toBeTruthy();
    expect(upload).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "恢复备份" }).hasAttribute("disabled")).toBe(true);
  });
  it("下载保留服务端文件名并显示loading", async () => {
    let finish!: (value: { blob: Blob; filename: string }) => void;
    download.mockImplementation(
      () =>
        new Promise((resolve) => {
          finish = resolve;
        }),
    );
    vi.stubGlobal(
      "URL",
      Object.assign(URL, { createObjectURL: vi.fn(() => "blob:test"), revokeObjectURL: vi.fn() }),
    );
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (
      this: HTMLAnchorElement,
    ) {
      expect(this.download).toBe("server-backup.db");
    });
    await mount("/config?tab=backup");
    fireEvent.click(screen.getByRole("button", { name: "下载备份" }));
    expect(screen.getByRole("button", { name: "下载备份" }).getAttribute("aria-busy")).toBe("true");
    await act(async () => {
      finish({ blob: new Blob(["sqlite"]), filename: "server-backup.db" });
    });
    expect(click).toHaveBeenCalledOnce();
    expect(URL.revokeObjectURL).toHaveBeenCalledWith("blob:test");
  });
  it("输入恢复后才能执行、过程不可取消、真实摘要3秒后清会话跳转", async () => {
    let finish!: (value: { restoredAt: number; preRestoreBackup: string }) => void;
    upload.mockImplementation(
      () =>
        new Promise((resolve) => {
          finish = resolve;
        }),
    );
    const clear = vi.spyOn(session, "clearSession");
    const router = await mount("/config?tab=backup");
    choose(new File(["sqlite"], "panel.sqlite3"));
    await acceptRestore();
    expect(upload).toHaveBeenCalledOnce();
    const [path, body, options] = upload.mock.calls[0];
    expect(path).toBe("/backup/restore");
    expect((body as FormData).get("file")).toBeInstanceOf(File);
    expect(options?.timeoutMs).toBe(600000);
    expect(await screen.findByText(/正在上传并恢复数据库/)).toBeTruthy();
    expect(screen.getByRole("tab", { name: "站点" }).hasAttribute("disabled")).toBe(true);
    const notice = vi.spyOn(notify, "info");
    fireEvent.click(screen.getByRole("link", { name: "离开系统配置" }));
    await waitFor(() =>
      expect(notice).toHaveBeenCalledWith("恢复过程中请保持页面打开，完成后会自动退出登录"),
    );
    expect(router.state.location.pathname).toBe("/config");
    expect(screen.queryByRole("button", { name: "取消" })).toBeNull();
    vi.useFakeTimers();
    await act(async () => {
      finish({
        restoredAt: 1792999839177,
        preRestoreBackup: "go-panel.db.pre-restore-20261023-120000.bak",
      });
    });
    expect(screen.getByText("go-panel.db.pre-restore-20261023-120000.bak")).toBeTruthy();
    expect(screen.getByText("数据已恢复，3 秒后自动退出登录")).toBeTruthy();
    expect(clear).not.toHaveBeenCalled();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2999);
    });
    expect(clear).not.toHaveBeenCalled();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1);
    });
    expect(clear).toHaveBeenCalledWith("restored");
    expect(session.readSession().token).toBeNull();
    expect(router.state.location.pathname).toBe("/login");
    expect(router.state.location.search.reason).toBe("restored");
  });
  it("恢复失败保留会话、允许重试", async () => {
    upload.mockRejectedValue(new Error("备份文件无效"));
    const router = await mount("/config?tab=backup");
    choose(new File(["invalid"], "panel.db"));
    await acceptRestore();
    expect(await screen.findByText("备份文件无效")).toBeTruthy();
    expect(session.readSession().token).toBe("admin");
    expect(router.state.location.pathname).toBe("/config");
    expect(screen.getByRole("button", { name: "恢复备份" }).hasAttribute("disabled")).toBe(false);
  });
});
describe("配置边界与登录原因", () => {
  it("只允许五个标签与白名单登录原因", () => {
    expect(configSearch({ tab: "unexpected" })).toEqual({ tab: "site" });
    for (const tab of ["site", "security", "backup", "r2", "advanced"])
      expect(configSearch({ tab })).toEqual({ tab });
    expect(parseLoginSearch({ reason: "restored" })).toEqual({ reason: "restored" });
    expect(loginReasons.restored).toBe("数据已恢复，请重新登录");
    expect(parseLoginSearch({ reason: "toString" })).toEqual({});
  });
  it("后端保护规则与256MB包含边界", () => {
    expect(restoreFileError({ name: "BACKUP.DB", size: maxRestoreBytes })).toBeUndefined();
    expect(restoreFileError({ name: "backup.tar.gz", size: 1 })).toBeTruthy();
    expect(customConfigNameError("bad-name", [])).toBeTruthy();
    expect(customConfigNameError("ok_key", [])).toBeUndefined();
    expect(isProtectedConfigName("subscription_nodes_backfilled")).toBe(true);
    expect(isProtectedConfigName("diagnose_internet_probe")).toBe(false);
  });
});
