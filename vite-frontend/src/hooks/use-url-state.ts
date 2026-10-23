import { useCallback } from "react";
import { useNavigate, useSearch } from "@tanstack/react-router";
/** 路由 validateSearch 负责验证值并保留 q/status/sort/focus 等字段；这里只合并变更，不重载页面。 */
export function useUrlState<T extends Record<string, unknown>>(defaults: T) {
  const search = useSearch({ strict: false }) as Record<string, unknown>;
  const navigate = useNavigate();
  const state = { ...defaults, ...search } as T;
  const setState = useCallback(
    (patch: Partial<T> | ((previous: T) => Partial<T>)) =>
      navigate({
        replace: true,
        search: (previous) =>
          ({
            ...previous,
            ...(typeof patch === "function" ? patch({ ...defaults, ...previous } as T) : patch),
          }) as never,
      }),
    [navigate, defaults],
  );
  return { state, setState };
}
