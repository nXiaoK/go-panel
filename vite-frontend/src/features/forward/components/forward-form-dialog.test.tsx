import { ForwardFormDialog } from "@/features/forward/components/forward-form-dialog";
import { createForward, updateForward } from "@/lib/api";
import { ApiError } from "@/lib/api/client";
import { notify } from "@/lib/notify";
import type { Forward, Tunnel, UserTunnelOption } from "@/lib/types";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("@/lib/notify", () => ({ notify: { success: vi.fn(), error: vi.fn() } }));
vi.mock("@/lib/api", () => ({ createForward: vi.fn(), updateForward: vi.fn() }));
vi.mock("@/components/ui/select", () => ({
  Select: ({
    value,
    onValueChange,
    children,
    disabled,
  }: {
    value: string;
    onValueChange: (value: string) => void;
    children: ReactNode;
    disabled?: boolean;
  }) => (
    <select
      value={value}
      onChange={(event) => onValueChange(event.target.value)}
      disabled={disabled}
    >
      {children}
    </select>
  ),
  SelectTrigger: () => null,
  SelectValue: () => null,
  SelectContent: ({ children }: { children: ReactNode }) => <>{children}</>,
  SelectItem: ({
    value,
    disabled,
    children,
  }: {
    value: string;
    disabled?: boolean;
    children: ReactNode;
  }) => (
    <option value={value} disabled={disabled}>
      {children}
    </option>
  ),
}));
vi.mock("@/features/forward/components/exit-member-picker", () => ({
  ExitMemberPicker: () => <div>出口成员选择</div>,
}));
afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});
const option: UserTunnelOption = {
  id: 3,
  name: "可用隧道",
  type: 2,
  inIp: "entry.example.com",
  inNodePortSta: 10000,
  inNodePortEnd: 20000,
  hasRelay: false,
  inNodeForwardMode: "gost",
  available: true,
};
const editing: Forward = {
  id: 8,
  name: "web",
  tunnelId: 3,
  inPort: 12345,
  remoteAddr: "example.com:443",
  status: 1,
  exitMode: "balance",
  exitStrategy: "round",
  exitMembers: [
    { outNodeId: 4, active: true },
    { outNodeId: 5, active: true },
  ],
};
function form(
  mode: "admin" | "user",
  current: Forward | null = null,
  tunnels: (Tunnel | UserTunnelOption)[] = [option],
) {
  const onOpenChange = vi.fn();
  const onSaved = vi.fn();
  render(
    <QueryClientProvider client={new QueryClient()}>
      <ForwardFormDialog
        mode={mode}
        open
        onOpenChange={onOpenChange}
        editing={current}
        tunnels={tunnels}
        nodes={[]}
        onSaved={onSaved}
      />
    </QueryClientProvider>,
  );
  return { onOpenChange, onSaved };
}
describe("regular-user forward form", () => {
  it("disables unusable grants, shows reasons and creates with the tunnel default exit", async () => {
    vi.mocked(createForward).mockResolvedValue("操作成功");
    const callbacks = form("user", null, [
      option,
      {
        ...option,
        id: 4,
        name: "已到期隧道",
        available: false,
        unavailableReason: "该隧道权限已到期",
      },
    ]);
    expect(screen.queryByText("出口成员选择")).toBeNull();
    expect(screen.queryByText("出口模式")).toBeNull();
    expect(screen.getByRole("option", { name: /已到期隧道/ }).hasAttribute("disabled")).toBe(true);
    expect(screen.getByText(/该隧道权限已到期/)).toBeTruthy();
    fireEvent.change(screen.getByLabelText("名称"), { target: { value: "new web" } });
    fireEvent.change(screen.getAllByRole("combobox")[0], { target: { value: "3" } });
    expect(screen.getByText("可用端口 10000–20000，留空自动分配")).toBeTruthy();
    fireEvent.change(screen.getByPlaceholderText(/10.211.55.5/), {
      target: { value: "example.com:443" },
    });
    fireEvent.click(screen.getByRole("button", { name: "保存" }));
    await waitFor(() => expect(createForward).toHaveBeenCalledOnce());
    await waitFor(() => expect(callbacks.onOpenChange).toHaveBeenCalledWith(false));
    expect(callbacks.onSaved).toHaveBeenCalledOnce();
    expect(vi.mocked(createForward).mock.calls[0][0]).toMatchObject({
      exitMode: "single",
      exitMembers: [],
      tunnelId: 3,
    });
  });
  it("omits all exit configuration when editing an administrator-configured forward", async () => {
    vi.mocked(updateForward).mockResolvedValue("操作成功");
    form("user", editing);
    fireEvent.click(screen.getByRole("button", { name: "保存" }));
    await waitFor(() => expect(updateForward).toHaveBeenCalledOnce());
    const payload = vi.mocked(updateForward).mock.calls[0][0];
    for (const key of ["exitMode", "exitStrategy", "exitMembers"])
      expect(Object.hasOwn(payload, key)).toBe(false);
  });
  it("retains administrator exit controls and payload", async () => {
    vi.mocked(updateForward).mockResolvedValue("操作成功");
    form("admin", editing, [{ id: 3, name: "admin tunnel", type: 2, inNodeId: 1, outNodeId: 4 }]);
    expect(screen.getByText("出口成员选择")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "保存" }));
    await waitFor(() => expect(updateForward).toHaveBeenCalledOnce());
    expect(vi.mocked(updateForward).mock.calls[0][0]).toMatchObject({
      exitMode: "balance",
      exitStrategy: "round",
      exitMembers: editing.exitMembers,
    });
  });
  it("reports missing targets inline and focuses the target field", async () => {
    form("user", { ...editing, remoteAddr: "" });
    const target = screen.getByPlaceholderText(/10.211.55.5/);
    fireEvent.click(screen.getByRole("button", { name: "保存" }));
    expect(await screen.findByText("请输入目标地址")).toBeTruthy();
    expect(target.getAttribute("aria-invalid")).toBe("true");
    await waitFor(() => expect(document.activeElement).toBe(target));
    expect(updateForward).not.toHaveBeenCalled();
    expect(notify.error).not.toHaveBeenCalled();
  });
  it("validates explicit port ranges inline without native browser blocking", async () => {
    form("user", editing);
    fireEvent.change(screen.getByRole("spinbutton"), { target: { value: "70000" } });
    fireEvent.click(screen.getByRole("button", { name: "保存" }));
    expect(await screen.findByText("入口端口范围应为 1-65535")).toBeTruthy();
    expect(screen.getByRole("spinbutton").getAttribute("aria-invalid")).toBe("true");
    expect(updateForward).not.toHaveBeenCalled();
  });
  it("confirms before discarding dirty form input", async () => {
    const callbacks = form("user", editing);
    fireEvent.change(screen.getByLabelText("名称"), { target: { value: "changed" } });
    fireEvent.click(screen.getByRole("button", { name: "取消" }));
    expect(await screen.findByRole("alertdialog", { name: "放弃未保存的修改？" })).toBeTruthy();
    expect(callbacks.onOpenChange).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "放弃修改" }));
    expect(callbacks.onOpenChange).toHaveBeenCalledWith(false);
  });
  it("releases the save button after a rejected request and allows retry", async () => {
    vi.mocked(updateForward)
      .mockRejectedValueOnce(new ApiError("系统维护中，请稍后重试", 503, 503))
      .mockResolvedValueOnce("操作成功");
    form("user", editing);
    const save = screen.getByRole("button", { name: "保存" });
    fireEvent.click(save);
    await waitFor(() =>
      expect(screen.getByRole("alert").textContent).toContain("系统维护中，请稍后重试"),
    );
    expect(notify.error).not.toHaveBeenCalled();
    expect(save.hasAttribute("disabled")).toBe(false);
    fireEvent.click(save);
    await waitFor(() => expect(updateForward).toHaveBeenCalledTimes(2));
  });
});
