import { useEffect, useRef, useState, type ReactNode } from "react";
import {
  flexRender,
  getCoreRowModel,
  getSortedRowModel,
  useReactTable,
  type ColumnDef,
  type OnChangeFn,
  type RowData,
  type RowSelectionState,
  type SortingState,
} from "@tanstack/react-table";
import { ArrowDown, ArrowUp, ChevronsUpDown } from "lucide-react";
import {
  Table,
  TableHeader,
  TableBody,
  TableHead,
  TableRow,
  TableCell,
} from "@/components/ui/table";
import { Checkbox } from "@/components/ui/checkbox";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { EmptyState, ErrorState, LoadingRows } from "./feedback";
declare module "@tanstack/react-table" {
  interface ColumnMeta<TData extends RowData, TValue> {
    align?: "left" | "right";
    numeric?: boolean;
    actions?: boolean;
    rowType?: TData;
    valueType?: TValue;
  }
}
export type DataTableProps<T> = {
  columns: ColumnDef<T>[];
  data: T[];
  getRowId: (row: T) => string;
  loading?: boolean;
  error?: unknown;
  onRetry?: () => void;
  emptyState?: ReactNode;
  filtered?: boolean;
  onClearFilters?: () => void;
  enableSelection?: boolean;
  bulkActions?: (rows: T[]) => ReactNode;
  renderMobileCard: (row: T) => ReactNode;
  initialSorting?: SortingState;
  sorting?: SortingState;
  onSortingChange?: OnChangeFn<SortingState>;
  initialRowSelection?: RowSelectionState;
  rowSelection?: RowSelectionState;
  onRowSelectionChange?: OnChangeFn<RowSelectionState>;
  focusRowId?: string;
  onRowActivate?: (row: T) => void;
  onRowClick?: (row: T) => void;
  ariaLabel?: string;
};
function useMobileCards() {
  const [mobile, setMobile] = useState(
    () => typeof window !== "undefined" && window.matchMedia("(max-width: 767px)").matches,
  );
  useEffect(() => {
    const media = window.matchMedia("(max-width: 767px)");
    const update = () => setMobile(media.matches);
    update();
    media.addEventListener("change", update);
    return () => media.removeEventListener("change", update);
  }, []);
  return mobile;
}
export function DataTable<T>({
  columns,
  data,
  getRowId,
  loading,
  error,
  onRetry,
  emptyState,
  filtered,
  onClearFilters,
  enableSelection,
  bulkActions,
  renderMobileCard,
  initialSorting = [],
  sorting: controlledSorting,
  onSortingChange,
  initialRowSelection = {},
  rowSelection: controlledSelection,
  onRowSelectionChange,
  focusRowId,
  onRowActivate,
  onRowClick,
  ariaLabel = "数据列表",
}: DataTableProps<T>) {
  const mobile = useMobileCards();
  const [localSorting, setLocalSorting] = useState<SortingState>(initialSorting);
  const [localSelection, setLocalSelection] = useState<RowSelectionState>(initialRowSelection);
  const [highlight, setHighlight] = useState<string>();
  const rawSelection = controlledSelection ?? localSelection;
  const sorting = controlledSorting ?? localSorting;
  const changeSelection = onRowSelectionChange ?? setLocalSelection;
  const table = useReactTable({
    data,
    columns,
    getRowId,
    state: { sorting, rowSelection: rawSelection },
    onSortingChange: onSortingChange ?? setLocalSorting,
    onRowSelectionChange: changeSelection,
    enableRowSelection: Boolean(enableSelection),
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
  });
  const rowElements = useRef(new Map<string, HTMLElement>());
  const focused = useRef<string | undefined>(undefined);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const validIds = new Set(data.map(getRowId));
  const selected = table
    .getRowModel()
    .rows.filter((row) => rawSelection[row.id])
    .map((row) => row.original);
  // 刷新中的空占位不能抹掉选择；真实数据更新后清除已消失的实体。
  useEffect(() => {
    if (loading) return;
    const next = Object.fromEntries(
      Object.entries(rawSelection).filter(([id, checked]) => checked && validIds.has(id)),
    );
    if (Object.keys(next).length !== Object.keys(rawSelection).length) changeSelection(next);
  });
  useEffect(() => {
    if (!focusRowId) {
      focused.current = undefined;
      return;
    }
    const element = rowElements.current.get(focusRowId);
    if (loading || !element || focused.current === focusRowId) return;
    focused.current = focusRowId;
    changeSelection({});
    element.scrollIntoView?.({ block: "center", behavior: "instant" });
    element.focus({ preventScroll: true });
    setHighlight(focusRowId);
    clearTimeout(timer.current);
    timer.current = setTimeout(() => setHighlight(undefined), 2000);
  }, [focusRowId, loading, data, mobile, changeSelection]);
  useEffect(() => () => clearTimeout(timer.current), []);
  const activate = onRowActivate ?? onRowClick;
  function isInteractive(target: EventTarget | null) {
    return (
      target instanceof Element &&
      Boolean(
        target.closest(
          "button,a,input,select,textarea,[role=checkbox],[role=menuitem],[data-row-interactive]",
        ),
      )
    );
  }
  const rowProps = (id: string, original: T) => ({
    ref: (element: HTMLElement | null) => {
      if (element) rowElements.current.set(id, element);
      else rowElements.current.delete(id);
    },
    tabIndex: activate || id === focusRowId ? 0 : undefined,
    "data-state": rawSelection[id] || highlight === id ? "selected" : undefined,
    "data-highlighted": highlight === id || undefined,
    onClick: (event: React.MouseEvent) => {
      if (!isInteractive(event.target)) activate?.(original);
    },
    onKeyDown: (event: React.KeyboardEvent) => {
      if (
        event.target === event.currentTarget &&
        (event.key === "Enter" || event.key === " ") &&
        activate
      ) {
        event.preventDefault();
        activate(original);
      }
    },
  });
  const checkbox = (id: string, checked: boolean, onChange: (checked: boolean) => void) => (
    <Checkbox
      aria-label={id}
      checked={checked}
      onCheckedChange={(value) => onChange(value === true)}
      onClick={(event) => event.stopPropagation()}
    />
  );
  if (loading) return <LoadingRows />;
  if (error) return <ErrorState error={error} onRetry={onRetry} />;
  if (!data.length)
    return <>{emptyState ?? <EmptyState filtered={filtered} onClearFilters={onClearFilters} />}</>;
  return (
    <div className="min-w-0 space-y-3 text-sm leading-5">
      {mobile ? (
        <div aria-label={ariaLabel} className="space-y-3">
          {table.getRowModel().rows.map((row) => (
            <article
              key={row.id}
              {...rowProps(row.id, row.original)}
              className={cn(
                "rounded-md border border-hairline bg-canvas p-4",
                (rawSelection[row.id] || highlight === row.id) && "bg-surface-selected",
              )}
            >
              {enableSelection && (
                <div className="mb-2">
                  {checkbox(`选择行 ${row.id}`, row.getIsSelected(), (value) =>
                    row.toggleSelected(value),
                  )}
                </div>
              )}
              {renderMobileCard(row.original)}
            </article>
          ))}
        </div>
      ) : (
        <div className="max-w-full overflow-hidden rounded-md border border-hairline">
          <Table aria-label={ariaLabel} containerClassName="max-h-[min(65dvh,640px)]">
            <TableHeader>
              {table.getHeaderGroups().map((group) => (
                <TableRow key={group.id}>
                  {enableSelection && (
                    <TableHead className="sticky left-0 z-20 w-12 bg-canvas-soft">
                      {checkbox("选择全部", table.getIsAllRowsSelected(), (value) =>
                        table.toggleAllRowsSelected(value),
                      )}
                    </TableHead>
                  )}
                  {group.headers.map((header, index) => {
                    const meta = header.column.columnDef.meta;
                    const direction = header.column.getIsSorted();
                    return (
                      <TableHead
                        key={header.id}
                        aria-sort={
                          header.column.getCanSort()
                            ? direction === "asc"
                              ? "ascending"
                              : direction === "desc"
                                ? "descending"
                                : "none"
                            : undefined
                        }
                        className={cn(
                          meta?.align === "right" && "text-right",
                          index === 0 &&
                            (enableSelection
                              ? "sticky left-12 z-20 bg-canvas-soft"
                              : "sticky left-0 z-20 bg-canvas-soft"),
                          (meta?.actions || header.column.id === "actions") &&
                            "sticky right-0 z-20 bg-canvas-soft",
                        )}
                      >
                        {header.isPlaceholder ? null : header.column.getCanSort() ? (
                          <button
                            type="button"
                            className="inline-flex min-h-8 items-center gap-2 max-md:min-h-10"
                            onClick={header.column.getToggleSortingHandler()}
                          >
                            {flexRender(header.column.columnDef.header, header.getContext())}
                            {direction === "asc" ? (
                              <ArrowUp aria-hidden="true" className="size-3" />
                            ) : direction === "desc" ? (
                              <ArrowDown aria-hidden="true" className="size-3" />
                            ) : (
                              <ChevronsUpDown aria-hidden="true" className="size-3" />
                            )}
                          </button>
                        ) : (
                          flexRender(header.column.columnDef.header, header.getContext())
                        )}
                      </TableHead>
                    );
                  })}
                </TableRow>
              ))}
            </TableHeader>
            <TableBody>
              {table.getRowModel().rows.map((row) => (
                <TableRow key={row.id} {...rowProps(row.id, row.original)} className="group">
                  {enableSelection && (
                    <TableCell className="sticky left-0 z-10 bg-canvas group-hover:bg-surface-hover group-data-[state=selected]:bg-surface-selected">
                      {checkbox(`选择行 ${row.id}`, row.getIsSelected(), (value) =>
                        row.toggleSelected(value),
                      )}
                    </TableCell>
                  )}
                  {row.getVisibleCells().map((cell, index) => {
                    const meta = cell.column.columnDef.meta;
                    return (
                      <TableCell
                        key={cell.id}
                        className={cn(
                          meta?.align === "right" && "text-right",
                          meta?.numeric && "font-mono tabular",
                          index === 0 &&
                            (enableSelection
                              ? "sticky left-12 z-10 bg-canvas group-hover:bg-surface-hover group-data-[state=selected]:bg-surface-selected"
                              : "sticky left-0 z-10 bg-canvas group-hover:bg-surface-hover group-data-[state=selected]:bg-surface-selected"),
                          (meta?.actions || cell.column.id === "actions") &&
                            "sticky right-0 z-10 bg-canvas group-hover:bg-surface-hover group-data-[state=selected]:bg-surface-selected",
                        )}
                      >
                        {flexRender(cell.column.columnDef.cell, cell.getContext())}
                      </TableCell>
                    );
                  })}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      {enableSelection && selected.length > 0 && (
        <div
          role="region"
          aria-label="批量操作"
          className="sticky bottom-0 z-20 flex flex-wrap items-center gap-3 rounded-md border border-hairline bg-canvas-soft p-3 shadow-level-3"
        >
          <span className="text-body">已选 {selected.length} 项</span>
          {bulkActions?.(selected)}
          <Button type="button" variant="ghost" size="sm" onClick={() => changeSelection({})}>
            取消选择
          </Button>
        </div>
      )}
    </div>
  );
}
