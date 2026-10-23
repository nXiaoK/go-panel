import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { DataTable } from "@/components/patterns/data-table";
import { FormField as Field } from "@/components/patterns/form-field";
import type { RowSelectionState, ColumnDef } from "@tanstack/react-table";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { completeFromNft, detectNftRules, detectTunnelRules } from "@/lib/api";
import { errorMessage } from "@/lib/api/client";
import { notify } from "@/lib/notify";
import type { CompleteForwardRule, NftDetection, Node, TunnelDetection } from "@/lib/types";
import { Loader2 } from "lucide-react";
import { useEffect, useMemo, useState } from "react";

type DetectionRow = (NftDetection["detected"][number] | TunnelDetection["detected"][number]) & {
  selectionId: string;
};

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
  const [portResult, setPortResult] = useState<NftDetection | null>(null);
  const [tunnelResult, setTunnelResult] = useState<TunnelDetection | null>(null);
  const [selected, setSelected] = useState<RowSelectionState>({});

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
      setSelected({});
      setNodeId(nftNodes[0]?.id ?? null);
      setInNodeId(nftNodes[0]?.id ?? null);
      setOutNodeId(nftNodes[1]?.id ?? nftNodes[0]?.id ?? null);
    }
  }, [open, nftNodes]);

  const nextFromMode = () => {
    if (nftNodes.length === 0) {
      notify.error("没有可用的 nftables 节点");
      return;
    }
    if (mode === "tunnel" && nftNodes.length < 2) {
      notify.error("隧道识别需要至少 2 个 nftables 节点");
      return;
    }
    setStep("node");
  };

  const detect = async () => {
    setDetecting(true);
    try {
      if (mode === "port") {
        if (!nodeId) {
          return notify.error("请选择节点");
        }
        const portData = await detectNftRules(nodeId);
        if (!portData?.total) {
          notify.success(`节点 ${portData?.nodeName || ""} 未发现需要补全的规则`);
          onOpenChange(false);
          return;
        }
        setPortResult(portData);
        const auto: RowSelectionState = {};
        (portData.detected || []).forEach((r, i) => {
          if (r.suggested) auto[String(i)] = true;
        });
        setSelected(auto);
        setStep("result");
      } else {
        if (!inNodeId || !outNodeId) {
          return notify.error("请选择入口 / 出口节点");
        }
        if (inNodeId === outNodeId) {
          return notify.error("入口与出口节点不能相同");
        }
        const tunnelData = await detectTunnelRules(inNodeId, outNodeId);
        if (!tunnelData?.total) {
          notify.success(`未发现需要补全的隧道规则`);
          onOpenChange(false);
          return;
        }
        setTunnelResult(tunnelData);
        const auto: RowSelectionState = {};
        (tunnelData.detected || []).forEach((r, i) => {
          if (r.suggested) auto[String(i)] = true;
        });
        setSelected(auto);
        setStep("result");
      }
    } catch (error) {
      notify.error(errorMessage(error));
    } finally {
      setDetecting(false);
    }
  };

  const complete = async () => {
    if (selectedIDs.length === 0) return notify.error("请至少选择一条规则");
    setCompleting(true);
    try {
      let rules: CompleteForwardRule[] = [];
      let targetNodeId = 0;
      if (portResult) {
        targetNodeId = portResult.nodeId;
        rules = selectedIDs.map((i) => {
          const r = portResult.detected[i];
          return {
            tunnelId: r.tunnelId,
            inPort: r.inPort,
            outPort: "outPort" in r ? r.outPort : 0,
            targetHost: r.targetHost,
            protocol: r.protocol,
            name: `NFT识别-端口${r.inPort}`,
            rawRule: r.rawRule,
          };
        });
      } else if (tunnelResult) {
        targetNodeId = tunnelResult.inNodeId;
        rules = selectedIDs.map((i) => {
          const r = tunnelResult.detected[i];
          return {
            tunnelId: r.tunnelId,
            inPort: r.inPort,
            outPort: "relayPort" in r ? r.relayPort : 0,
            targetHost: r.targetHost,
            targetPort: "targetPort" in r ? r.targetPort : 0,
            protocol: r.protocol,
            name: `NFT识别-隧道${r.inPort}`,
            rawRule: r.inRawRule,
            outRawRule: r.outRawRule,
          };
        });
      }
      const { created, failed } = await completeFromNft(targetNodeId, rules);
      if (failed === 0) notify.success(`成功补全 ${created} 条规则`);
      else notify.warning(`成功 ${created} 条，失败 ${failed} 条`);
      onOpenChange(false);
      onComplete();
    } catch (error) {
      notify.error(errorMessage(error));
    } finally {
      setCompleting(false);
    }
  };

  const detected: DetectionRow[] = (
    (mode === "port" ? portResult?.detected : tunnelResult?.detected) || []
  ).map((rule, index) => ({ ...rule, selectionId: String(index) }));
  const selectedIDs = Object.entries(selected)
    .filter(([, checked]) => checked)
    .map(([id]) => Number(id));
  const ruleAddress = (rule: DetectionRow) =>
    "inRawRule" in rule
      ? `${rule.inNodeName} :${rule.inPort} → ${rule.outNodeName} :${rule.relayPort} → ${rule.targetHost}:${rule.targetPort}`
      : `:${rule.inPort} → ${rule.targetHost}:${rule.outPort}`;
  const ruleHeading = (rule: DetectionRow) => (
    <div className="space-y-1">
      <p className="font-semibold">{rule.tunnelName || `隧道 #${rule.tunnelId}`}</p>
      <div className="flex gap-2">
        <Badge variant="outline" className="font-mono text-caption uppercase">
          {rule.protocol}
        </Badge>
        {rule.suggested && (
          <Badge variant="outline" className="text-caption">
            推荐
          </Badge>
        )}
      </div>
    </div>
  );
  const columns: ColumnDef<DetectionRow>[] = [
    { id: "tunnel", header: "隧道", cell: ({ row }) => ruleHeading(row.original) },
    {
      id: "address",
      header: "规则",
      cell: ({ row }) => (
        <p className="break-all font-mono text-caption text-body">{ruleAddress(row.original)}</p>
      ),
    },
    {
      id: "comment",
      header: "备注",
      cell: ({ row }) => (
        <p className="break-words text-caption text-mute">{row.original.comment || "—"}</p>
      ),
    },
  ];

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
            <RadioGroup
              aria-label="识别模式"
              value={mode}
              onValueChange={(v) => setMode(v === "tunnel" ? "tunnel" : "port")}
            >
              <label className="flex items-start gap-3 rounded-md border border-hairline p-3 cursor-pointer">
                <RadioGroupItem value="port" className="mt-1" />
                <div>
                  <div className="font-medium">端口转发识别</div>
                  <div className="text-xs text-mute">识别单个节点上直接转发到目标地址的规则</div>
                </div>
              </label>
              <label className="flex items-start gap-3 rounded-md border border-hairline p-3 cursor-pointer">
                <RadioGroupItem value="tunnel" className="mt-1" />
                <div>
                  <div className="font-medium">隧道转发识别</div>
                  <div className="text-xs text-mute">
                    识别两节点隧道转发配置（A → B）；三节点 A → B → C 暂不支持导入
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
          <div className="max-h-[55dvh] overflow-y-auto">
            <DataTable
              columns={columns}
              data={detected}
              getRowId={(rule) => rule.selectionId}
              enableSelection
              rowSelection={selected}
              onRowSelectionChange={setSelected}
              ariaLabel="识别到的 NFT 规则"
              renderMobileCard={(rule) => (
                <div className="space-y-2">
                  {ruleHeading(rule)}
                  <p className="break-all font-mono text-caption text-body">{ruleAddress(rule)}</p>
                  {rule.comment && <p className="text-caption text-mute">{rule.comment}</p>}
                </div>
              )}
            />
          </div>
        )}

        <DialogFooter>
          {step !== "mode" && (
            <Button
              type="button"
              variant="ghost"
              onClick={() => setStep(step === "result" ? "node" : "mode")}
            >
              上一步
            </Button>
          )}
          <Button type="button" variant="ghost" onClick={() => onOpenChange(false)}>
            取消
          </Button>
          {step === "mode" && (
            <Button type="button" onClick={nextFromMode}>
              下一步
            </Button>
          )}
          {step === "node" && (
            <Button type="button" onClick={detect} disabled={detecting}>
              {detecting && <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />} 开始识别
            </Button>
          )}
          {step === "result" && (
            <Button type="button" onClick={complete} disabled={completing}>
              {completing && <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />}
              补全所选 ({selectedIDs.length})
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
