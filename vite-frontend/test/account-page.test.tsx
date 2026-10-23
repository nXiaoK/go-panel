import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AccountPage } from "@/features/account/account-page";
import { ProfilePage } from "@/features/account/page";
import { SettingsPage } from "@/features/account/settings-page";
import { Route as AccountRoute } from "@/routes/_app.account";
import { parseLoginSearch } from "@/features/auth/login-search";
import { configureSessionForTest, readSession, setSession } from "@/lib/session";
import { getUserPackageInfo, updatePassword } from "@/lib/api";
import type { UserPackage } from "@/lib/types";
import { notify } from "@/lib/notify";

const navigate = vi.hoisted(() => vi.fn());
const toast = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn(), warning: vi.fn() }));
vi.mock("sonner", () => ({ toast }));
vi.mock("@tanstack/react-router", async (original) => ({
  ...(await original<typeof import("@tanstack/react-router")>()),
  useNavigate: () => navigate,
}));
vi.mock("@/lib/api", async (original) => ({
  ...(await original<typeof import("@/lib/api")>()),
  getUserPackageInfo: vi.fn(),
  updatePassword: vi.fn(),
}));
let client: QueryClient;
function show(component = <ProfilePage />) {
  client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(<QueryClientProvider client={client}>{component}</QueryClientProvider>);
}
function passwords(newPassword = "x", confirmPassword = newPassword) {
  fireEvent.change(screen.getByLabelText("原密码"), { target: { value: "old" } });
  fireEvent.change(screen.getByLabelText("新密码"), { target: { value: newPassword } });
  fireEvent.change(screen.getByLabelText("确认新密码"), { target: { value: confirmPassword } });
}
beforeEach(() => {
  window.scrollTo = vi.fn();
  vi.clearAllMocks();
  localStorage.clear();
  configureSessionForTest({ storage: localStorage, navigate: vi.fn() });
  setSession({ token: "account-test", roleId: 1, name: "alice", mustChangePassword: false });
  vi.spyOn(AccountRoute, "useSearch").mockReturnValue({ tab: "security" });
  vi.spyOn(AccountRoute, "useNavigate").mockReturnValue(navigate);
  vi.mocked(updatePassword).mockResolvedValue("已修改");
  vi.mocked(getUserPackageInfo).mockResolvedValue({
    userInfo: {
      user: "alice",
      flow: 100,
      num: 10,
      inFlow: 80 * 1024 ** 3,
      outFlow: 2 * 1024 ** 3,
      expTime: Date.UTC(2026, 11, 31),
    },
  } as UserPackage);
});
afterEach(() => {
  cleanup();
  client?.clear();
  vi.restoreAllMocks();
  localStorage.clear();
});

describe("账户资料与安全", () => {
  it("普通用户显示真实流量、转发上限和到期时间", async () => {
    show();
    expect(
      (await screen.findByRole("meter", { name: "账户流量配额" })).getAttribute("aria-valuetext"),
    ).toBe("82.0 GB / 100 GB");
    expect(screen.getByText("转发上限")).toBeTruthy();
    expect(screen.getByText("10")).toBeTruthy();
    expect(screen.getByText("2026-12-31")).toBeTruthy();
    expect(screen.getByText("普通用户")).toBeTruthy();
  });
  it("管理员不显示配额并且不发起套餐请求", () => {
    setSession({ token: "admin-test", roleId: 0, name: "admin", mustChangePassword: false });
    show();
    expect(screen.getByText("管理员")).toBeTruthy();
    expect(screen.queryByLabelText("账户配额")).toBeNull();
    expect(getUserPackageInfo).not.toHaveBeenCalled();
  });
  it("0 GB遵循后端已用尽语义，不显示不限流量", async () => {
    vi.mocked(getUserPackageInfo).mockResolvedValue({
      userInfo: { flow: 0, num: 0, inFlow: 0, outFlow: 0 },
    } as UserPackage);
    show();
    expect(await screen.findByText("0 B / 0 GB · 流量已用尽")).toBeTruthy();
    expect(screen.queryByText("不限流量")).toBeNull();
  });
  it.each(["session", "url"])("%s 强制改密只显示修改登录信息与警告", (source) => {
    if (source === "session")
      setSession({ token: "forced-test", roleId: 1, name: "alice", mustChangePassword: true });
    else
      vi.mocked(AccountRoute.useSearch).mockReturnValue({ tab: "preferences", forcePassword: 1 });
    show(<AccountPage />);
    expect(screen.getByRole("alert").textContent).toBe("为了账号安全，请先修改初始密码");
    expect(screen.getByText("修改登录信息")).toBeTruthy();
    expect(screen.queryByText("账号")).toBeNull();
    expect(screen.queryByRole("button", { name: "退出登录" })).toBeNull();
    expect(screen.queryByRole("tablist")).toBeNull();
    expect(screen.queryByRole("switch")).toBeNull();
    expect(getUserPackageInfo).not.toHaveBeenCalled();
  });
  it("密码不一致时在确认字段下方关联错误并阻止请求", () => {
    show();
    passwords("新密码", "另一个");
    fireEvent.click(screen.getByRole("button", { name: "更新登录信息" }));
    const input = screen.getByLabelText("确认新密码");
    expect(input.getAttribute("aria-invalid")).toBe("true");
    expect(document.getElementById(input.getAttribute("aria-describedby")!)?.textContent).toBe(
      "两次输入的新密码不一致",
    );
    expect(updatePassword).not.toHaveBeenCalled();
  });
  it("与后端一致允许短密码，成功清会话并携带登录原因立即导航", async () => {
    show();
    await screen.findByRole("meter", { name: "账户流量配额" });
    passwords("x");
    fireEvent.click(screen.getByRole("button", { name: "更新登录信息" }));
    await waitFor(() =>
      expect(updatePassword).toHaveBeenCalledWith({
        newUsername: "alice",
        currentPassword: "old",
        newPassword: "x",
        confirmPassword: "x",
      }),
    );
    await waitFor(() => expect(readSession().token).toBeNull());
    expect(getUserPackageInfo).toHaveBeenCalledOnce();
    expect(navigate).toHaveBeenCalledWith({
      to: "/login",
      search: { reason: "password-changed" },
      replace: true,
    });
  });
  it("按UTF-8字节而非字符限制72字节，强度不改变规则", async () => {
    show();
    passwords("密".repeat(25));
    fireEvent.click(screen.getByRole("button", { name: "更新登录信息" }));
    expect(screen.getByLabelText("新密码").getAttribute("aria-invalid")).toBe("true");
    expect(screen.getByText("新密码不能超过 72 个 UTF-8 字节")).toBeTruthy();
    expect(updatePassword).not.toHaveBeenCalled();
    passwords("密".repeat(24));
    fireEvent.click(screen.getByRole("button", { name: "更新登录信息" }));
    await waitFor(() => expect(updatePassword).toHaveBeenCalledOnce());
  });
  it.each([
    ["当前密码错误", "原密码"],
    ["用户名已被占用", "用户名"],
  ])("后端错误 %s 对应字段可访问", async (message, label) => {
    vi.mocked(updatePassword).mockRejectedValue(new Error(message));
    show();
    passwords();
    fireEvent.click(screen.getByRole("button", { name: "更新登录信息" }));
    await waitFor(() =>
      expect(screen.getByLabelText(label).getAttribute("aria-invalid")).toBe("true"),
    );
    expect(screen.getByText(message)).toBeTruthy();
    expect(readSession().token).toBe("account-test");
  });
  it("密码可切换可见性，提交中保持busy并禁止重复", async () => {
    let resolve!: (value: string) => void;
    vi.mocked(updatePassword).mockReturnValue(
      new Promise((done) => {
        resolve = done;
      }),
    );
    show();
    passwords();
    fireEvent.click(screen.getByRole("button", { name: "显示原密码" }));
    expect(screen.getByLabelText("原密码").getAttribute("type")).toBe("text");
    fireEvent.click(screen.getByRole("button", { name: "隐藏原密码" }));
    expect(screen.getByLabelText("原密码").getAttribute("type")).toBe("password");
    fireEvent.click(screen.getByRole("button", { name: "更新登录信息" }));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "更新登录信息" }).getAttribute("aria-busy")).toBe(
        "true",
      ),
    );
    fireEvent.click(screen.getByRole("button", { name: "更新登录信息" }));
    expect(updatePassword).toHaveBeenCalledOnce();
    resolve("已修改");
    await waitFor(() => expect(navigate).toHaveBeenCalled());
  });
  it("账户标签写入tab参数，偏好页无面板地址、深色模式入口", async () => {
    show(<AccountPage />);
    fireEvent.mouseDown(screen.getByRole("tab", { name: "偏好" }), { button: 0, ctrlKey: false });
    await waitFor(() => expect(navigate).toHaveBeenCalledWith({ search: { tab: "preferences" } }));
    expect(screen.queryByText("面板地址")).toBeNull();
    expect(screen.queryByText("深色模式")).toBeNull();
  });
});
describe("账户偏好与登录原因", () => {
  it("开关保存pref键并让notify立即读取新值，警告错误仍显示", () => {
    show(<SettingsPage />);
    const notifications = screen.getByRole("switch", { name: "操作成功提示" });
    fireEvent.click(notifications);
    expect(localStorage.getItem("pref_notify")).toBe("0");
    expect(notifications.getAttribute("aria-checked")).toBe("false");
    notify.success("隐藏成功");
    notify.warning("保留警告");
    notify.error("保留错误");
    expect(toast.success).not.toHaveBeenCalled();
    expect(toast.warning).toHaveBeenCalledWith("保留警告");
    expect(toast.error).toHaveBeenCalledWith("保留错误");
    fireEvent.click(notifications);
    notify.success("显示成功");
    expect(toast.success).toHaveBeenCalledWith("显示成功");
    fireEvent.click(screen.getByRole("switch", { name: "列表自动刷新" }));
    expect(localStorage.getItem("pref_auto_refresh")).toBe("0");
    expect(screen.getByRole("switch", { name: "列表自动刷新" }).getAttribute("aria-checked")).toBe(
      "false",
    );
  });
  it("登录白名单接受password-changed，拒绝原型属性和任意文本", () => {
    expect(parseLoginSearch({ reason: "password-changed" })).toEqual({
      reason: "password-changed",
    });
    for (const reason of ["__proto__", "constructor", "任意文本"])
      expect(parseLoginSearch({ reason })).toEqual({});
  });
});
