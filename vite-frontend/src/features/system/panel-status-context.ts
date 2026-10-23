import { createContext, useContext } from "react";
export const PanelStatusContext = createContext({
  failed: false,
  uptimeSeconds: null as number | null,
});
export const usePanelStatus = () => useContext(PanelStatusContext);
