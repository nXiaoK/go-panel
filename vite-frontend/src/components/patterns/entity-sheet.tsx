import { useId, useRef, useState, type FormEvent, type ReactNode } from "react";
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetDescription,
  SheetFooter,
} from "@/components/ui/sheet";
import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogCancel,
  AlertDialogAction,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
export type EntitySheetProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description?: ReactNode;
  onSubmit?: (event: FormEvent<HTMLFormElement>) => void | Promise<unknown>;
  submitText?: string;
  submitting?: boolean;
  dirty?: boolean;
  children: ReactNode;
  footerActions?: ReactNode;
  footer?: ReactNode | ((controls: { requestClose: () => void; submitting: boolean }) => ReactNode);
};
export function EntitySheet({
  open,
  onOpenChange,
  title,
  description,
  onSubmit,
  submitText = "保存修改",
  submitting = false,
  dirty = false,
  children,
  footerActions,
  footer,
}: EntitySheetProps) {
  const id = useId();
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [pending, setPending] = useState(false);
  const submittingRef = useRef(false);
  const busy = submitting || pending;
  function requestOpen(next: boolean) {
    if (next) {
      onOpenChange(true);
      return;
    }
    if (busy || submittingRef.current) return;
    if (dirty) setConfirmOpen(true);
    else onOpenChange(false);
  }
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy || submittingRef.current || !onSubmit) return;
    submittingRef.current = true;
    setPending(true);
    try {
      await onSubmit(event);
    } finally {
      submittingRef.current = false;
      setPending(false);
    }
  }
  const bodyClass = "min-h-0 flex-1 space-y-6 overflow-y-auto px-6 py-5";
  return (
    <>
      <Sheet open={open} onOpenChange={requestOpen}>
        <SheetContent className="gap-0 p-0 text-sm leading-5">
          <SheetHeader className="border-b border-hairline px-6 py-5 pr-14">
            <SheetTitle>{title}</SheetTitle>
            {description && <SheetDescription>{description}</SheetDescription>}
          </SheetHeader>
          {onSubmit ? (
            <form id={id} onSubmit={submit} aria-busy={busy} className={bodyClass}>
              {children}
            </form>
          ) : (
            <div className={bodyClass}>{children}</div>
          )}
          <SheetFooter className="border-t border-hairline bg-canvas px-6 py-4">
            {(typeof footer === "function"
              ? footer({ requestClose: () => requestOpen(false), submitting: busy })
              : footer) ?? (
              <>
                <div className="mr-auto flex flex-wrap gap-2">{footerActions}</div>
                <Button
                  type="button"
                  variant="outline"
                  disabled={busy}
                  onClick={() => requestOpen(false)}
                >
                  取消
                </Button>
                {onSubmit && (
                  <Button type="submit" form={id} loading={busy}>
                    {submitText}
                  </Button>
                )}
              </>
            )}
          </SheetFooter>
        </SheetContent>
      </Sheet>
      <AlertDialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>放弃未保存的修改？</AlertDialogTitle>
            <AlertDialogDescription>关闭后，本次修改将不会保存</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>继续编辑</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                setConfirmOpen(false);
                onOpenChange(false);
              }}
            >
              放弃修改
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}
