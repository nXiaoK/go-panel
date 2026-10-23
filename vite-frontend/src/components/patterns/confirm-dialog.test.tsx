import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ConfirmProvider } from "./confirm-dialog";
import { useState } from "react";
import { flushSync } from "react-dom";
import { EntitySheet } from "./entity-sheet";
import { Input } from "@/components/ui/input";
import { useConfirm, usePrompt, type ConfirmOptions } from "@/hooks/use-confirm";
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});
function Harness({
  onResult,
  options = {},
}: {
  onResult: (value: unknown) => void;
  options?: Partial<ConfirmOptions>;
}) {
  const requestConfirm = useConfirm(),
    prompt = usePrompt();
  return (
    <>
      <button
        onClick={async () =>
          onResult(
            await requestConfirm({
              title: "删除转发",
              description: "删除后无法恢复",
              confirmText: "删除",
              tone: "danger",
              ...options,
            }),
          )
        }
      >
        打开确认
      </button>
      <button
        onClick={async () =>
          onResult(
            await prompt({
              title: "新增配置项",
              description: "添加自定义配置",
              inputLabel: "配置项名称",
              confirmText: "添加",
            }),
          )
        }
      >
        打开输入
      </button>
    </>
  );
}
describe("统一确认对话框", () => {
  it.each(["确认", "取消", "Esc"])("%s完成promise且关闭对话框", async (action) => {
    const result = vi.fn();
    render(
      <ConfirmProvider>
        <Harness onResult={result} />
      </ConfirmProvider>,
    );
    fireEvent.click(screen.getByText("打开确认"));
    await screen.findByRole("alertdialog");
    if (action === "Esc") fireEvent.keyDown(screen.getByRole("alertdialog"), { key: "Escape" });
    else fireEvent.click(screen.getByRole("button", { name: action === "确认" ? "删除" : "取消" }));
    await waitFor(() => expect(result).toHaveBeenCalledExactlyOnceWith(action === "确认"));
    expect(screen.queryByRole("alertdialog")).toBeNull();
  });
  it("要求名称完全一致才能确认，取消不会执行操作", async () => {
    const result = vi.fn();
    render(
      <ConfirmProvider>
        <Harness onResult={result} options={{ requireText: "game-a" }} />
      </ConfirmProvider>,
    );
    fireEvent.click(screen.getByText("打开确认"));
    const button = await screen.findByRole("button", { name: "删除" });
    expect((button as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(screen.getByLabelText("输入「game-a」确认"), { target: { value: "game-b" } });
    expect((button as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(screen.getByLabelText("输入「game-a」确认"), { target: { value: "game-a" } });
    expect((button as HTMLButtonElement).disabled).toBe(false);
    fireEvent.click(button);
    await waitFor(() => expect(result).toHaveBeenCalledWith(true));
  });
  it("并发确认排队且不丢失promise，后续输入状态重置", async () => {
    const results: boolean[] = [];
    function Queue() {
      const requestConfirm = useConfirm();
      return (
        <button
          onClick={() => {
            void requestConfirm({
              title: "第一步",
              description: "第一步说明",
              requireText: "one",
            }).then((value) => results.push(value));
            void requestConfirm({
              title: "第二步",
              description: "第二步说明",
              requireText: "two",
            }).then((value) => results.push(value));
          }}
        >
          排队
        </button>
      );
    }
    render(
      <ConfirmProvider>
        <Queue />
      </ConfirmProvider>,
    );
    fireEvent.click(screen.getByText("排队"));
    fireEvent.change(await screen.findByLabelText("输入「one」确认"), { target: { value: "one" } });
    fireEvent.click(screen.getByRole("button", { name: "确认操作" }));
    await screen.findByText("第二步");
    expect((screen.getByLabelText("输入「two」确认") as HTMLInputElement).value).toBe("");
    fireEvent.click(screen.getByRole("button", { name: "取消" }));
    await waitFor(() => expect(results).toEqual([true, false]));
  });
  it("输入对话框返回已清理文本，取消或卸载返回null/false", async () => {
    const result = vi.fn();
    const view = render(
      <ConfirmProvider>
        <Harness onResult={result} />
      </ConfirmProvider>,
    );
    fireEvent.click(screen.getByText("打开输入"));
    const button = await screen.findByRole("button", { name: "添加" });
    expect((button as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(screen.getByLabelText("配置项名称"), { target: { value: "  custom_key  " } });
    fireEvent.click(button);
    await waitFor(() => expect(result).toHaveBeenLastCalledWith("custom_key"));
    fireEvent.click(screen.getByText("打开输入"));
    fireEvent.keyDown(await screen.findByRole("alertdialog"), { key: "Escape" });
    await waitFor(() => expect(result).toHaveBeenLastCalledWith(null));
    fireEvent.click(screen.getByText("打开确认"));
    await screen.findByRole("alertdialog");
    act(() => view.unmount());
    await waitFor(() => expect(result).toHaveBeenLastCalledWith(false));
  });
});

function pointerClick(target: HTMLElement) {
  fireEvent(target, new MouseEvent("pointerdown", { bubbles: true, button: 0 }));
  fireEvent(target, new MouseEvent("mousedown", { bubbles: true, button: 0 }));
  target.focus();
  fireEvent(target, new MouseEvent("pointerup", { bubbles: true, button: 0 }));
  fireEvent(target, new MouseEvent("mouseup", { bubbles: true, button: 0 }));
  fireEvent.click(target);
}

function SheetHarness({ onDelete, onClose }: { onDelete: () => void; onClose: () => void }) {
  const requestConfirm = useConfirm();
  const [open, setOpen] = useState(true);
  const [draft, setDraft] = useState("原始草稿");
  return (
    <EntitySheet
      open={open}
      title="用户详情"
      onOpenChange={(value) => {
        setOpen(value);
        onClose();
      }}
    >
      <Input aria-label="草稿" value={draft} onChange={(event) => setDraft(event.target.value)} />
      <button
        type="button"
        onClick={async () => {
          if (
            await requestConfirm({
              title: "删除用户",
              description: "删除不可恢复",
              requireText: "alice",
              confirmText: "删除用户",
            })
          )
            onDelete();
        }}
      >
        打开删除确认
      </button>
    </EntitySheet>
  );
}

describe("抽屉内统一确认", () => {
  it.each(["取消（错名）", "取消（准确名）", "Escape"])(
    "%s后保留底层抽屉、草稿与触发焦点",
    async (action) => {
      const remove = vi.fn(),
        close = vi.fn();
      render(
        <ConfirmProvider>
          <SheetHarness onDelete={remove} onClose={close} />
        </ConfirmProvider>,
      );
      await act(async () => new Promise((resolve) => setTimeout(resolve, 5)));
      fireEvent.change(screen.getByLabelText("草稿"), { target: { value: "保留修改" } });
      const trigger = screen.getByRole("button", { name: "打开删除确认" });
      pointerClick(trigger);
      await screen.findByRole("alertdialog");
      await act(async () => new Promise((resolve) => setTimeout(resolve, 5)));
      fireEvent.change(screen.getByLabelText("输入「alice」确认"), { target: { value: "ali" } });
      expect((screen.getByRole("button", { name: "删除用户" }) as HTMLButtonElement).disabled).toBe(
        true,
      );
      if (action === "取消（准确名）") {
        fireEvent.change(screen.getByLabelText("输入「alice」确认"), {
          target: { value: "alice" },
        });
        expect(
          (screen.getByRole("button", { name: "删除用户" }) as HTMLButtonElement).disabled,
        ).toBe(false);
      }
      if (action.startsWith("取消")) pointerClick(screen.getByRole("button", { name: "取消" }));
      else fireEvent.keyDown(screen.getByRole("alertdialog"), { key: "Escape" });
      await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
      await act(async () => new Promise((resolve) => setTimeout(resolve, 5)));
      expect(close).not.toHaveBeenCalled();
      expect(remove).not.toHaveBeenCalled();
      expect(screen.getByRole("dialog").textContent).toContain("用户详情");
      expect((screen.getByLabelText("草稿") as HTMLInputElement).value).toBe("保留修改");
      expect(document.activeElement).toBe(trigger);
    },
  );
});

it("关闭动画期间的取消点击不应作为底层抽屉的外部点击", async () => {
  const computed = window.getComputedStyle.bind(window);
  vi.spyOn(window, "getComputedStyle").mockImplementation(
    (element) =>
      new Proxy(computed(element), {
        get(target, key) {
          if (key === "animationName" && element.getAttribute("role") === "alertdialog") {
            return element.getAttribute("data-state") === "open" ? "confirm-enter" : "confirm-exit";
          }
          return Reflect.get(target, key);
        },
      }),
  );
  const close = vi.fn();
  render(
    <ConfirmProvider>
      <SheetHarness onDelete={vi.fn()} onClose={close} />
    </ConfirmProvider>,
  );
  await act(async () => new Promise((resolve) => setTimeout(resolve, 5)));
  pointerClick(screen.getByRole("button", { name: "打开删除确认" }));
  const alert = await screen.findByRole("alertdialog");
  await act(async () => new Promise((resolve) => setTimeout(resolve, 5)));
  // 浏览器离散事件会在原生 click 到 document 前提交关闭；强制提交防止 act 批处理掩盖时序。
  const flushClick = () => flushSync(() => {});
  document.body.addEventListener("click", flushClick);
  try {
    pointerClick(screen.getByRole("button", { name: "取消" }));
  } finally {
    document.body.removeEventListener("click", flushClick);
  }
  await act(async () => new Promise((resolve) => setTimeout(resolve, 5)));
  expect(alert.getAttribute("data-state")).toBe("closed");
  expect(close).not.toHaveBeenCalled();
});

it("确认已获焦后追加队列，Enter推进且最终取消仍回最初页面触发器", async () => {
  const results: boolean[] = [];
  let queueSecond!: () => void;
  function QueuedFromDialog() {
    const requestConfirm = useConfirm();
    return (
      <button
        type="button"
        onClick={() => {
          void requestConfirm({
            title: "第一项操作",
            description: "第一项说明",
            requireText: "one",
          }).then((value) => results.push(value));
          queueSecond = () => {
            void requestConfirm({
              title: "第二项操作",
              description: "第二项说明",
              requireText: "two",
            }).then((value) => results.push(value));
          };
        }}
      >
        开始队列
      </button>
    );
  }
  render(
    <ConfirmProvider>
      <QueuedFromDialog />
    </ConfirmProvider>,
  );
  const trigger = screen.getByRole("button", { name: "开始队列" });
  pointerClick(trigger);
  const first = await screen.findByLabelText("输入「one」确认");
  await act(async () => new Promise((resolve) => setTimeout(resolve, 0)));
  act(() => first.focus());
  // 第二项入队时 activeElement 确实在第一项确认内部，模拟确认中的异步后续请求。
  expect(document.activeElement).toBe(first);
  act(() => queueSecond());
  fireEvent.change(first, { target: { value: "one" } });
  fireEvent.keyDown(first, { key: "Enter" });
  const second = await screen.findByLabelText("输入「two」确认");
  expect((second as HTMLInputElement).value).toBe("");
  expect(document.activeElement).not.toBe(trigger);
  fireEvent.keyDown(second, { key: "Escape" });
  await waitFor(() => expect(results).toEqual([true, false]));
  await waitFor(() => expect(document.activeElement).toBe(trigger));
  expect(screen.queryByRole("alertdialog")).toBeNull();
});
