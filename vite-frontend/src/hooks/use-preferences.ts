import { useEffect, useState } from "react";

import { readPreferences, subscribePreferences, type Preferences } from "@/lib/preferences";

/** usePreferences returns the live local preferences, updating on change. */
export function usePreferences(): Preferences {
  const [prefs, setPrefs] = useState<Preferences>(readPreferences);
  useEffect(() => subscribePreferences(() => setPrefs(readPreferences())), []);
  return prefs;
}
