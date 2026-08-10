export function isMissingPanelAddressError(message: string | undefined | null): boolean {
  const text = String(message ?? "").toLowerCase();
  return (
    text.includes("设置ip") ||
    text.includes("面板公网地址") ||
    text.includes("panel public address")
  );
}
