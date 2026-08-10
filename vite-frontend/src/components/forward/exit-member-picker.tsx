import { Badge } from "@/components/ui/badge";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type { ForwardExitMember, ForwardExitMode, Node, Tunnel } from "@/lib/types";
import { normalizeExitMembersForMode } from "./exit-members";

export function ExitMemberPicker({
  className = "",
  mode,
  members,
  candidates,
  tunnel,
  onChange,
}: {
  className?: string;
  mode: ForwardExitMode;
  members: ForwardExitMember[];
  candidates: Node[];
  nodeMap: Map<number, Node>;
  tunnel?: Tunnel | null;
  onChange: (members: ForwardExitMember[]) => void;
}) {
  const selectedIds = new Set(members.map((m) => m.outNodeId));
  const activeId = members.find((m) => m.active)?.outNodeId ?? members[0]?.outNodeId;

  const apply = (next: ForwardExitMember[]) => {
    onChange(normalizeExitMembersForMode(mode, next, tunnel));
  };

  if (mode === "single") {
    const current = activeId || tunnel?.outNodeId || tunnel?.exitNodeId || candidates[0]?.id;
    return (
      <div className={className}>
        <Label className="text-xs text-muted-foreground">出口节点</Label>
        <Select
          value={current ? String(current) : ""}
          onValueChange={(v) => apply([{ outNodeId: Number(v), active: true, weight: 1 }])}
        >
          <SelectTrigger className="mt-1.5">
            <SelectValue placeholder="选择出口节点" />
          </SelectTrigger>
          <SelectContent>
            {candidates.map((node) => (
              <SelectItem key={node.id} value={String(node.id)}>
                {node.name} · {node.forwardMode || "node"} · {node.status === 1 ? "在线" : "离线"}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
    );
  }

  const toggleMember = (node: Node, checked: boolean) => {
    let next = members.filter((m) => m.outNodeId !== node.id);
    if (checked) {
      next = [
        ...next,
        { outNodeId: node.id, active: mode === "balance" || next.length === 0, weight: 1 },
      ];
    }
    apply(next);
  };

  const setActive = (nodeID: number) => {
    apply(members.map((m) => ({ ...m, active: m.outNodeId === nodeID })));
  };

  return (
    <div className={`space-y-2 ${className}`}>
      <div className="flex items-center justify-between">
        <Label className="text-xs text-muted-foreground">候选出口节点</Label>
        <span className="text-[11px] text-muted-foreground">{members.length} selected</span>
      </div>
      <RadioGroup
        value={activeId ? String(activeId) : ""}
        onValueChange={(v) => setActive(Number(v))}
      >
        <div className="max-h-44 space-y-1 overflow-auto rounded-md border border-border/60 bg-background">
          {candidates.map((node) => {
            const checked = selectedIds.has(node.id);
            const active = activeId === node.id;
            return (
              <div
                key={node.id}
                className="flex items-center gap-2 border-b border-border/50 px-3 py-2 last:border-b-0"
              >
                <Checkbox
                  checked={checked}
                  onCheckedChange={(v) => toggleMember(node, v === true)}
                  aria-label={`选择 ${node.name}`}
                />
                {mode === "manual" ? (
                  <RadioGroupItem
                    value={String(node.id)}
                    disabled={!checked}
                    aria-label={`当前出口 ${node.name}`}
                  />
                ) : (
                  <span className="h-4 w-4 rounded-full border border-border bg-muted" />
                )}
                <div className="min-w-0 flex-1">
                  <div className="truncate text-sm">{node.name}</div>
                  <div className="truncate font-mono text-[11px] text-muted-foreground">
                    {node.serverIp || node.ip || "-"} · {node.forwardMode || "node"}
                  </div>
                </div>
                {mode === "manual" && active && checked && (
                  <Badge variant="outline" className="border-primary/30 text-[10px] text-primary">
                    当前
                  </Badge>
                )}
              </div>
            );
          })}
          {candidates.length === 0 && (
            <div className="px-3 py-6 text-center text-sm text-muted-foreground">
              暂无可用出口节点
            </div>
          )}
        </div>
      </RadioGroup>
    </div>
  );
}
