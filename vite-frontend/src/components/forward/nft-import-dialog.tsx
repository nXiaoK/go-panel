import { useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import { Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Label } from "@/components/ui/label";
import { Checkbox } from "@/components/ui/checkbox";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { ScrollArea } from "@/components/ui/scroll-area";
import { completeFromNft, detectNftRules, detectTunnelRules } from "@/lib/api";
import type { Node } from "@/lib/types";

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1.5">
      <Label className="text-xs text-muted-foreground">{label}</Label>
      {children}
    </div>
  );
}

export function NftDetectDialog({
  open,
  onOpenChange,
  nodes,
  onComplete,
}: {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  nodes: Node[];
  onComplete: () => void;
}) {
  const [step, setStep] = useState<"mode" | "node" | "result">("mode");
  const [mode, setMode] = useState<"port" | "tunnel">("port");
  const [nodeId, setNodeId] = useState<number | null>(null);
  const [inNodeId, setInNodeId] = useState<number | null>(null);
  const [outNodeId, setOutNodeId] = useState<number | null>(null);
  const [detecting, setDetecting] = useState(false);
  const [completing, setCompleting] = useState(false);
  const [portResult, setPortResult] = useState<any>(null);
  const [tunnelResult, setTunnelResult] = useState<any>(null);
  const [selected, setSelected] = useState<Set<number>>(new Set());

  const nftNodes = useMemo(
    () => nodes.filter((n) => n.forwardMode === "nftables" && n.status === 1),
    [nodes],
  );

  useEffect(() => {
    if (open) {
      setStep("mode");
      setMode("port");
      setPortResult(null);
      setTunnelResult(null);
      setSelected(new Set());
      setNodeId(nftNodes[0]?.id ?? null);
      setInNodeId(nftNodes[0]?.id ?? null);
      setOutNodeId(nftNodes[1]?.id ?? nftNodes[0]?.id ?? null);
    }
  }, [open, nftNodes]);

  const nextFromMode = () => {
    if (nftNodes.length === 0) {
      toast.error("没有可用的 nftables 节点");
      return;
    }
    if (mode === "tunnel" && nftNodes.length < 2) {
      toast.error("隧道识别需要至少 2 个 nftables 节点");
      return;
    }
    setStep("node");
  };

  const detect = async () => {
    setDetecting(true);
    if (mode === "port") {
      if (!nodeId) {
        setDetecting(false);
        return toast.error("请选择节点");
      }
      const res = await detectNftRules(nodeId);
      setDetecting(false);
      if (res.code !== 0) return toast.error(res.msg || "识别失败");
      const portData = res.data as any;
      if (!portData?.total) {
        toast.success(`节点 ${portData?.nodeName || ""} 未发现需要补全的规则`);
        onOpenChange(false);
        return;
      }
      setPortResult(portData);
      const auto = new Set<number>();
      (portData.detected || []).forEach((r: any, i: number) => r.suggested && auto.add(i));
      setSelected(auto);
      setStep("result");
    } else {
      if (!inNodeId || !outNodeId) {
        setDetecting(false);
        return toast.error("请选择入口 / 出口节点");
      }
      if (inNodeId === outNodeId) {
        setDetecting(false);
        return toast.error("入口与出口节点不能相同");
      }
      const res = await detectTunnelRules(inNodeId, outNodeId);
      setDetecting(false);
      if (res.code !== 0) return toast.error(res.msg || "识别失败");
      const tunnelData = res.data as any;
      if (!tunnelData?.total) {
        toast.success(`未发现需要补全的隧道规则`);
        onOpenChange(false);
        return;
      }
      setTunnelResult(tunnelData);
      const auto = new Set<number>();
      (tunnelData.detected || []).forEach((r: any, i: number) => r.suggested && auto.add(i));
      setSelected(auto);
      setStep("result");
    }
  };

  const toggle = (i: number) => {
    setSelected((prev) => {
      const n = new Set(prev);
      if (n.has(i)) n.delete(i);
      else n.add(i);
      return n;
    });
  };

  const complete = async () => {
    if (selected.size === 0) return toast.error("请至少选择一条规则");
    setCompleting(true);
    let rules: any[] = [];
    let targetNodeId = 0;
    if (portResult) {
      targetNodeId = portResult.nodeId;
      rules = Array.from(selected).map((i) => {
        const r = portResult.detected[i];
        return {
          tunnelId: r.tunnelId,
          inPort: r.inPort,
          outPort: r.outPort,
          targetHost: r.targetHost,
          protocol: r.protocol,
          name: `NFT识别-端口${r.inPort}`,
          rawRule: r.rawRule,
        };
      });
    } else if (tunnelResult) {
      targetNodeId = tunnelResult.inNodeId;
      rules = Array.from(selected).map((i) => {
        const r = tunnelResult.detected[i];
        return {
          tunnelId: r.tunnelId,
          inPort: r.inPort,
          outPort: r.relayPort,
          targetHost: r.targetHost,
          targetPort: r.targetPort,
          protocol: r.protocol,
          name: `NFT识别-隧道${r.inPort}`,
          rawRule: r.inRawRule,
          outRawRule: r.outRawRule,
        };
      });
    }
    const res = await completeFromNft(targetNodeId, rules);
    setCompleting(false);
    if (res.code !== 0) return toast.error(res.msg || "补全失败");
    const { created = 0, failed = 0 } = (res.data as any) || {};
    if (failed === 0) toast.success(`成功补全 ${created} 条规则`);
    else toast.warning(`成功 ${created} 条，失败 ${failed} 条`);
    onOpenChange(false);
    onComplete();
  };

  const detected: any[] = (mode === "port" ? portResult?.detected : tunnelResult?.detected) || [];

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>
            识别 NFT 规则
            {step === "result" && detected.length ? ` · ${detected.length} 条` : ""}
          </DialogTitle>
        </DialogHeader>

        {step === "mode" && (
          <div className="space-y-3">
            <RadioGroup value={mode} onValueChange={(v) => setMode(v as any)}>
              <label className="flex items-start gap-3 rounded-md border border-border/60 p-3 cursor-pointer">
                <RadioGroupItem value="port" className="mt-1" />
                <div>
                  <div className="font-medium">端口转发识别</div>
                  <div className="text-xs text-muted-foreground">
                    识别单个节点上直接转发到目标地址的规则
                  </div>
                </div>
              </label>
              <label className="flex items-start gap-3 rounded-md border border-border/60 p-3 cursor-pointer">
                <RadioGroupItem value="tunnel" className="mt-1" />
                <div>
                  <div className="font-medium">隧道转发识别</div>
                  <div className="text-xs text-muted-foreground">
                    识别通过中转节点的隧道转发配置（选择入口与出口节点）
                  </div>
                </div>
              </label>
            </RadioGroup>
          </div>
        )}

        {step === "node" && mode === "port" && (
          <div className="space-y-3">
            <Field label="节点">
              <Select
                value={nodeId ? String(nodeId) : ""}
                onValueChange={(v) => setNodeId(Number(v))}
              >
                <SelectTrigger>
                  <SelectValue placeholder="选择节点" />
                </SelectTrigger>
                <SelectContent>
                  {nftNodes.map((n) => (
                    <SelectItem key={n.id} value={String(n.id)}>
                      {n.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
          </div>
        )}

        {step === "node" && mode === "tunnel" && (
          <div className="space-y-3">
            <Field label="入口节点">
              <Select
                value={inNodeId ? String(inNodeId) : ""}
                onValueChange={(v) => setInNodeId(Number(v))}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {nftNodes.map((n) => (
                    <SelectItem key={n.id} value={String(n.id)}>
                      {n.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <Field label="出口节点">
              <Select
                value={outNodeId ? String(outNodeId) : ""}
                onValueChange={(v) => setOutNodeId(Number(v))}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {nftNodes.map((n) => (
                    <SelectItem key={n.id} value={String(n.id)}>
                      {n.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
          </div>
        )}

        {step === "result" && (
          <ScrollArea className="max-h-[55vh]">
            <div className="space-y-2">
              {detected.map((r, i) => (
                <label
                  key={i}
                  className="flex items-start gap-3 rounded-md border border-border/60 p-3 cursor-pointer"
                >
                  <Checkbox
                    checked={selected.has(i)}
                    onCheckedChange={() => toggle(i)}
                    className="mt-1"
                  />
                  <div className="flex-1 space-y-1">
                    <div className="flex items-center gap-2">
                      <span className="font-medium text-sm">
                        {r.tunnelName || `隧道 #${r.tunnelId}`}
                      </span>
                      <Badge variant="outline" className="font-mono text-[10px] uppercase">
                        {r.protocol}
                      </Badge>
                      {r.suggested && (
                        <Badge variant="default" className="text-[10px]">
                          推荐
                        </Badge>
                      )}
                    </div>
                    <div className="font-mono text-[11px] text-muted-foreground">
                      {mode === "port"
                        ? `:${r.inPort} → ${r.targetHost}:${r.outPort}`
                        : `${r.inNodeName} :${r.inPort} → ${r.outNodeName} :${r.relayPort} → ${r.targetHost}:${r.targetPort}`}
                    </div>
                    {r.comment && (
                      <div className="text-[11px] text-muted-foreground">{r.comment}</div>
                    )}
                  </div>
                </label>
              ))}
            </div>
          </ScrollArea>
        )}

        <DialogFooter>
          {step !== "mode" && (
            <Button variant="ghost" onClick={() => setStep(step === "result" ? "node" : "mode")}>
              上一步
            </Button>
          )}
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            取消
          </Button>
          {step === "mode" && <Button onClick={nextFromMode}>下一步</Button>}
          {step === "node" && (
            <Button onClick={detect} disabled={detecting} className="shadow-glow">
              {detecting && <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />} 开始识别
            </Button>
          )}
          {step === "result" && (
            <Button onClick={complete} disabled={completing} className="shadow-glow">
              {completing && <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />}
              补全所选 ({selected.size})
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
