import { useSyncExternalStore } from "react";
import { getResolvedTheme, getThemePreference, subscribeTheme } from "@/lib/theme";

export function useTheme() {
  const preference = useSyncExternalStore(
    subscribeTheme,
    getThemePreference,
    () => "dark" as const,
  );
  const resolved = useSyncExternalStore(subscribeTheme, getResolvedTheme, () => "dark" as const);
  return { preference, resolved };
}
