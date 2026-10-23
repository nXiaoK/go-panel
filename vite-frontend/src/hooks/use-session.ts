import { useSyncExternalStore } from "react";
import { isAdministrator } from "@/lib/capabilities";
import { clearSession, readSession, serverSession, subscribeSession } from "@/lib/session";

export function useSession() {
  const session = useSyncExternalStore(subscribeSession, readSession, serverSession);
  return { ...session, isAdmin: isAdministrator(session.roleId), signOut: clearSession };
}
