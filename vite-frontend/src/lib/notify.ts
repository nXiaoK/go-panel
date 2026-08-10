import { toast } from "sonner";

import { readPreferences } from "@/lib/preferences";

/**
 * Preference-aware notifications. Success and info honor the user's notify
 * preference; warnings, errors, and security messages are never suppressed.
 */
export const notify = {
  success(message: string) {
    if (readPreferences().notify) toast.success(message);
  },
  info(message: string) {
    if (readPreferences().notify) toast.info(message);
  },
  warning(message: string) {
    toast.warning(message);
  },
  error(message: string) {
    toast.error(message);
  },
};
