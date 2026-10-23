import { useEffect, useRef, useState } from "react";
import { AlertTriangle } from "lucide-react";
import { EntitySheet } from "@/components/patterns/entity-sheet";
import { FormField } from "@/components/patterns/form-field";
import { ErrorState } from "@/components/patterns/feedback";
import { SegmentedControl } from "@/components/patterns/segmented-control";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Progress } from "@/components/ui/progress";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { buildPanelWsUrl, createSpeedTestId } from "@/lib/panel-ws";
import { formatMbps } from "@/lib/format";
import { formatLatencyMs, formatLossPercent } from "@/lib/speed-rate";
import type { SpeedTestResult, Tunnel } from "@/lib/types";
import { speedTestTunnel } from "../api";
import { canSpeedTest, speedDirections, validateSpeedInputs, type SpeedInputs } from "../model";

type SpeedProgress = {
  testId: string;
  mbps?: number;
  startSeconds?: number;
  endSeconds?: number;
  elapsedSeconds?: number;
  latencyMs?: number;
  lossPercent?: number;
};
function speedLabel(value?: number) {
  if (value == null || !Number.isFinite(value)) return "—";
  const rate = formatMbps(value);
  return `${rate.value} ${rate.unit}`;
}
export function SpeedTestSheet({
  tunnel,
  onOpenChange,
}: {
  tunnel?: Tunnel | null;
  onOpenChange: (open: boolean) => void;
}) {
  // 没有可测速隧道时不建立 socket，也不读取节点字段，覆盖空隧道旧回归。
  return canSpeedTest(tunnel) ? (
    <SpeedTestContent key={tunnel.id} tunnel={tunnel} onOpenChange={onOpenChange} />
  ) : null;
}
function SpeedTestContent({
  tunnel,
  onOpenChange,
}: {
  tunnel: Tunnel;
  onOpenChange: (open: boolean) => void;
}) {
  const directions = speedDirections(Boolean(tunnel.relayNodeId));
  const [direction, setDirection] = useState(directions[0].value);
  // 时长默认 10 秒、并发默认 1；输入过程保留空串，避免清空数字时被自动恢复。
  const [inputs, setInputs] = useState<SpeedInputs>({ duration: "10", parallel: "1", port: "" });
  const [errors, setErrors] = useState<ReturnType<typeof validateSpeedInputs>>({});
  const [error, setError] = useState<unknown>();
  const [running, setRunning] = useState(false);
  const runningRef = useRef(false);
  const [samples, setSamples] = useState<SpeedProgress[]>([]);
  const [result, setResult] = useState<SpeedTestResult | null>(null);
  const [elapsed, setElapsed] = useState(0);
  const [disconnected, setDisconnected] = useState(false);
  const activeId = useRef("");
  const request = useRef<AbortController | null>(null);
  const socket = useRef<WebSocket | null>(null);
  const started = useRef(0);
  useEffect(() => {
    try {
      const token = localStorage.getItem("token") || "";
      const ws = new WebSocket(buildPanelWsUrl(), token ? [`jwt.${token}`] : undefined);
      socket.current = ws;
      ws.onopen = () => setDisconnected(false);
      ws.onmessage = (event) => {
        try {
          const message = JSON.parse(event.data);
          if (
            message?.type !== "speed-test-progress" ||
            !message.data ||
            !activeId.current ||
            message.data.testId !== activeId.current
          )
            return;
          const progress = message.data as SpeedProgress;
          setSamples((previous) => [...previous, progress].slice(-6));
        } catch {
          /* 无效或其他消息不影响最终 HTTP 结果 */
        }
      };
      ws.onclose = () => {
        if (socket.current === ws) {
          socket.current = null;
          setDisconnected(true);
        }
      };
      ws.onerror = () => setDisconnected(true);
    } catch {
      setDisconnected(true);
    }
    return () => {
      activeId.current = "";
      runningRef.current = false;
      request.current?.abort();
      const ws = socket.current;
      socket.current = null;
      if (ws) {
        ws.onmessage = null;
        ws.onopen = null;
        ws.onerror = null;
        ws.onclose = null;
        ws.close();
      }
    };
  }, []);
  useEffect(() => {
    if (!running) return;
    const timer = setInterval(() => setElapsed((Date.now() - started.current) / 1000), 250);
    return () => clearInterval(timer);
  }, [running]);
  function patch(key: keyof SpeedInputs, value: string) {
    setInputs((previous) => ({ ...previous, [key]: value }));
    setErrors((previous) => ({ ...previous, [key]: undefined }));
  }
  async function run() {
    if (runningRef.current) return;
    const validation = validateSpeedInputs(inputs);
    setErrors(validation);
    if (Object.keys(validation).length) return;
    const testId = createSpeedTestId();
    activeId.current = testId;
    const controller = new AbortController();
    request.current = controller;
    setSamples([]);
    setResult(null);
    setError(undefined);
    setElapsed(0);
    started.current = Date.now();
    runningRef.current = true;
    setRunning(true);
    try {
      const response = await speedTestTunnel(
        {
          tunnelId: tunnel.id,
          testId,
          direction,
          durationSeconds: Number(inputs.duration),
          parallel: Number(inputs.parallel),
          port: inputs.port.trim() ? Number(inputs.port) : 0,
        },
        { signal: controller.signal },
      );
      if (!controller.signal.aborted && activeId.current === testId) setResult(response);
    } catch (failure) {
      if (!controller.signal.aborted && activeId.current === testId) setError(failure);
    } finally {
      if (!controller.signal.aborted && activeId.current === testId) {
        runningRef.current = false;
        setRunning(false);
        activeId.current = "";
      }
    }
  }
  const latest = samples.at(-1);
  const duration = Number(inputs.duration) || 10;
  return (
    <EntitySheet
      open
      onOpenChange={onOpenChange}
      title={`隧道测速 · ${tunnel.name}`}
      description="测试当前链路的节点间性能，测试期间会产生网络流量"
      onSubmit={() => {
        void run();
      }}
      footer={({ requestClose }) => (
        <>
          <Button type="button" variant="outline" onClick={requestClose}>
            关闭测速
          </Button>
          <Button
            type="button"
            loading={running}
            onClick={() => {
              void run();
            }}
          >
            {result ? "再测一次" : "开始测速"}
          </Button>
        </>
      )}
    >
      <div className="space-y-4">
        <div className="space-y-1.5">
          <p className="text-caption-strong text-body" id={`speed-direction-${tunnel.id}`}>
            测试方向
          </p>
          <SegmentedControl
            label="测试方向"
            value={direction}
            onChange={setDirection}
            options={directions.map((item) => ({ ...item, disabled: running }))}
          />
        </div>
        <div className="grid gap-4 sm:grid-cols-2">
          <FormField
            label="测试时长（秒）"
            required
            hint="1–120 秒，默认 10 秒"
            error={errors.duration}
          >
            <Input
              inputMode="numeric"
              value={inputs.duration}
              disabled={running}
              onChange={(event) => patch("duration", event.target.value)}
            />
          </FormField>
          <FormField
            label="并发连接"
            required
            hint="1–32 个连接，默认 1 个"
            error={errors.parallel}
          >
            <Input
              inputMode="numeric"
              value={inputs.parallel}
              disabled={running}
              onChange={(event) => patch("parallel", event.target.value)}
            />
          </FormField>
          <FormField label="iperf3 端口" hint="留空时由服务端自动分配" error={errors.port}>
            <Input
              inputMode="numeric"
              placeholder="自动"
              value={inputs.port}
              disabled={running}
              onChange={(event) => patch("port", event.target.value)}
            />
          </FormField>
        </div>
      </div>
      {error ? (
        <ErrorState
          error={error}
          onRetry={() => {
            void run();
          }}
        />
      ) : null}
      {running && disconnected && (
        <p
          role="status"
          className="rounded-md border border-status-warning bg-status-warning-bg p-3 text-caption text-status-warning"
        >
          实时进度已断开，结果将在测试结束后显示
        </p>
      )}
      {(running || samples.length > 0) && (
        <section aria-label="测速进度" className="space-y-4 rounded-md border border-hairline p-4">
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
            <Metric label="实时速率" value={speedLabel(latest?.mbps)} large />
            <Metric
              label="实时延迟"
              value={latest?.latencyMs == null ? "—" : formatLatencyMs(latest.latencyMs)}
            />
            <Metric
              label="实时丢包"
              value={latest?.lossPercent == null ? "—" : formatLossPercent(latest.lossPercent)}
            />
          </div>
          <Progress
            aria-label="测速已用时间"
            value={result ? 100 : Math.min(100, (elapsed / duration) * 100)}
          />
          <p className="font-mono text-caption text-mute">
            {Math.min(duration, elapsed).toFixed(0)} / {duration} 秒 ·{" "}
            {running ? "测试中" : "已结束"}
          </p>
          {samples.length ? (
            <div className="overflow-x-auto">
              <Table aria-label="最近六个采样点">
                <TableHeader>
                  <TableRow>
                    <TableHead>时段（秒）</TableHead>
                    <TableHead className="text-right">速率</TableHead>
                    <TableHead className="text-right">延迟</TableHead>
                    <TableHead className="text-right">丢包</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {samples.map((sample, index) => (
                    <TableRow key={index}>
                      <TableCell className="font-mono text-caption">
                        {sample.startSeconds ?? "—"}–
                        {sample.endSeconds ?? sample.elapsedSeconds ?? "—"}
                      </TableCell>
                      <TableCell className="text-right font-mono text-caption">
                        {speedLabel(sample.mbps)}
                      </TableCell>
                      <TableCell className="text-right font-mono text-caption">
                        {formatLatencyMs(sample.latencyMs)}
                      </TableCell>
                      <TableCell className="text-right font-mono text-caption">
                        {formatLossPercent(sample.lossPercent)}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          ) : (
            <p role="status" className="text-caption text-mute">
              等待节点上报测速数据
            </p>
          )}
        </section>
      )}
      {result && (
        <section aria-label="测速结果" className="space-y-4">
          <h2 className="text-section-title text-ink">测试完成</h2>
          <p className="text-caption text-body">
            {result.sourceNodeName} → {result.targetNodeName}
          </p>
          <div className="grid gap-3 sm:grid-cols-2">
            <Metric label="发送速率" value={speedLabel(result.summary.sentMbps)} large framed />
            <Metric label="接收速率" value={speedLabel(result.summary.receivedMbps)} large framed />
            <Metric label="最终延迟" value={formatLatencyMs(result.summary.latencyMs)} framed />
            <Metric
              label="最终丢包"
              value={formatLossPercent(result.summary.lossPercent)}
              warning={(result.summary.lossPercent ?? 0) > 0}
              framed
            />
            <Metric label="重传" value={String(result.summary.retransmits ?? "—")} framed />
          </div>
          <p className="break-all font-mono text-caption text-mute">
            {result.targetHost}:{result.port} · {result.durationSeconds} 秒 · {result.parallel}{" "}
            个并发连接
          </p>
        </section>
      )}
    </EntitySheet>
  );
}
function Metric({
  label,
  value,
  large,
  warning,
  framed,
}: {
  label: string;
  value: string;
  large?: boolean;
  warning?: boolean;
  framed?: boolean;
}) {
  return (
    <div className={framed ? "rounded-md border border-hairline p-4" : "min-w-0"}>
      <p className="text-caption text-mute">{label}</p>
      <p
        className={`mt-1 break-words font-mono text-ink ${large ? "text-metric-lg" : "text-metric-md"}`}
      >
        {value}
        {warning && (
          <AlertTriangle
            aria-label="检测到丢包"
            className="ml-2 inline size-4 text-status-warning"
          />
        )}
      </p>
    </div>
  );
}
