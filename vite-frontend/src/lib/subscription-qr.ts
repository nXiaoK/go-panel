import QRCode from "qrcode";
import { useEffect, useRef, useState } from "react";

export type SubscriptionQrState = {
  value: string;
  dataUrl: string;
  error: string;
};

const emptyQrSelection = { dataUrl: "", error: "" };

export function selectSubscriptionQrState(
  open: boolean,
  currentValue: string,
  state: SubscriptionQrState,
) {
  if (!open || state.value !== currentValue) return emptyQrSelection;
  return { dataUrl: state.dataUrl, error: state.error };
}

export function createSubscriptionQrDataUrl(value: string): Promise<string> {
  return QRCode.toDataURL(value, {
    errorCorrectionLevel: "M",
    margin: 1,
    width: 256,
  });
}

export function useSubscriptionQrDataUrl(open: boolean, value: string) {
  const [state, setState] = useState<SubscriptionQrState>({
    value: "",
    dataUrl: "",
    error: "",
  });
  const generationRef = useRef(0);

  useEffect(() => {
    const generation = ++generationRef.current;
    let cancelled = false;
    setState({ value, dataUrl: "", error: "" });

    if (open && value) {
      createSubscriptionQrDataUrl(value)
        .then((nextDataUrl) => {
          if (!cancelled && generationRef.current === generation) {
            setState({ value, dataUrl: nextDataUrl, error: "" });
          }
        })
        .catch(() => {
          if (!cancelled && generationRef.current === generation) {
            setState({ value, dataUrl: "", error: "二维码生成失败，请重试" });
          }
        });
    }

    return () => {
      cancelled = true;
    };
  }, [open, value]);

  return selectSubscriptionQrState(open, value, state);
}
