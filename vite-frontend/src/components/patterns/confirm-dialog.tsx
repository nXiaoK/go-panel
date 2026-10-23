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
  returnFocus: HTMLElement | null;
  resolve: (value: boolean | string | null) => void;
};

export function ConfirmProvider({ children }: { children: ReactNode }) {
  const queue = useRef<Request[]>([]);
  const returnFocus = useRef<HTMLElement | null>(null);
  const [request, setRequest] = useState<Request | null>(null);
  const [text, setText] = useState("");
  // 关闭动画结束前保留文案，避免取消时短暂出现空白标题和默认按钮。
  const [displayOptions, setDisplayOptions] = useState<ConfirmOptions | PromptOptions | null>(null);
  const enqueue = useCallback(
    (options: ConfirmOptions | PromptOptions, prompt: boolean) =>
      new Promise<boolean | string | null>((resolve) => {
        const next = {
          options,
          prompt,
          resolve,
          // 排队请求可能来自当前确认内的异步动作，继承最初页面控件，避免回焦到已卸载浮层。
          returnFocus:
            queue.current.length > 0
              ? queue.current[0].returnFocus
              : document.activeElement instanceof HTMLElement
                ? document.activeElement
                : null,
        };
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
      returnFocus.current = request.returnFocus;
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
        <AlertDialogContent
          onCloseAutoFocus={(event) => {
            // 命令式确认没有 Radix Trigger；取消后把焦点还给原控件，队列结束前不抢焦点。
            event.preventDefault();
            if (returnFocus.current?.isConnected)
              returnFocus.current.focus({ preventScroll: true });
          }}
        >
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
                  ? "bg-destructive-solid text-destructive-foreground hover:bg-destructive-solid/90"
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
