import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { TrafficChart } from "./traffic-chart";
import { trafficChartGeometry, type TrafficPoint } from "../traffic";
const data: TrafficPoint[] = [
  { time: "00:00", down: 1, up: 2 },
  { time: "01:00", down: 4, up: 1 },
];
let resize: (width: number, height: number) => void;
beforeEach(() => {
  vi.stubGlobal(
    "ResizeObserver",
    class {
      constructor(callback: (entries: unknown[]) => void) {
        resize = (width, height) => callback([{ contentRect: { width, height } }]);
      }
      observe() {
        resize(800, 280);
      }
      disconnect() {}
    },
  );
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
describe("流量图像素坐标与无障碍", () => {
  it("800×280像素路径正确，5个整齐刻度，resize重新计算且圆点保持r4", () => {
    const geometry = trafficChartGeometry(data, 800, 280);
    expect(geometry.ticks).toEqual([0, 1, 2, 3, 4]);
    expect(geometry.path("down")).toBe("M 64.00 187.00 L 780.00 16.00");
    const { container } = render(<TrafficChart data={data} range="24h" view="chart" />);
    expect(container.querySelector('path[data-series="down"]')?.getAttribute("d")).toBe(
      geometry.path("down"),
    );
    fireEvent.focus(screen.getByRole("img"));
    expect(container.querySelectorAll("circle")).toHaveLength(2);
    for (const circle of container.querySelectorAll("circle")) {
      expect(circle.getAttribute("r")).toBe("4");
      expect(circle.getAttribute("stroke-width")).toBe("2");
    }
    expect(container.querySelector("svg")?.hasAttribute("preserveAspectRatio")).toBe(false);
    act(() => resize(390, 280));
    expect(container.querySelector('path[data-series="down"]')?.getAttribute("d")).toBe(
      "M 64.00 187.00 L 370.00 16.00",
    );
    expect(container.querySelector("circle")?.getAttribute("r")).toBe("4");
  });
  it("方向键/Home/End选择最近点，提示同时提供上下行及合计", () => {
    render(<TrafficChart data={data} range="7d" view="chart" />);
    const chart = screen.getByRole("img", { name: /过去 7 天流量趋势，合计 8.00 GB/ });
    fireEvent.focus(chart);
    expect(within(screen.getByRole("status")).getByText("00:00")).toBeTruthy();
    fireEvent.keyDown(chart, { key: "ArrowRight" });
    expect(within(screen.getByRole("status")).getByText("01:00")).toBeTruthy();
    expect(within(screen.getByRole("status")).getByText("5.00 GB")).toBeTruthy();
    fireEvent.keyDown(chart, { key: "Home" });
    expect(within(screen.getByRole("status")).getByText("00:00")).toBeTruthy();
    fireEvent.keyDown(chart, { key: "End" });
    expect(within(screen.getByRole("status")).getByText("01:00")).toBeTruthy();
    fireEvent.keyDown(chart, { key: "ArrowLeft" });
    expect(within(screen.getByRole("status")).getByText("00:00")).toBeTruthy();
    fireEvent.keyDown(chart, { key: "ArrowLeft" });
    expect(within(screen.getByRole("status")).getByText("00:00")).toBeTruthy();
  });
  it("表格行数等于每个数据点，数字等宽右对齐", () => {
    render(<TrafficChart data={data} range="24h" view="table" />);
    const table = screen.getByRole("table", { name: "流量明细" });
    expect(table.querySelectorAll("tbody tr")).toHaveLength(data.length);
    expect(
      within(table)
        .getAllByRole("columnheader")
        .map((item) => item.textContent),
    ).toEqual(["时间", "下行", "上行", "合计"]);
    expect(table.querySelector("tbody tr td:nth-child(2)")?.className).toContain(
      "text-right font-mono tabular",
    );
  });
  it("刷新保留上一帧60%透明度，首次骨架，无记录居中空态", () => {
    const { container, rerender } = render(<TrafficChart data={data} range="24h" view="chart" />);
    const path = container.querySelector('path[data-series="down"]')?.getAttribute("d");
    rerender(<TrafficChart data={data} range="24h" view="chart" refreshing />);
    expect(screen.getByTestId("traffic-frame").className).toContain("opacity-60");
    expect(container.querySelector('path[data-series="down"]')?.getAttribute("d")).toBe(path);
    expect(screen.queryByLabelText("流量趋势加载中")).toBeNull();
    rerender(<TrafficChart data={[]} range="24h" view="chart" loading />);
    expect(screen.getByLabelText("流量趋势加载中")).toBeTruthy();
    rerender(<TrafficChart data={[]} range="24h" view="chart" tunnelSelected />);
    expect(screen.getByText("暂无流量记录")).toBeTruthy();
    expect(screen.getByText(/该隧道尚无升级后的流量记录/)).toBeTruthy();
  });
});
