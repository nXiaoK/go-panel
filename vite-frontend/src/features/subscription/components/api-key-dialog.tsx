import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "@/components/ui/dialog";
import { FormField } from "@/components/patterns/form-field";
import { Input } from "@/components/ui/input";
import { useConfirm } from "@/hooks/use-confirm";
import { useSubscriptionMutation } from "../queries";
import { updateSubscriptionApiKey } from "../api";
import { notify } from "@/lib/notify";

export function SubscriptionApiKeyDialog({
  open,
  onOpenChange,
  currentKey,
  onSaved,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  currentKey: string;
  onSaved?: () => void;
}) {
  const [draft, setDraft] = useState("");
  const [visible, setVisible] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const requestConfirm = useConfirm();
  useEffect(() => {
    if (open) {
      setDraft(currentKey);
      setVisible(false);
    }
  }, [open, currentKey]);
  const save = useSubscriptionMutation("subscription.update", {
    mutationFn: (value: string) => updateSubscriptionApiKey(value),
    onSuccess: () => {
      notify.success("API Key 已更新");
      onOpenChange(false);
      onSaved?.();
    },
    onError: (error: Error) => notify.error(error.message || "更新失败"),
  });
  const busy = confirming || save.isPending;
  async function rotate(value: string) {
    if (busy) return;
    setConfirming(true);
    try {
      if (
        !(await requestConfirm({
          tone: "danger",
          title: "更换节点上报 API Key",
          description: "旧 API Key 会立即失效，已部署的上报脚本需要更新。",
          confirmText: "更换 API Key",
        }))
      )
        return;
      await save.mutateAsync(value).catch(() => undefined);
    } finally {
      setConfirming(false);
    }
  }
  const masked = draft
    ? draft.length > 8
      ? `${draft.slice(0, 4)}••••••••${draft.slice(-4)}`
      : "••••••••"
    : "尚未配置";
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!busy) onOpenChange(next);
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>节点上报 API Key</DialogTitle>
          <DialogDescription>用于脚本上报节点。请妥善保管，不要公开密钥。</DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <FormField
            label="API Key"
            htmlFor="subscription-api-key"
            hint={
              visible
                ? "保存自定义密钥前需要确认更换"
                : "默认仅显示前后 4 位；点击显示后可查看和编辑"
            }
          >
            <Input
              id="subscription-api-key"
              value={visible ? draft : masked}
              readOnly={!visible}
              onChange={(event) => setDraft(event.target.value)}
              className="font-mono"
              autoComplete="off"
              spellCheck={false}
            />
          </FormField>
          <Button variant="outline" onClick={() => setVisible(!visible)} disabled={busy}>
            {visible ? "隐藏" : "显示"}
          </Button>
        </div>
        <DialogFooter className="flex-wrap">
          <Button variant="outline" onClick={() => rotate("")} disabled={busy}>
            随机生成
          </Button>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            取消
          </Button>
          <Button
            onClick={() => rotate(draft)}
            loading={save.isPending}
            disabled={!visible || draft === currentKey || !draft.trim() || busy}
          >
            保存
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
