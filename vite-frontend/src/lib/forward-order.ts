export type ForwardOrderFilters = {
  query?: string | null;
  status?: string | null;
  tunnelID?: string | number | null;
};

export type ForwardOrderRow = {
  id: number;
  inx?: number;
};

export type MoveDirection = "up" | "down";

/** Reordering is only safe on the full unfiltered list. */
export function canReorderForwards(filters: ForwardOrderFilters): boolean {
  const query = String(filters.query ?? "").trim();
  const status = String(filters.status ?? "all").trim() || "all";
  const tunnel =
    filters.tunnelID === null || filters.tunnelID === undefined
      ? "all"
      : String(filters.tunnelID).trim() || "all";
  return query === "" && status === "all" && tunnel === "all";
}

/** Move a row one step within the full ordered list and reindex 1-based. */
export function moveForwardByID<T extends ForwardOrderRow>(
  rows: T[],
  id: number,
  direction: MoveDirection,
): T[] {
  const idx = rows.findIndex((row) => row.id === id);
  if (idx < 0) return rows;

  const target = direction === "up" ? idx - 1 : idx + 1;
  if (target < 0 || target >= rows.length) return rows;

  const next = [...rows];
  [next[idx], next[target]] = [next[target], next[idx]];
  return next.map((row, i) => ({ ...row, inx: i + 1 }));
}

export function forwardOrderIDs(rows: ForwardOrderRow[]): number[] {
  return rows.map((row) => row.id);
}
