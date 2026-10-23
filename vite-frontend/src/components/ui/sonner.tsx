import { useEffect, useState, type CSSProperties } from "react";
import { Toaster as Sonner } from "sonner";
import "./sonner.css";
type ToasterProps = React.ComponentProps<typeof Sonner>;
const Toaster = ({ toastOptions, style, ...props }: ToasterProps) => {
  const [mobile, setMobile] = useState(false);
  useEffect(() => {
    const media = window.matchMedia?.("(max-width: 767px)");
    if (!media) return;
    const update = () => setMobile(media.matches);
    update();
    media.addEventListener("change", update);
    return () => media.removeEventListener("change", update);
  }, []);
  return (
    <Sonner
      {...props}
      theme="dark"
      richColors={false}
      position={mobile ? "top-center" : "top-right"}
      className="toaster group"
      style={
        {
          ...style,
          "--normal-bg": "var(--canvas-soft)",
          "--normal-text": "var(--ink)",
          "--normal-border": "var(--hairline)",
        } as CSSProperties
      }
      toastOptions={{
        ...toastOptions,
        style: {
          ...toastOptions?.style,
          background: "var(--canvas-soft)",
          color: "var(--ink)",
          border: "1px solid var(--hairline)",
          boxShadow: "var(--shadow-level-3)",
        },
        classNames: {
          ...toastOptions?.classNames,
          toast: "toast text-sm leading-5",
          description: "text-body!",
          actionButton: "bg-primary! text-on-primary!",
          cancelButton: "bg-canvas! text-body!",
        },
      }}
    />
  );
};
export { Toaster };
