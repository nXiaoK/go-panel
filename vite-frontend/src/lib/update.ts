const DISMISSED_UPDATE_KEY = "dismissed_update_version";

type UpdateStorage = Pick<Storage, "getItem" | "setItem">;

function browserStorage(): UpdateStorage | null {
  return typeof window === "undefined" ? null : window.localStorage;
}

export function displayVersion(version?: string, commit?: string): string {
  const normalized = String(version || "dev").trim() || "dev";
  if (normalized !== "dev") return normalized;
  const shortCommit = String(commit || "")
    .trim()
    .slice(0, 7);
  return shortCommit && shortCommit !== "unknown" ? `dev-${shortCommit}` : "dev";
}

export function shouldPromptForUpdate(
  latestVersion: string | undefined,
  storage: UpdateStorage | null = browserStorage(),
): boolean {
  const latest = String(latestVersion || "").trim();
  if (!latest) return false;
  return !storage || storage.getItem(DISMISSED_UPDATE_KEY) !== latest;
}

export function dismissUpdate(
  latestVersion: string | undefined,
  storage: UpdateStorage | null = browserStorage(),
): void {
  const latest = String(latestVersion || "").trim();
  if (latest && storage) storage.setItem(DISMISSED_UPDATE_KEY, latest);
}

export const MANUAL_UPDATE_COMMAND = "docker compose pull && docker compose up -d";
