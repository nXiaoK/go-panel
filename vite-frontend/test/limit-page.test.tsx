import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { LimitPage } from "@/features/limit/page";
import { ConfirmProvider } from "@/components/patterns/confirm-dialog";
import { apiPost } from "@/lib/api/client";
import { configureSessionForTest, setSession } from "@/lib/session";
import { notify } from "@/lib/notify";

vi.mock("@/lib/api/client", async (original) => ({
  ...(await original<typeof import("@/lib/api/client")>()),
  apiPost: vi.fn(),
}));
let client: QueryClient;
const limit = { id: 4, name: "100M-普通", tunnelId: 2, speed: 100 };
function show() {
  client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <ConfirmProvider>
        <LimitPage />
      </ConfirmProvider>
    </QueryClientProvider>,
  );
}
beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  configureSessionForTest({ storage: localStorage, navigate: vi.fn() });
  setSession({ token: "limit-test", roleId: 0, name: "admin", mustChangePassword: false });
  window.matchMedia = vi
    .fn()
    .mockReturnValue({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() });
  HTMLElement.prototype.scrollIntoView = vi.fn();
  window.scrollTo = vi.fn();
  HTMLElement.prototype.hasPointerCapture = vi.fn().mockReturnValue(false);
  vi.mocked(apiPost).mockImplementation(async (path) =>
    path === "/speed-limit/list"
      ? [limit]
      : path === "/tunnel/list"
        ? [{ id: 2, name: "香港→日本", type: 1, inNodeId: 1 }]
        : "已保存",
  );
});
afterEach(() => {
  cleanup();
  client?.clear();
  vi.restoreAllMocks();
  localStorage.clear();
});

describe("限速策略页面", () => {
  it("使用共享可排序表格，不为使用数额外查询授权", async () => {
    show();
    const table = await screen.findByRole("table", { name: "限速策略列表" });
    expect(within(table).getByText("香港→日本")).toBeTruthy();
    expect(screen.queryByRole("columnheader", { name: "使用中" })).toBeNull();
    expect(
      vi
        .mocked(apiPost)
        .mock.calls.map(([path]) => path)
        .sort(),
    ).toEqual(["/speed-limit/list", "/tunnel/list"]);
    fireEvent.click(screen.getByRole("button", { name: "速率 (Mbps)" }));
    expect(
      screen.getByRole("columnheader", { name: "速率 (Mbps)" }).getAttribute("aria-sort"),
    ).toBe("descending");
    fireEvent.click(screen.getByRole("button", { name: "速率 (Mbps)" }));
    expect(
      screen.getByRole("columnheader", { name: "速率 (Mbps)" }).getAttribute("aria-sort"),
    ).toBe("ascending");
  });
  it("新建三字段Dialog显示名称、隧道与整数速率的关联错误", async () => {
    show();
    await screen.findByText("100M-普通");
    fireEvent.click(screen.getByRole("button", { name: "新建策略" }));
    const dialog = screen.getByRole("dialog", { name: "新建限速策略" });
    expect(within(dialog).getByLabelText("策略名称").id).toBe("limit-form-name");
    fireEvent.change(within(dialog).getByLabelText("限速速率 (Mbps)"), {
      target: { value: "0.5" },
    });
    fireEvent.click(within(dialog).getByRole("button", { name: "保存策略" }));
    for (const name of ["策略名称", "关联隧道", "限速速率 (Mbps)"]) {
      const input = within(dialog).getByLabelText(name);
      expect(input.getAttribute("aria-invalid")).toBe("true");
      expect(
        document.getElementById(input.getAttribute("aria-describedby")!)?.textContent,
      ).toBeTruthy();
    }
    expect(vi.mocked(apiPost).mock.calls.some(([path]) => path === "/speed-limit/create")).toBe(
      false,
    );
  });
  it("新建提交真实隧道名称与整数速率", async () => {
    show();
    await screen.findByText("100M-普通");
    fireEvent.click(screen.getByRole("button", { name: "新建策略" }));
    fireEvent.change(screen.getByLabelText("策略名称"), { target: { value: "  最低限速  " } });
    fireEvent.change(screen.getByLabelText("限速速率 (Mbps)"), { target: { value: "1" } });
    fireEvent.pointerDown(screen.getByLabelText("关联隧道"), {
      button: 0,
      ctrlKey: false,
      pointerType: "mouse",
    });
    fireEvent.click(await screen.findByRole("option", { name: "香港→日本" }));
    fireEvent.click(screen.getByRole("button", { name: "保存策略" }));
    await waitFor(() =>
      expect(apiPost).toHaveBeenCalledWith(
        "/speed-limit/create",
        { name: "最低限速", speed: 1, tunnelId: 2, tunnelName: "香港→日本", id: undefined },
        undefined,
      ),
    );
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });
  it("编辑拒绝小数、接受后端允许的大整数，pending禁止重复与关闭", async () => {
    let resolve!: (value: string) => void;
    const original = vi.mocked(apiPost).getMockImplementation()!;
    vi.mocked(apiPost).mockImplementation((path, ...args) =>
      path === "/speed-limit/update"
        ? new Promise<string>((done) => {
            resolve = done;
          })
        : original(path, ...args),
    );
    show();
    fireEvent.click(await screen.findByRole("button", { name: "编辑限速策略" }));
    const speed = screen.getByLabelText("限速速率 (Mbps)");
    fireEvent.change(speed, { target: { value: "1.2" } });
    fireEvent.click(screen.getByRole("button", { name: "保存策略" }));
    expect(speed.getAttribute("aria-invalid")).toBe("true");
    fireEvent.change(speed, { target: { value: "1000000" } });
    fireEvent.click(screen.getByRole("button", { name: "保存策略" }));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "保存策略" }).getAttribute("aria-busy")).toBe(
        "true",
      ),
    );
    fireEvent.click(screen.getByRole("button", { name: "保存策略" }));
    fireEvent.click(screen.getByRole("button", { name: "关闭对话框" }));
    expect(screen.getByRole("dialog")).toBeTruthy();
    expect(
      vi.mocked(apiPost).mock.calls.filter(([path]) => path === "/speed-limit/update"),
    ).toHaveLength(1);
    resolve("已更新");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(apiPost).toHaveBeenCalledWith(
      "/speed-limit/update",
      { ...limit, tunnelName: "香港→日本", speed: 1000000 },
      undefined,
    );
  });
  it("删除必须确认，取消不请求，后端引用拒绝原因原样显示", async () => {
    const onError = vi.spyOn(notify, "error");
    const original = vi.mocked(apiPost).getMockImplementation()!;
    vi.mocked(apiPost).mockImplementation((path, ...args) =>
      path === "/speed-limit/delete"
        ? Promise.reject(new Error("该限速规则还有用户在使用 请先取消分配"))
        : original(path, ...args),
    );
    show();
    fireEvent.click(await screen.findByRole("button", { name: "删除限速策略" }));
    expect(screen.getByRole("alertdialog").textContent).toContain("仍被用户授权引用的策略无法删除");
    fireEvent.click(screen.getByRole("button", { name: "取消" }));
    expect(vi.mocked(apiPost).mock.calls.some(([path]) => path === "/speed-limit/delete")).toBe(
      false,
    );
    fireEvent.click(screen.getByRole("button", { name: "删除限速策略" }));
    fireEvent.click(
      within(screen.getByRole("alertdialog")).getByRole("button", { name: "删除限速策略" }),
    );
    await waitFor(() =>
      expect(onError).toHaveBeenCalledWith("该限速规则还有用户在使用 请先取消分配"),
    );
  });
  it("筛选空态可清除，移动端使用卡片且保留行操作", async () => {
    window.matchMedia = vi
      .fn()
      .mockReturnValue({ matches: true, addEventListener: vi.fn(), removeEventListener: vi.fn() });
    show();
    await screen.findByText("100M-普通");
    expect(screen.queryByRole("table")).toBeNull();
    expect(screen.getByRole("button", { name: "编辑限速策略" })).toBeTruthy();
    fireEvent.change(screen.getByRole("textbox", { name: "搜索" }), {
      target: { value: "找不到" },
    });
    await screen.findByText("没有匹配的结果");
    fireEvent.click(screen.getByRole("button", { name: "清除筛选" }));
    expect(await screen.findByText("100M-普通")).toBeTruthy();
  });
});
