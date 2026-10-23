import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogAction,
} from "@/components/ui/alert-dialog";
import "@testing-library/jest-dom/vitest";
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { useState } from "react";
import { AppContent } from "@/app/app-content";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "@/components/ui/dialog";
import { Checkbox } from "@/components/ui/checkbox";
import { Switch } from "@/components/ui/switch";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { FormField } from "./form-field";
import { Meter } from "./meter";
import { DataTable, type DataTableProps } from "./data-table";
import { EntitySheet } from "./entity-sheet";
import { CopyButton } from "./copy-button";
import { TableToolbar } from "./table-toolbar";
import { DiagnosisSheet } from "./diagnosis-sheet";
import type { NodeStatusCheck } from "@/lib/types";
import type { RowSelectionState, SortingState } from "@tanstack/react-table";
beforeEach(() => {
  vi.stubGlobal(
    "ResizeObserver",
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  );
  vi.stubGlobal(
    "matchMedia",
    vi
      .fn()
      .mockReturnValue({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() }),
  );
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
type Item = { id: string; name: string; amount: number };
const data: Item[] = [
  { id: "a", name: "甲", amount: 2 },
  { id: "b", name: "乙", amount: 1 },
  { id: "c", name: "丙", amount: 3 },
];
const props: DataTableProps<Item> = {
  data,
  getRowId: (row) => row.id,
  columns: [
    { accessorKey: "name", header: "名称" },
    { accessorKey: "amount", header: "用量", meta: { numeric: true, align: "right" } },
  ],
  renderMobileCard: (row) => <div data-testid="mobile-card">{row.name}</div>,
};
describe("表格行为", () => {
  it("排序切换aria-sort与数值顺序，并支持受控排序", () => {
    function Controlled() {
      const [sorting, onSortingChange] = useState<SortingState>([]);
      return <DataTable {...props} sorting={sorting} onSortingChange={onSortingChange} />;
    }
    render(<Controlled />);
    const head = screen.getByRole("columnheader", { name: "用量" });
    expect(head).toHaveAttribute("aria-sort", "none");
    fireEvent.click(within(head).getByRole("button"));
    expect(head).toHaveAttribute("aria-sort", "descending");
    expect(screen.getAllByRole("row")[1]).toHaveTextContent("丙");
    fireEvent.click(within(head).getByRole("button"));
    expect(head).toHaveAttribute("aria-sort", "ascending");
    expect(screen.getAllByRole("row")[1]).toHaveTextContent("乙");
  });
  it("选中两行只把真实两实体传给批量回调", () => {
    const received = vi.fn();
    render(
      <DataTable
        {...props}
        enableSelection
        bulkActions={(rows) => <Button onClick={() => received(rows)}>执行批量</Button>}
      />,
    );
    fireEvent.click(screen.getByRole("checkbox", { name: "选择行 a" }));
    fireEvent.click(screen.getByRole("checkbox", { name: "选择行 c" }));
    expect(screen.getByRole("region", { name: "批量操作" })).toHaveTextContent("已选 2 项");
    fireEvent.click(screen.getByRole("button", { name: "执行批量" }));
    expect(received).toHaveBeenCalledWith([data[0], data[2]]);
    fireEvent.click(screen.getByRole("button", { name: "取消选择" }));
    expect(screen.queryByRole("region", { name: "批量操作" })).toBeNull();
  });
  it.each([false, true])(
    "长列表跨断点后保留真实选择，批量操作与取消均可用，mobile=%s",
    (mobile) => {
      const listeners = new Set<() => void>();
      const media = {
        matches: mobile,
        addEventListener: (_: string, listener: () => void) => listeners.add(listener),
        removeEventListener: (_: string, listener: () => void) => listeners.delete(listener),
      };
      vi.mocked(window.matchMedia).mockReturnValue(media as unknown as MediaQueryList);
      const items = Array.from({ length: 50 }, (_, index) => ({
        id: String(index + 1),
        name: `长列表条目 ${index + 1}`,
        amount: index,
      }));
      const received = vi.fn();
      render(
        <AppContent aria-label="应用内容">
          <DataTable
            {...props}
            data={items}
            enableSelection
            bulkActions={(rows) => (
              <Button onClick={() => received(rows)}>处理所选长列表条目</Button>
            )}
          />
        </AppContent>,
      );
      fireEvent.click(screen.getByRole("checkbox", { name: "选择行 1" }));
      fireEvent.click(screen.getByRole("checkbox", { name: "选择行 50" }));
      const actions = screen.getByRole("region", { name: "批量操作" });
      expect(screen.getByRole("main", { name: "应用内容" })).toContainElement(actions);
      expect(actions).toHaveTextContent("已选 2 项");
      act(() => {
        media.matches = !mobile;
        listeners.forEach((listener) => listener());
      });
      expect(screen.getByRole("checkbox", { name: "选择行 1" })).toBeChecked();
      expect(screen.getByRole("checkbox", { name: "选择行 50" })).toBeChecked();
      expect(screen.queryByRole("table") !== null).toBe(mobile);
      fireEvent.click(screen.getByRole("button", { name: "处理所选长列表条目" }));
      expect(received).toHaveBeenCalledExactlyOnceWith([items[0], items[49]]);
      fireEvent.click(screen.getByRole("button", { name: "取消选择" }));
      expect(screen.queryByRole("region", { name: "批量操作" })).toBeNull();
      expect(screen.getByRole("checkbox", { name: "选择行 1" })).not.toBeChecked();
      expect(screen.getByRole("checkbox", { name: "选择行 50" })).not.toBeChecked();
    },
  );
  it("受控预选可供外部保存按钮使用，数据删除后清理无效选择", () => {
    const save = vi.fn();
    function Controlled({ items }: { items: Item[] }) {
      const [selection, setSelection] = useState<RowSelectionState>({ a: true, b: true });
      return (
        <>
          <DataTable
            {...props}
            data={items}
            enableSelection
            rowSelection={selection}
            onRowSelectionChange={setSelection}
          />
          <Button onClick={() => save(selection)}>外部保存</Button>
        </>
      );
    }
    const { rerender } = render(<Controlled items={data} />);
    expect(screen.getByRole("checkbox", { name: "选择行 a" })).toBeChecked();
    rerender(<Controlled items={[data[0]]} />);
    fireEvent.click(screen.getByText("外部保存"));
    expect(save).toHaveBeenCalledWith({ a: true });
  });
  it("390px实际使用matchMedia卡片", () => {
    vi.mocked(window.matchMedia).mockReturnValue({
      matches: true,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    } as unknown as MediaQueryList);
    render(<DataTable {...props} />);
    expect(screen.getAllByTestId("mobile-card")).toHaveLength(3);
    expect(screen.queryByRole("table")).toBeNull();
  });
  it("加载、错误、空数据与筛选空态分别显示", () => {
    const retry = vi.fn(),
      clear = vi.fn();
    const { rerender } = render(<DataTable {...props} loading />);
    expect(screen.getByRole("status")).toHaveTextContent("正在加载");
    rerender(<DataTable {...props} error={new Error("测试失败")} onRetry={retry} />);
    expect(screen.getByRole("alert")).toHaveTextContent("测试失败");
    fireEvent.click(screen.getByText("重试"));
    expect(retry).toHaveBeenCalledOnce();
    rerender(<DataTable {...props} data={[]} />);
    expect(screen.getByText("暂无数据")).toBeInTheDocument();
    rerender(<DataTable {...props} data={[]} filtered onClearFilters={clear} />);
    expect(screen.getByText("没有匹配的结果")).toBeInTheDocument();
    fireEvent.click(screen.getByText("清除筛选"));
    expect(clear).toHaveBeenCalledOnce();
  });
  it.each([false, true])("异步数据到达后定位、清选、聚焦并高亮2秒，mobile=%s", (mobile) => {
    vi.useFakeTimers();
    vi.mocked(window.matchMedia).mockReturnValue({
      matches: mobile,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    } as unknown as MediaQueryList);
    const scroll = vi.fn();
    HTMLElement.prototype.scrollIntoView = scroll;
    const { rerender } = render(
      <DataTable
        {...props}
        data={[]}
        loading
        focusRowId="b"
        enableSelection
        initialRowSelection={{ a: true }}
      />,
    );
    rerender(
      <DataTable {...props} focusRowId="b" enableSelection initialRowSelection={{ a: true }} />,
    );
    const focused = mobile
      ? screen.getByText("乙").closest("article")
      : screen.getByText("乙").closest("tr");
    expect(focused).toHaveAttribute("data-highlighted", "true");
    expect(focused).toHaveFocus();
    expect(scroll).toHaveBeenCalled();
    expect(screen.queryByRole("region", { name: "批量操作" })).toBeNull();
    act(() => vi.advanceTimersByTime(2000));
    expect(focused).not.toHaveAttribute("data-highlighted");
  });
  it("行鼠标与键盘激活，勾选和菜单按钮不触发行事件", () => {
    const activate = vi.fn();
    render(
      <DataTable
        {...props}
        enableSelection
        onRowActivate={activate}
        columns={[
          ...props.columns,
          { id: "actions", header: "操作", cell: () => <Button>更多操作</Button> },
        ]}
      />,
    );
    const row = screen.getByText("甲").closest("tr")!;
    fireEvent.click(screen.getByRole("checkbox", { name: "选择行 a" }));
    fireEvent.click(within(row).getByRole("button", { name: "更多操作" }));
    expect(activate).not.toHaveBeenCalled();
    fireEvent.click(screen.getByText("甲"));
    fireEvent.keyDown(row, { key: "Enter" });
    expect(activate).toHaveBeenCalledTimes(2);
    expect(activate).toHaveBeenCalledWith(data[0]);
  });
});
describe("表单与反馈", () => {
  it("Input和真实SelectTrigger都可由label访问，错误替代说明", () => {
    render(
      <>
        <FormField label="名称" hint="输入名称">
          <Input />
        </FormField>
        <FormField label="隧道" required hint="旧说明" error="必须选择">
          <Select>
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="a">A</SelectItem>
            </SelectContent>
          </Select>
        </FormField>
      </>,
    );
    expect(screen.getByLabelText("名称")).toBeInstanceOf(HTMLInputElement);
    const trigger = screen.getByLabelText(/隧道/);
    expect(trigger).toHaveAttribute("role", "combobox");
    expect(trigger).toHaveAttribute("aria-invalid", "true");
    expect(trigger).toHaveAttribute("aria-required", "true");
    expect(document.getElementById(trigger.getAttribute("aria-describedby")!)).toHaveTextContent(
      "必须选择",
    );
    expect(screen.queryByText("旧说明")).toBeNull();
  });
  it.each([
    [79, "good"],
    [80, "warning"],
    [95, "serious"],
    [100, "critical"],
  ])("Meter%s阈值和aria", (value, tone) => {
    render(<Meter value={Number(value)} max={100} label="流量" />);
    const meter = screen.getByRole("meter", { name: "流量" });
    expect(meter).toHaveAttribute("aria-valuenow", String(value));
    expect(meter).toHaveAttribute("aria-valuemin", "0");
    expect(meter).toHaveAttribute("aria-valuemax", "100");
    expect(meter.firstChild).toHaveAttribute("data-tone", tone);
  });
  it("缺失数值不渲染虚假0，超额aria数值截到max", () => {
    const { rerender } = render(<Meter value={undefined} max={100} />);
    expect(screen.queryByRole("meter")).toBeNull();
    expect(screen.getByText("暂无数据")).toBeInTheDocument();
    rerender(<Meter value={120} max={100} />);
    expect(screen.getByRole("meter")).toHaveAttribute("aria-valuenow", "100");
  });
  it("loading按钮保留文字并禁止点击，兼容原表单隐式提交", () => {
    const click = vi.fn(),
      submit = vi.fn((e: React.FormEvent) => e.preventDefault());
    const { rerender } = render(
      <Button loading onClick={click}>
        保存资源
      </Button>,
    );
    const button = screen.getByRole("button", { name: "保存资源" });
    expect(button).toBeDisabled();
    expect(button).toHaveAttribute("aria-busy", "true");
    fireEvent.click(button);
    expect(click).not.toHaveBeenCalled();
    rerender(
      <form onSubmit={submit}>
        <Button>原有保存</Button>
      </form>,
    );
    fireEvent.click(screen.getByText("原有保存"));
    expect(submit).toHaveBeenCalledOnce();
  });
  it("复制成功保持2秒且失败提供手动复制提示", async () => {
    vi.useFakeTimers();
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText: vi.fn().mockResolvedValue(undefined) },
    });
    render(<CopyButton text="命令" />);
    await act(async () => fireEvent.click(screen.getByText("复制")));
    expect(screen.getByText("已复制")).toBeInTheDocument();
    act(() => vi.advanceTimersByTime(2000));
    expect(screen.getByText("复制")).toBeInTheDocument();
    vi.mocked(navigator.clipboard.writeText).mockRejectedValue(new Error());
    await act(async () => fireEvent.click(screen.getByText("复制")));
    expect(screen.getByRole("status")).toHaveTextContent("请手动选择复制");
  });
  it("搜索防抖200ms并清理旧定时器，外部URL更新还原词", () => {
    vi.useFakeTimers();
    const change = vi.fn();
    const { rerender, unmount } = render(<TableToolbar searchValue="" onSearchChange={change} />);
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "香港" } });
    act(() => vi.advanceTimersByTime(199));
    expect(change).not.toHaveBeenCalled();
    act(() => vi.advanceTimersByTime(1));
    expect(change).toHaveBeenCalledWith("香港");
    rerender(<TableToolbar searchValue="东京" onSearchChange={change} />);
    expect(screen.getByRole("textbox")).toHaveValue("东京");
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "离开" } });
    unmount();
    act(() => vi.advanceTimersByTime(200));
    expect(change).toHaveBeenCalledTimes(1);
  });
});
describe("实体抽屉", () => {
  it.each(["关闭", "取消", "Escape", "遮罩"])("dirty时%s必须先确认", async (method) => {
    const change = vi.fn();
    render(
      <EntitySheet open onOpenChange={change} title="编辑" dirty onSubmit={vi.fn()}>
        <Input />
      </EntitySheet>,
    );
    await act(async () => new Promise((resolve) => setTimeout(resolve, 0)));
    if (method === "Escape") fireEvent.keyDown(document, { key: "Escape" });
    else if (method === "遮罩") {
      const overlay = document.querySelector('[data-state="open"].fixed.inset-0')!;
      fireEvent.pointerDown(overlay, { button: 0, ctrlKey: false });
      fireEvent.pointerUp(overlay);
      fireEvent.click(overlay);
    } else fireEvent.click(screen.getByRole("button", { name: method }));
    expect(change).not.toHaveBeenCalled();
    expect(await screen.findByRole("alertdialog")).toHaveTextContent("放弃未保存的修改");
    fireEvent.click(screen.getByText("继续编辑"));
    expect(change).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "取消" }));
    fireEvent.click(await screen.findByText("放弃修改"));
    expect(change).toHaveBeenCalledWith(false);
  });
  it("提交中防重并阻止关闭，头尾与滚动区平级", async () => {
    let resolve!: () => void;
    const submit = vi.fn(
      () =>
        new Promise<void>((r) => {
          resolve = r;
        }),
    );
    const close = vi.fn();
    render(
      <EntitySheet open onOpenChange={close} title="编辑" onSubmit={submit}>
        <Input />
      </EntitySheet>,
    );
    const button = screen.getByRole("button", { name: "保存修改" });
    fireEvent.click(button);
    fireEvent.click(button);
    fireEvent.click(screen.getByRole("button", { name: "关闭" }));
    expect(submit).toHaveBeenCalledOnce();
    expect(close).not.toHaveBeenCalled();
    expect(button).toBeDisabled();
    const dialog = screen.getByRole("dialog");
    const form = dialog.querySelector("form")!;
    expect(form).toHaveClass("min-h-0", "flex-1", "overflow-y-auto");
    expect(form.previousElementSibling).toHaveClass("shrink-0");
    expect(form.nextElementSibling).toHaveClass("shrink-0");
    await act(async () => resolve());
    expect(button).not.toBeDisabled();
  });
  it("自定义footer使用guarded requestClose，省略onSubmit不嵌套form", () => {
    render(
      <EntitySheet
        open
        onOpenChange={vi.fn()}
        title="只读详情"
        dirty
        footer={({ requestClose }) => <Button onClick={requestClose}>自定义关闭</Button>}
      >
        <form aria-label="授权表单">
          <Input />
        </form>
      </EntitySheet>,
    );
    expect(screen.getAllByRole("form")).toHaveLength(1);
    fireEvent.click(screen.getByText("自定义关闭"));
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
  });
});
it("诊断外网advisory不计失败，NodeStatusCheck保留单节点字段", () => {
  const { rerender } = render(
    <DiagnosisSheet
      open
      onOpenChange={vi.fn()}
      data={{
        tunnelType: "relay",
        timestamp: 1,
        results: [
          { success: true, nodeId: 1, nodeName: "入口", targetIp: "a", description: "链路检查" },
          {
            success: false,
            advisory: true,
            nodeId: 1,
            nodeName: "入口",
            targetIp: "b",
            description: "外网探测",
          },
        ],
      }}
    />,
  );
  expect(screen.getByText("链路诊断通过")).toBeInTheDocument();
  expect(screen.getByText("警告（仅供参考）")).toBeInTheDocument();
  const node: NodeStatusCheck = {
    nodeId: 7,
    name: "单节点",
    forwardMode: "nft",
    dbStatus: 1,
    connected: true,
    responsive: true,
    roundTripMs: 12,
    version: "1.0",
    latestVersion: "1.1",
    upgradeAvailable: true,
    syncState: "failed",
    syncLastError: "同步错误",
    syncLastAttempt: 1,
    checkedAt: 2,
  };
  rerender(<DiagnosisSheet open onOpenChange={vi.fn()} data={node} />);
  for (const label of [
    "单节点",
    "已连接",
    "响应正常",
    "12 ms",
    "1.0",
    "1.1",
    "可升级",
    "同步失败",
    "同步错误",
  ])
    expect(screen.getByText(label, { exact: false })).toBeInTheDocument();
});

it("旧Dialog直接body自动滚动，结果态诊断支持重试且loading防重", () => {
  const { unmount } = render(
    <Dialog open>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>旧表单</DialogTitle>
        </DialogHeader>
        <div className="grid gap-3">
          {Array.from({ length: 20 }, (_, i) => (
            <Input key={i} />
          ))}
        </div>
        <DialogFooter>
          <Button>旧提交</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>,
  );
  const heading = screen.getByText("旧表单");
  const body = heading.parentElement!.nextElementSibling!;
  expect(body).toHaveClass("min-h-0", "flex-1", "overflow-y-auto");
  expect(body.nextElementSibling).toContainElement(screen.getByRole("button", { name: "旧提交" }));
  unmount();
  const retry = vi.fn();
  const data = { tunnelType: "relay", timestamp: 1, results: [] };
  const { rerender } = render(
    <DiagnosisSheet open onOpenChange={vi.fn()} data={data} onRetry={retry} />,
  );
  fireEvent.click(screen.getByRole("button", { name: "重新诊断" }));
  expect(retry).toHaveBeenCalledOnce();
  rerender(<DiagnosisSheet open onOpenChange={vi.fn()} data={data} onRetry={retry} loading />);
  const button = screen.getByRole("button", { name: "重新诊断" });
  expect(button).toBeDisabled();
  fireEvent.click(button);
  expect(retry).toHaveBeenCalledOnce();
});

it("Switch、Checkbox、Radio组与可透传自定义控件均关联label/错误和required，复用自带id", () => {
  function ProxyInput(props: React.ComponentProps<"input">) {
    return <input {...props} />;
  }
  render(
    <>
      <FormField label="启用同步" error="同步错误" required>
        <Switch />
      </FormField>
      <FormField label="同意规则" error="必须勾选" required>
        <Checkbox />
      </FormField>
      <FormField label="出口模式" error="请选择" required>
        <RadioGroup>
          <RadioGroupItem value="one" aria-label="默认出口" />
        </RadioGroup>
      </FormField>
      <FormField label="自定义输入" error="格式错误">
        <ProxyInput id="custom-id" />
      </FormField>
      <FormField label="已有ID">
        <Input id="existing-id" />
      </FormField>
    </>,
  );
  for (const label of [/启用同步/, /同意规则/, /出口模式/, /自定义输入/]) {
    const control = screen.getByLabelText(label);
    expect(control).toHaveAttribute("aria-invalid", "true");
    expect(document.getElementById(control.getAttribute("aria-describedby")!)).not.toBeNull();
  }
  expect(screen.getByRole("radiogroup", { name: /出口模式/ })).toHaveAttribute(
    "aria-required",
    "true",
  );
  expect(screen.getByLabelText("自定义输入")).toHaveAttribute("id", "custom-id");
  expect(screen.getByLabelText("已有ID")).toHaveAttribute("id", "existing-id");
});
it("表单中的复制/清除搜索/错误重试不会触发保存", async () => {
  const submit = vi.fn((event: React.FormEvent) => event.preventDefault());
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText: vi.fn().mockResolvedValue(undefined) },
  });
  render(
    <form onSubmit={submit}>
      <CopyButton text="命令" />
      <TableToolbar searchValue="搜索词" onSearchChange={vi.fn()} />
      <DataTable {...props} error={new Error("错误")} onRetry={vi.fn()} />
      <DataTable {...props} enableSelection initialRowSelection={{ a: true }} />
    </form>,
  );
  await act(async () => fireEvent.click(screen.getByRole("button", { name: "复制" })));
  fireEvent.click(screen.getByRole("button", { name: "清除搜索" }));
  fireEvent.click(screen.getByRole("button", { name: "重试" }));
  fireEvent.click(screen.getByRole("button", { name: "取消选择" }));
  expect(submit).not.toHaveBeenCalled();
});

it.each(["loading", "disabled"])("asChild %s在子点击动作之前拦截且阻止链接导航", (state) => {
  const click = vi.fn(),
    capture = vi.fn();
  render(
    <Button asChild loading={state === "loading"} disabled={state === "disabled"}>
      <a href="#never" onClick={click} onClickCapture={capture}>
        链接操作
      </a>
    </Button>,
  );
  const link = screen.getByRole("link", { name: "链接操作" });
  expect(fireEvent.click(link)).toBe(false);
  expect(click).not.toHaveBeenCalled();
  expect(capture).not.toHaveBeenCalled();
  expect(link).toHaveAttribute("aria-disabled", "true");
  if (state === "loading") {
    expect(link).toHaveAttribute("aria-busy", "true");
    expect(link.querySelector(".lucide-loader-circle")).not.toBeNull();
  }
});
it("AlertDialog长说明置于独立滚动区，标题和最终动作不随内容滚走", () => {
  render(
    <AlertDialog open>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>批量操作结果</AlertDialogTitle>
          <AlertDialogDescription>{"长结果".repeat(200)}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogAction>关闭结果</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>,
  );
  const dialog = screen.getByRole("alertdialog");
  expect(dialog).toHaveClass("overflow-hidden");
  const body = screen.getByText("批量操作结果").parentElement!.nextElementSibling!;
  expect(body).toHaveClass("min-h-0", "flex-1", "overflow-y-auto");
  expect(body.nextElementSibling).toContainElement(
    screen.getByRole("button", { name: "关闭结果" }),
  );
  expect(body).toContainElement(screen.getByText("长结果".repeat(200)));
});
it("复合字段render prop为实际控件生成唯一ID，布局div不冒充控件", () => {
  render(
    <FormField label="多目标" error="目标错误">
      {(control) => (
        <div>
          <Input {...control} />
          <Input
            {...control}
            id={`${control.id}-2`}
            aria-labelledby={undefined}
            aria-label="目标地址 2"
          />
        </div>
      )}
    </FormField>,
  );
  const first = screen.getByLabelText("多目标"),
    second = screen.getByLabelText("目标地址 2");
  expect(first.tagName).toBe("INPUT");
  expect(first.id).not.toBe(second.id);
  expect(document.querySelectorAll(`[id="${first.id}"]`)).toHaveLength(1);
  expect(first).toHaveAttribute("aria-invalid", "true");
  expect(second).toHaveAttribute("aria-describedby", first.getAttribute("aria-describedby"));
});
