import { createContext, useContext } from "react";

export type ConfirmOptions = {
  title: string;
  description: string;
  confirmText?: string;
  tone?: "default" | "danger";
  requireText?: string;
};
export type PromptOptions = Omit<ConfirmOptions, "requireText"> & { inputLabel: string };
export type ConfirmService = {
  confirm: (options: ConfirmOptions) => Promise<boolean>;
  prompt: (options: PromptOptions) => Promise<string | null>;
};
export const ConfirmContext = createContext<ConfirmService | null>(null);
function useConfirmService() {
  const service = useContext(ConfirmContext);
  if (!service) throw new Error("确认对话框需要 ConfirmProvider");
  return service;
}
export function useConfirm() {
  return useConfirmService().confirm;
}
export function usePrompt() {
  return useConfirmService().prompt;
}
