import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { ConfirmContext, type ConfirmOptions, type PromptOptions } from "@/hooks/use-confirm";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

type Request = {
  options: ConfirmOptions | PromptOptions;
  prompt: boolean;
  resolve: (value: boolean | string | null) => void;
};

export function ConfirmProvider({ children }: { children: ReactNode }) {
  const queue = useRef<Request[]>([]);
  const [request, setRequest] = useState<Request | null>(null);
  const [text, setText] = useState("");
  // 关闭动画结束前保留文案，避免取消时短暂出现空白标题和默认按钮。
  const [displayOptions, setDisplayOptions] = useState<ConfirmOptions | PromptOptions | null>(null);
  const enqueue = useCallback(
    (options: ConfirmOptions | PromptOptions, prompt: boolean) =>
      new Promise<boolean | string | null>((resolve) => {
        const next = { options, prompt, resolve };
        queue.current.push(next);
        if (queue.current.length === 1) {
          setText("");
          setDisplayOptions(next.options);
          setRequest(next);
        }
      }),
    [],
  );
  const finish = useCallback(
    (accepted: boolean) => {
      if (!request || queue.current[0] !== request) return;
      request.resolve(request.prompt ? (accepted ? text.trim() : null) : accepted);
      queue.current.shift();
      setText("");
      setRequest(queue.current[0] ?? null);
      if (queue.current[0]) setDisplayOptions(queue.current[0].options);
    },
    [request, text],
  );
  useEffect(
    () => () => {
      queue.current.splice(0).forEach((pending) => pending.resolve(pending.prompt ? null : false));
    },
    [],
  );
  const service = useMemo(
    () => ({
      confirm: (options: ConfirmOptions) => enqueue(options, false) as Promise<boolean>,
      prompt: (options: PromptOptions) => enqueue(options, true) as Promise<string | null>,
    }),
    [enqueue],
  );
  const options = displayOptions;
  const requireText = options && "requireText" in options ? options.requireText : undefined;
  const inputLabel = options && "inputLabel" in options ? options.inputLabel : undefined;
  const disabled =
    Boolean(requireText !== undefined && text !== requireText) ||
    Boolean(request?.prompt && !text.trim());
  return (
    <ConfirmContext.Provider value={service}>
      {children}
      <AlertDialog
        open={request !== null}
        onOpenChange={(open) => {
          if (!open) finish(false);
        }}
      >
        <AlertDialogContent onEscapeKeyDown={() => finish(false)}>
          <AlertDialogHeader>
            <AlertDialogTitle>{options?.title}</AlertDialogTitle>
            <AlertDialogDescription>{options?.description}</AlertDialogDescription>
          </AlertDialogHeader>
          {(requireText !== undefined || inputLabel) && (
            <div className="space-y-2">
              <Label htmlFor="confirm-dialog-input">
                {inputLabel || `输入「${requireText}」确认`}
              </Label>
              <Input
                id="confirm-dialog-input"
                autoComplete="off"
                value={text}
                onChange={(event) => setText(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === "Enter" && !disabled) {
                    event.preventDefault();
                    finish(true);
                  }
                }}
              />
            </div>
          )}
          <AlertDialogFooter>
            <AlertDialogCancel onClick={() => finish(false)}>取消</AlertDialogCancel>
            <AlertDialogAction
              disabled={disabled}
              className={
                options?.tone === "danger"
                  ? "bg-destructive text-destructive-foreground hover:bg-destructive/90"
                  : undefined
              }
              onClick={(event) => {
                event.preventDefault();
                if (!disabled) finish(true);
              }}
            >
              {options?.confirmText || "确认操作"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </ConfirmContext.Provider>
  );
}
