import { useState } from "react";
import { AppContent } from "@/app/app-content";
import { AlertTriangle, MoreHorizontal } from "lucide-react";
import type { ColumnDef, RowSelectionState } from "@tanstack/react-table";
import { ConfirmProvider } from "@/components/patterns/confirm-dialog";
import { useConfirm } from "@/hooks/use-confirm";
import { PageHeader } from "@/components/patterns/page-header";
import { StatTile } from "@/components/patterns/stat-tile";
import { StatusPill } from "@/components/patterns/status-pill";
import { Meter } from "@/components/patterns/meter";
import { FormField } from "@/components/patterns/form-field";
import { FormSection } from "@/components/patterns/form-section";
import { EntitySheet } from "@/components/patterns/entity-sheet";
import { DiagnosisSheet } from "@/components/patterns/diagnosis-sheet";
import { DataTable } from "@/components/patterns/data-table";
import { TableToolbar } from "@/components/patterns/table-toolbar";
import { FilterSelect } from "@/components/patterns/filter-select";
import { SegmentedControl } from "@/components/patterns/segmented-control";
import { CodeBlock } from "@/components/patterns/code-block";
import { EmptyState, ErrorState, LoadingRows } from "@/components/patterns/feedback";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Checkbox } from "@/components/ui/checkbox";
import { Switch } from "@/components/ui/switch";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import { Badge } from "@/components/ui/badge";
import { Card, CardHeader, CardTitle, CardContent } from "@/components/ui/card";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
} from "@/components/ui/dropdown-menu";
import { Tooltip, TooltipProvider, TooltipTrigger, TooltipContent } from "@/components/ui/tooltip";
import { Popover, PopoverTrigger, PopoverContent } from "@/components/ui/popover";
import { Toaster } from "@/components/ui/sonner";
import { notify as toast } from "@/lib/notify";
type PreviewRow = { id: string; name: string; flow: number };
const rows: PreviewRow[] = [
  { id: "1", name: "香港入口", flow: 120 },
  { id: "2", name: "东京出口", flow: 240 },
  { id: "3", name: "上海中继", flow: 360 },
];
const columns: ColumnDef<PreviewRow>[] = [
  { accessorKey: "name", header: "节点名称" },
  { accessorKey: "flow", header: "流量 / GB", meta: { numeric: true, align: "right" } },
  {
    id: "actions",
    header: "操作",
    cell: () => (
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="ghost" size="icon" aria-label="行操作">
            <MoreHorizontal />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuItem>查看详情</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    ),
  },
];
function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">{children}</CardContent>
    </Card>
  );
}
function Preview() {
  const [longTable, setLongTable] = useState(false);
  const [focus, setFocus] = useState<string>();
  const previewRows = longTable
    ? Array.from({ length: 50 }, (_, index) => ({
        id: String(index + 1),
        name: `测试节点 ${index + 1}`,
        flow: (index + 1) * 10,
      }))
    : rows;
  const [saving, setSaving] = useState(false);
  const [sheet, setSheet] = useState(false);
  const [dirty, setDirty] = useState(false);
  const [diagnosis, setDiagnosis] = useState(false);
  const [q, setQ] = useState("");
  const [filter, setFilter] = useState("all");
  const [view, setView] = useState("table");
  const [selection, setSelection] = useState<RowSelectionState>({});
  const confirm = useConfirm();
  return (
    <TooltipProvider>
      <main className="mx-auto max-w-[1440px] space-y-6 p-4 md:p-6">
        <PageHeader
          title="组件验收实验室 U02"
          description="仅开发环境 · 共享组件交互与视觉状态"
          actions={<Button onClick={() => setSheet(true)}>打开编辑抽屉</Button>}
        />
        <div className="grid gap-4 sm:grid-cols-3">
          <StatTile label="累计流量" value="128.5" unit="GB" hint="来自实际记录的展示格式" />
          <StatTile label="在线节点" value={3} hint={<StatusPill tone="good" label="在线" />} />
          <StatTile label="加载指标" value="—" loading />
        </div>
        <Section title="按钮与标签">
          <div className="flex flex-wrap gap-2">
            <Button>主操作</Button>
            <Button variant="outline">次要操作</Button>
            <Button variant="ghost">幽灵按钮</Button>
            <Button variant="danger">删除资源</Button>
            <Button variant="danger-solid">确认删除</Button>
            <Button variant="link">查看说明</Button>
            <Button disabled>不可用</Button>
            <Button loading>正在保存</Button>
            <Button
              loading={saving}
              onClick={() => {
                setSaving(true);
                setTimeout(() => setSaving(false), 1200);
              }}
            >
              测试加载宽度
            </Button>
            <Button size="sm" variant="outline">
              小尺寸
            </Button>
            <Badge>中性标签</Badge>
            <Badge variant="count">12</Badge>
          </div>
          <div className="flex flex-wrap gap-2">
            <StatusPill tone="good" label="运行中" />
            <StatusPill tone="warning" label="3 天后到期" />
            <StatusPill tone="serious" label="待同步" icon={AlertTriangle} />
            <StatusPill tone="critical" label="异常" icon={AlertTriangle} />
            <StatusPill tone="neutral" label="已暂停" hollow />
          </div>
        </Section>
        <Section title="表单与选择">
          <div className="grid gap-5 md:grid-cols-2">
            <FormField label="转发名称" required hint="便于识别此规则">
              <Input placeholder="输入名称" />
            </FormField>
            <FormField label="所属隧道" required>
              <Select defaultValue="a">
                <SelectTrigger>
                  <SelectValue placeholder="请选择" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="a">香港隧道</SelectItem>
                  <SelectItem value="b">东京隧道</SelectItem>
                </SelectContent>
              </Select>
            </FormField>
            <FormField label="目标地址" error="请输入有效的地址">
              <Input defaultValue="invalid" />
            </FormField>
            <FormField label="已禁用字段">
              <Input disabled placeholder="由系统管理" />
            </FormField>
            <FormField label="说明">
              <Textarea placeholder="输入可选说明" />
            </FormField>
            <div className="space-y-3">
              <label className="flex min-h-10 items-center gap-2 text-sm">
                <Checkbox defaultChecked />
                启用转发
              </label>
              <label className="flex min-h-10 items-center gap-2 text-sm">
                <Switch defaultChecked />
                自动同步
              </label>
              <RadioGroup defaultValue="a">
                <label className="flex items-center gap-2">
                  <RadioGroupItem value="a" />
                  默认出口
                </label>
                <label className="flex items-center gap-2">
                  <RadioGroupItem value="b" />
                  手动出口
                </label>
              </RadioGroup>
            </div>
          </div>
          <Tabs defaultValue="a">
            <TabsList>
              <TabsTrigger value="a">基本信息</TabsTrigger>
              <TabsTrigger value="b">高级选项</TabsTrigger>
            </TabsList>
            <TabsContent value="a">默认标签内容</TabsContent>
            <TabsContent value="b">高级标签内容</TabsContent>
          </Tabs>
        </Section>
        <Section title="数据列表">
          <div className="flex flex-wrap gap-2">
            <Button
              variant="outline"
              onClick={() => {
                setLongTable((value) => !value);
                setFocus(undefined);
              }}
            >
              切换长表
            </Button>
            <Button
              variant="outline"
              onClick={() => {
                setLongTable(true);
                setFocus("30");
              }}
            >
              定位第30行
            </Button>
          </div>
          <TableToolbar
            searchValue={q}
            onSearchChange={setQ}
            filters={
              <FilterSelect
                label="状态筛选"
                value={filter}
                onChange={setFilter}
                options={[
                  { value: "all", label: "全部状态" },
                  { value: "online", label: "在线" },
                ]}
              />
            }
            count={`共 ${previewRows.length} 条`}
            viewSwitch={
              <SegmentedControl
                label="视图"
                value={view}
                onChange={setView}
                options={[
                  { value: "table", label: "表格" },
                  { value: "list", label: "列表" },
                ]}
              />
            }
          />
          <DataTable
            columns={columns}
            focusRowId={focus}
            data={previewRows.filter((row) => row.name.includes(q))}
            getRowId={(row) => row.id}
            enableSelection
            rowSelection={selection}
            onRowSelectionChange={setSelection}
            renderMobileCard={(row) => (
              <div className="flex justify-between gap-2">
                <strong>{row.name}</strong>
                <span className="font-mono">{row.flow} GB</span>
              </div>
            )}
            bulkActions={(selected) => (
              <Button
                variant="outline"
                onClick={() => toast.success(`已选择 ${selected.length} 个节点`)}
              >
                批量操作
              </Button>
            )}
            filtered={Boolean(q)}
            onClearFilters={() => setQ("")}
          />
        </Section>
        <Section title="配额与复制">
          <div className="grid gap-5 sm:grid-cols-2">
            {[20, 80, 95, 100].map((value) => (
              <Meter key={value} value={value} max={100} formatValue={(n) => `${n} GB`} />
            ))}
          </div>
          <CodeBlock
            code={
              "go-panel --listen 127.0.0.1:6365 --database /var/lib/go-panel/panel.db # 这是一条用于验证长命令自动换行和复制状态的开发预览命令"
            }
          />
        </Section>
        <Section title="浮层与反馈">
          <div className="flex flex-wrap gap-2">
            <Button
              variant="outline"
              onClick={() =>
                void confirm({
                  title: "确认删除资源？",
                  description: "删除后无法恢复，请核对影响范围",
                  tone: "danger",
                  confirmText: "删除资源",
                })
              }
            >
              危险确认
            </Button>
            <Button variant="outline" onClick={() => setDiagnosis(true)}>
              查看诊断
            </Button>
            <Button variant="outline" onClick={() => toast.success("保存成功")}>
              显示通知
            </Button>
            <Button variant="outline" onClick={() => toast.error("保存失败，请稍后重试")}>
              显示错误通知
            </Button>
            <Popover>
              <PopoverTrigger asChild>
                <Button variant="outline">打开浮层</Button>
              </PopoverTrigger>
              <PopoverContent>浮层使用画布软色与细线边框</PopoverContent>
            </Popover>
            <Tooltip>
              <TooltipTrigger asChild>
                <Button variant="ghost">悬停说明</Button>
              </TooltipTrigger>
              <TooltipContent>键盘聚焦也可阅读提示</TooltipContent>
            </Tooltip>
          </div>
          <ErrorState
            error={new Error("网络连接失败，请检查节点状态")}
            onRetry={() => toast.info("正在重试")}
          />
          <div className="grid gap-4 md:grid-cols-2">
            <EmptyState
              title="暂无转发"
              description="添加第一条转发规则"
              action={<Button variant="outline">新建转发</Button>}
            />
            <EmptyState filtered onClearFilters={() => setQ("")} />
          </div>
          <LoadingRows />
        </Section>
        <EntitySheet
          open={sheet}
          onOpenChange={setSheet}
          title="编辑转发"
          description="抽屉内容独立滚动，修改后关闭将确认"
          dirty={dirty}
          onSubmit={async () => {
            await new Promise((resolve) => setTimeout(resolve, 700));
            setDirty(false);
            setSheet(false);
          }}
          submitText="保存转发"
        >
          <FormSection title="基本信息">
            {" "}
            <FormField label="名称" required>
              <Input onChange={() => setDirty(true)} />
            </FormField>
          </FormSection>
          {Array.from({ length: 12 }, (_, index) => (
            <FormSection key={index} title={`配置分区 ${index + 1}`}>
              <p className="text-sm leading-5 text-mute">用于验证窄屏抽屉滚动与固定操作条</p>
            </FormSection>
          ))}
        </EntitySheet>
        <DiagnosisSheet
          open={diagnosis}
          onOpenChange={setDiagnosis}
          data={{
            tunnelType: "双节点",
            timestamp: Date.now(),
            results: [
              {
                nodeId: 1,
                nodeName: "香港入口",
                targetIp: "127.0.0.1",
                description: "链路检查",
                success: true,
              },
              {
                nodeId: 1,
                nodeName: "香港入口",
                targetIp: "example.com",
                description: "外网探测",
                success: false,
                advisory: true,
                message: "探测超时，不影响链路判断",
              },
            ],
          }}
        />
        <Toaster richColors />
      </main>
    </TooltipProvider>
  );
}
// 开发专用长列表：共用实际页面容器，legacy 参数只用来复现旧滚动祖先问题。
function BatchBarPreview() {
  const legacy = new URLSearchParams(window.location.search).get("legacy") === "1";
  const [sheet, setSheet] = useState(false);
  const confirm = useConfirm();
  const items = Array.from({ length: 50 }, (_, index) => ({
    id: String(index + 1),
    name: `开发夹具条目 ${index + 1}`,
    flow: (index + 1) * 10,
  }));
  return (
    <div className="flex min-h-svh w-full">
      <aside className="hidden shrink-0 border-r border-hairline p-2 text-mute md:block md:w-14 lg:w-60">
        夹具
      </aside>
      <div className="relative flex min-w-0 flex-1 flex-col bg-canvas">
        <header className="sticky top-0 z-30 flex h-14 shrink-0 items-center border-b border-hairline bg-canvas px-4 text-sm">
          U02 批量条开发夹具
        </header>
        <AppContent className={legacy ? "overflow-x-hidden" : undefined}>
          <PageHeader
            title="长列表批量操作验收"
            description={`仅开发环境 · 50 条模拟数据 · ${legacy ? "旧滚动容器复现" : "实际应用内容容器"}`}
          />
          <div className="mt-6">
            <DataTable
              columns={columns}
              data={items}
              getRowId={(row) => row.id}
              ariaLabel="批量条验收列表"
              enableSelection
              renderMobileCard={(row) => (
                <div className="space-y-3">
                  <strong>{row.name}</strong>
                  <p className="text-body">手机长卡片 · 用于验证选中后立即可操作及滚动吸底</p>
                  <p className="font-mono">127.0.0.1:{6400 + Number(row.id)}</p>
                  <p className="text-mute">条目 {row.id} / 50</p>
                </div>
              )}
              bulkActions={() => (
                <>
                  <Button variant="outline" onClick={() => setSheet(true)}>
                    打开编辑抽屉
                  </Button>
                  <Button
                    variant="danger"
                    onClick={() =>
                      void confirm({
                        title: "批量删除开发夹具？",
                        description: "仅验证浮层，未连接业务接口，不会删除数据。",
                        confirmText: "关闭确认",
                      })
                    }
                  >
                    打开批量确认
                  </Button>
                </>
              )}
            />
          </div>
          <section className="mt-6 rounded-md border border-hairline p-5">
            <h2 className="text-sm font-semibold">列表后的独立区域</h2>
            <p className="mt-2 text-sm text-mute">批量条应随自身列表结束，不覆盖后续内容。</p>
          </section>
          <EntitySheet
            open={sheet}
            onOpenChange={setSheet}
            title="批量条浮层验收"
            description="仅开发夹具，不提交业务数据"
            onSubmit={() => setSheet(false)}
            submitText="关闭抽屉"
          >
            <FormField label="夹具名称">
              <Input defaultValue="批量操作测试" />
            </FormField>
          </EntitySheet>
        </AppContent>
      </div>
    </div>
  );
}
export function ComponentPreview() {
  const batch = new URLSearchParams(window.location.search).get("batch") === "1";
  return <ConfirmProvider>{batch ? <BatchBarPreview /> : <Preview />}</ConfirmProvider>;
}
