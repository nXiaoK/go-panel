import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { useUrlState } from "./use-url-state";
afterEach(cleanup);
const defaults = { q: "", status: "all", sort: "name", focus: "", page: 1 };
function Page() {
  const { state, setState } = useUrlState(defaults);
  return (
    <>
      <output data-testid="state">{JSON.stringify(state)}</output>
      <button onClick={() => void setState({ q: "香港", sort: "flow", page: 2 })}>搜索排序</button>
    </>
  );
}
it("还原共享URL、保留未修改字段并replace历史，刷新后状态一致", async () => {
  vi.spyOn(window, "scrollTo").mockImplementation(() => {});
  const root = createRootRoute();
  const route = createRoute({
    getParentRoute: () => root,
    path: "/",
    validateSearch: (s) => ({ ...defaults, ...s, focus: String(s.focus ?? "") }),
    component: Page,
  });
  const history = createMemoryHistory({
    initialEntries: ["/?q=original&status=error&focus=42&extra=keep"],
  });
  const router = createRouter({ routeTree: root.addChildren([route]), history });
  await router.load();
  render(<RouterProvider router={router} />);
  expect(screen.getByTestId("state").textContent).toContain('"focus":"42"');
  await act(async () => fireEvent.click(screen.getByText("搜索排序")));
  await waitFor(() =>
    expect(router.state.location.search).toMatchObject({
      q: "香港",
      status: "error",
      focus: "42",
      extra: "keep",
      sort: "flow",
      page: 2,
    }),
  );
  expect(history.length).toBe(1);
  await act(async () => router.load());
  expect(screen.getByTestId("state").textContent).toContain('"q":"香港"');
});
