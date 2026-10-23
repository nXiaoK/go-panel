import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ConfirmProvider } from "./confirm-dialog";
import { useConfirm, usePrompt, type ConfirmOptions } from "@/hooks/use-confirm";
afterEach(cleanup);
function Harness({
  onResult,
  options = {},
}: {
  onResult: (value: unknown) => void;
  options?: Partial<ConfirmOptions>;
}) {
  const confirm = useConfirm(),
    prompt = usePrompt();
  return (
    <>
      <button
        onClick={async () =>
          onResult(
            await confirm({
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
      const confirm = useConfirm();
      return (
        <button
          onClick={() => {
            void confirm({ title: "第一步", description: "第一步说明", requireText: "one" }).then(
              (value) => results.push(value),
            );
            void confirm({ title: "第二步", description: "第二步说明", requireText: "two" }).then(
              (value) => results.push(value),
            );
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
