import "@testing-library/jest-dom/vitest";

import { readFile } from "node:fs/promises";
import { createElement } from "react";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import QRCode from "qrcode";
import { afterEach, describe, expect, it, vi } from "vitest";

import {
  createSubscriptionQrDataUrl,
  selectSubscriptionQrState,
  useSubscriptionQrDataUrl,
} from "../src/lib/subscription-qr";

const subscriptionQrDialogSource = await readFile(
  "src/components/subscription/qr-dialog.tsx",
  "utf8",
);

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

function QrState({ open, value }: { open: boolean; value: string }) {
  const { dataUrl, error } = useSubscriptionQrDataUrl(open, value);

  return createElement(
    "div",
    null,
    dataUrl && createElement("img", { alt: "subscription QR", src: dataUrl }),
    error && createElement("p", { role: "alert" }, error),
  );
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

describe("createSubscriptionQrDataUrl", () => {
  it("synchronously hides state that does not belong to the current source", () => {
    const completed = {
      value: "first",
      dataUrl: "data:image/png;base64,old",
      error: "old error",
    };

    expect(selectSubscriptionQrState(false, "first", completed)).toEqual({
      dataUrl: "",
      error: "",
    });
    expect(selectSubscriptionQrState(true, "second", completed)).toEqual({
      dataUrl: "",
      error: "",
    });
    expect(selectSubscriptionQrState(true, "first", completed)).toEqual({
      dataUrl: completed.dataUrl,
      error: completed.error,
    });
  });

  it("renders the subscription QR from local data without a remote endpoint", () => {
    expect(subscriptionQrDialogSource).toContain("src={dataUrl}");
    expect(subscriptionQrDialogSource).toContain("useSubscriptionQrDataUrl(open, value)");
    expect(subscriptionQrDialogSource).not.toMatch(/https?:\/\//);
  });

  it("creates a local PNG data URL without calling fetch", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");

    const data = await createSubscriptionQrDataUrl("https://panel.example/sub/token");

    expect(data).toMatch(/^data:image\/png;base64,/);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("does not show a completed QR after the dialog closes", async () => {
    const pending = deferred<string>();
    vi.spyOn(QRCode, "toDataURL").mockImplementation(() => pending.promise);
    const view = render(createElement(QrState, { open: true, value: "first" }));

    view.rerender(createElement(QrState, { open: false, value: "first" }));
    await act(async () => pending.resolve("data:image/png;base64,stale"));

    expect(screen.queryByRole("img")).not.toBeInTheDocument();
  });

  it("keeps the newest QR when an older URL finishes later", async () => {
    const first = deferred<string>();
    const second = deferred<string>();
    vi.spyOn(QRCode, "toDataURL")
      .mockImplementationOnce(() => first.promise)
      .mockImplementationOnce(() => second.promise);
    const view = render(createElement(QrState, { open: true, value: "first" }));

    view.rerender(createElement(QrState, { open: true, value: "second" }));
    await act(async () => second.resolve("data:image/png;base64,newest"));
    await act(async () => first.resolve("data:image/png;base64,stale"));

    expect(screen.getByRole("img")).toHaveAttribute("src", "data:image/png;base64,newest");
  });

  it("clears an old QR and shows only a local error when generation fails", async () => {
    vi.spyOn(QRCode, "toDataURL")
      .mockResolvedValueOnce("data:image/png;base64,old")
      .mockRejectedValueOnce(new Error("secret-token"));
    const view = render(createElement(QrState, { open: true, value: "first" }));
    await screen.findByRole("img");

    view.rerender(createElement(QrState, { open: true, value: "secret-token" }));

    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("二维码生成失败"));
    expect(screen.queryByRole("img")).not.toBeInTheDocument();
    expect(screen.getByRole("alert")).not.toHaveTextContent("secret-token");
  });
});
