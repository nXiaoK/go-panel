import { createContext, useContext } from "react";
export type FieldControl = {
  id: string;
  "aria-labelledby"?: string;
  "aria-describedby"?: string;
  "aria-invalid"?: boolean;
  "aria-required"?: boolean;
};
export const FieldControlContext = createContext<FieldControl | undefined>(undefined);
export function useFieldControl() {
  return useContext(FieldControlContext);
}
