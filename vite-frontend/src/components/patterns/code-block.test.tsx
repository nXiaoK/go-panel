import "@testing-library/jest-dom/vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CodeBlock } from "./code-block";

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("代码块兼容与单行链接", () => {
  it("默认命令保留原始换行并完整复制", async () => {
    const copy = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal("navigator", { clipboard: { writeText: copy } });
    const command = "go-panel --listen 127.0.0.1:6365\n  --database /var/lib/panel.db";
    render(<CodeBlock code={command} />);
    expect(document.querySelector("pre")).toHaveClass("whitespace-pre-wrap", "break-all");
    expect(document.querySelector("code")?.textContent).toBe(command);
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "复制命令" })));
    expect(copy).toHaveBeenCalledExactlyOnceWith(command);
    expect(screen.getByText("已复制")).toBeInTheDocument();
  });
  it("单行省略保留完整title、唯一复制及尾部动作，复制后仍保持aria标签", async () => {
    vi.useFakeTimers();
    const copy = vi.fn().mockResolvedValue(undefined),
      open = vi.fn(),
      qr = vi.fn();
    vi.stubGlobal("navigator", { clipboard: { writeText: copy } });
    const link = `https://example.invalid/subscribe/${"long-token".repeat(30)}?format=clash`;
    render(
      <CodeBlock
        code={link}
        label="复制香港订阅链接"
        singleLine
        trailingActions={
          <>
            <button onClick={open}>打开链接</button>
            <button onClick={qr}>显示二维码</button>
          </>
        }
      />,
    );
    expect(document.querySelector("pre")).toBeNull();
    expect(document.querySelector("code")).toHaveClass("truncate", "min-w-0");
    expect(document.querySelector("code")).toHaveAttribute("title", link);
    expect(screen.getAllByRole("button")).toHaveLength(3);
    await act(async () =>
      fireEvent.click(screen.getByRole("button", { name: "复制香港订阅链接" })),
    );
    expect(copy).toHaveBeenCalledExactlyOnceWith(link);
    expect(screen.getByRole("status")).toHaveTextContent("已复制");
    expect(screen.getByRole("button", { name: "复制香港订阅链接" })).toHaveAttribute(
      "title",
      "已复制",
    );
    fireEvent.click(screen.getByRole("button", { name: "打开链接" }));
    fireEvent.click(screen.getByRole("button", { name: "显示二维码" }));
    expect(open).toHaveBeenCalledOnce();
    expect(qr).toHaveBeenCalledOnce();
    act(() => vi.advanceTimersByTime(2000));
    expect(screen.getByRole("status")).toBeEmptyDOMElement();
  });
  it("单行复制失败保留完整文本并提示手动复制", async () => {
    vi.stubGlobal("navigator", { clipboard: undefined });
    render(<CodeBlock code="https://example.invalid/token" label="复制订阅链接" singleLine />);
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "复制订阅链接" })));
    expect(screen.getByRole("status")).toHaveTextContent("请手动选择复制");
    expect(document.querySelector("code")?.textContent).toBe("https://example.invalid/token");
  });
});
