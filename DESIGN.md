---
version: 1.1
name: go-panel-console
description: go-panel 运维控制台设计规范。继承工作区 DESIGN.md（Voltagent 风格：近黑画布、单一电光绿强调色、细线卡片、Inter + 等宽字体），并针对中文、数据密集的管理后台补充状态色、图表色、表格、表单和移动端规范。
extends: "Voltagent-Inspired-design-analysis（工作区默认 DESIGN.md，暗色画布 + 单一电光绿强调色）"

colors:
  # —— 继承工作区品牌色（值不变）——
  primary: "#00d992"          # 唯一强调色：主操作、选中指示、"在线/运行中"
  primary-soft: "#2fd6a1"     # 焦点环、幽灵按钮文字
  primary-deep: "#10b981"     # 正文中的链接
  on-primary: "#101010"       # 绿色底上的文字（10.3:1）；绿色底上禁止用白字（1.65:1）
  ink: "#f2f2f2"              # 主文字（17.0:1）
  ink-strong: "#ffffff"       # 极少数强调
  body: "#bdbdbd"             # 次要文字（10.1:1）
  mute: "#8b949e"             # 说明、表头、轴标签（6.2:1）
  hairline: "#3d3a39"         # 卡片、分隔线、表格外框（装饰性，1.69:1）
  canvas: "#101010"           # 页面、侧栏、卡片底色
  canvas-soft: "#1a1a1a"      # 输入框、代码块、表头、浮层菜单
  canvas-text-soft: "#f5f6f7" # 代码块内文字
  # —— 控制台新增（本项目扩展）——
  border-control: "#6b6664"   # 输入框/选择框/复选框/开关的边框，满足 3:1 非文本对比（canvas 3.36 / canvas-soft 3.07）
  hairline-subtle: "#262626"  # 表格行分隔、图表网格
  surface-hover: "#161616"    # 行/菜单项悬停
  surface-selected: "#0f1d18" # 选中行（primary 约 8% 叠加在 canvas 上）
  status-good: "#00d992"      # 在线 / 运行中 / 成功（与 primary 同值：品牌规定绿色承载"live"）
  status-good-bg: "#0f2a20"
  status-warning: "#fab219"   # 即将到期、可升级、部分同步、配额 > 80%
  status-warning-bg: "#2a2210"
  status-serious: "#ec835a"   # 配额 > 95%、同步落后
  status-serious-bg: "#2a1a12"
  status-critical: "#d03b3b"  # 圆点/边框/图形（3.96:1，仅用于 ≥3:1 的非文字元素）
  status-critical-text: "#f87171" # 错误文字（6.88:1）
  status-critical-bg: "#2a1414"
  status-neutral: "#8b949e"   # 已暂停、已禁用、未知
  destructive-solid: "#b91c1c" # 危险操作的实心确认按钮底色（配 on-destructive 文字 5.78:1）
  on-destructive: "#f2f2f2"   # 危险实心按钮专用文字，避免随浅色 ink 变深
  overlay: "rgba(16,16,16,0.65)" # 深色浮层遮罩，保留背景页面轮廓
  chart-series-1: "#199e70"   # 下行（已用 dataviz 校验器在 #101010 上验证）
  chart-series-2: "#3987e5"   # 上行
  chart-grid: "#262626"
  chart-axis: "#3d3a39"

# 浅色主题覆盖以下语义颜色，组件继续使用同名令牌；未设置外观时默认深色。
light-colors:
  # 白色画布搭配低饱和灰绿输入区；三级文字在悬停/选中底色上仍至少 4.5:1。
  canvas: "#ffffff"
  canvas-soft: "#f5f7f6"
  canvas-text-soft: "#1c2923"
  ink: "#1c2923"
  ink-strong: "#101c16"
  body: "#405047"
  mute: "#5c6b62"
  # 浅色强调绿加深以保证对比；实心主按钮必须使用白色 on-primary 文字。
  primary: "#087653"
  primary-soft: "#087653"
  primary-deep: "#096c50"
  on-primary: "#ffffff"
  # 装饰线不承担控件识别；控件边界在全部交互底色上至少 3:1。
  hairline: "#d4ddd7"
  border-control: "#7c8880"
  hairline-subtle: "#e5ebe7"
  surface-hover: "#edf2ef"
  surface-selected: "#e5f3ec"
  # 状态底色仅辅助识别，始终保留图标和中文文字；文字均至少 4.5:1。
  status-good: "#08734f"
  status-good-bg: "#e5f3eb"
  status-warning: "#805b08"
  status-warning-bg: "#fff4d6"
  status-serious: "#a04720"
  status-serious-bg: "#fff0e8"
  status-critical: "#c03636"
  status-critical-text: "#b52d2d"
  status-critical-bg: "#fff0f0"
  status-neutral: "#5c6b62"
  # 危险按钮使用专用白字，不复用浅色 ink，防止深字配红底。
  destructive-solid: "#b91c1c"
  on-destructive: "#ffffff"
  # 图表序列在白色/浅灰画布上至少 3:1；轴线与网格仅作装饰。
  chart-series-1: "#137e59"
  chart-series-2: "#286bc4"
  chart-grid: "#e5ebe7"
  chart-axis: "#d4ddd7"
  # 减轻浅色浮层的遮罩和阴影，保持前景清晰、背景页面仍可辨认。
  overlay: "rgba(23, 38, 31, 0.3)"

typography:
  fontSans: '"Inter Variable", Inter, "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", "Noto Sans SC", system-ui, sans-serif'
  fontMono: '"JetBrains Mono Variable", "JetBrains Mono", "SF Mono", SFMono-Regular, Menlo, Consolas, monospace'
  page-title:     { fontSize: 24px, lineHeight: 32px, fontWeight: 600, letterSpacing: -0.3px }
  section-title:  { fontSize: 16px, lineHeight: 24px, fontWeight: 600 }
  card-title:     { fontSize: 14px, lineHeight: 20px, fontWeight: 600 }
  body:           { fontSize: 14px, lineHeight: 20px, fontWeight: 400 }
  body-strong:    { fontSize: 14px, lineHeight: 20px, fontWeight: 600 }
  body-lg:        { fontSize: 16px, lineHeight: 26px, fontWeight: 400 }   # 仅登录页、空状态说明
  caption:        { fontSize: 12px, lineHeight: 16px, fontWeight: 400 }
  caption-strong: { fontSize: 12px, lineHeight: 16px, fontWeight: 500 }
  eyebrow:        { fontSize: 12px, lineHeight: 16px, fontWeight: 600, letterSpacing: 1.5px, textTransform: uppercase }  # 仅拉丁文字
  metric-lg:      { fontFamily: mono, fontSize: 28px, lineHeight: 36px, fontWeight: 550 }
  metric-md:      { fontFamily: mono, fontSize: 20px, lineHeight: 28px, fontWeight: 550 }
  code:           { fontFamily: mono, fontSize: 13px, lineHeight: 18px, fontWeight: 400 }
  code-strong:    { fontFamily: mono, fontSize: 13px, lineHeight: 18px, fontWeight: 550 }
  button:         { fontSize: 14px, lineHeight: 20px, fontWeight: 600 }

rounded:
  xs: 4px     # 代码内联、小标签
  sm: 6px     # 按钮、输入框、菜单项
  md: 8px     # 卡片、对话框、抽屉、表格外框
  pill: 9999px # 仅状态胶囊与计数徽标

spacing: { xxs: 2px, xs: 4px, sm: 8px, md: 12px, lg: 16px, xl: 20px, 2xl: 24px, 3xl: 32px, 4xl: 40px, 5xl: 48px }

layout:
  sidebar-width: 240px
  sidebar-collapsed-width: 56px
  topbar-height: 56px
  content-max-width: 1440px
  content-padding: { desktop: 24px, mobile: 16px }
  breakpoints: { mobile: "<768px", tablet: "768–1023px", desktop: "≥1024px", wide: "≥1440px" }

elevation:
  level-0: none
  level-1: "1px solid {colors.hairline}"                                             # 默认：所有卡片
  level-2: "0 0 15px rgba(92,88,85,0.2)"                                              # 可点击卡片的悬停
  level-3: "0 20px 60px rgba(0,0,0,0.7), 0 0 0 1px rgba(148,163,184,0.1) inset"      # 对话框、抽屉、命令面板
  popover: "0 8px 24px rgba(0,0,0,0.5)"                                               # 下拉菜单、提示、选择列表
---

# go-panel 控制台设计规范

## 1. 定位与继承关系

本规范继承工作区 `DESIGN.md`（Voltagent 风格），用于一个**中文、数据密集、长时间使用**的转发运维控制台。

- **原样继承：** 品牌色值；深色下的近黑单一画布；细线即层级（卡片不用阴影）；单一电光绿强调色；Inter 与等宽字体的搭配；6px 按钮和 8px 卡片圆角；状态标签使用胶囊形状。
- **针对控制台调整：** 字号整体下移一档（正文 14px，页面标题 24px）。营销站的 60px 主标题、48px 分区留白、大号眉标签都不使用。
- **本项目扩展：**
  - 状态色（工作区规范说明语义色“保留给产品内场景”，这里补齐）；
  - 经过 dataviz 校验器验证的图表色；
  - 满足 3:1 非文本对比度的控件边框；
  - 表格、表单、抽屉、移动端卡片列表等后台组件规范。
- **支持深色、浅色和跟随系统**（替代原决策 D1）。保留深色为默认；顶栏及登录页提供外观切换，偏好只保存于当前浏览器 `pref_theme`。浅色使用白色画布与深绿色强调色，所有组件共享语义令牌。系统变化和同源其他标签页的偏好变化即时同步。

## 2. 颜色使用规则

### 2.1 绿色是稀缺资源

`primary` 只用于：

- 每个视图区域至多 **1 个**主按钮；
- 侧栏当前项左侧 2px 指示条，以及当前项图标；
- “在线 / 运行中”状态圆点；
- 焦点环（`primary-soft`）；
- 选中状态：复选框、开关开启、分段控件的选中项。

不要把绿色用于以下场合：

- 大面积背景；
- 正文文字；
- 图标装饰；
- 卡片边框（选中卡片除外）；
- 图表序列（图表使用 `chart-series-*`）。

### 2.2 文字层级

| 角色 | 令牌 | 用途 |
|---|---|---|
| 主文字 | `ink` | 标题、表格主列、数值 |
| 次要文字 | `body` | 表格普通列、段落 |
| 弱化文字 | `mute` | 表头、说明、占位符、单位、时间戳 |
| 错误文字 | `status-critical-text` | 字段错误、失败消息 |

最小字号为 **12px**，不允许出现 9–11px 的文字。

### 2.3 状态语义（全站统一，组件 `StatusPill`）

状态一律以“**圆点或图标 + 文字**”的形式表达，不能只靠颜色。

| 实体与状态 | 色调 | 文案 | 图标（lucide） |
|---|---|---|---|
| 节点在线、转发运行中、隧道启用、用户正常 | good | 在线 / 运行中 / 启用 / 正常 | 圆点 |
| 节点离线 | critical | 离线 | 圆点 |
| 转发异常（status = -1）、同步失败、诊断失败 | critical | 异常 / 同步失败 / 失败 | `AlertCircle` |
| 转发已暂停、隧道或用户已禁用 | neutral | 已暂停 / 已禁用 | 空心圆点 |
| 7 天内到期、可升级、部分同步、配额 > 80% | warning | 3 天后到期 / 可升级 / 部分同步 | `Clock` / `ArrowUpCircle` |
| 配额 > 95%、同步落后 | serious | 流量即将用尽 / 待同步 | `AlertTriangle` |
| 已到期 | critical | 已到期 | `CalendarX` |
| 诊断中“外网探测”失败（advisory） | warning | 警告（仅供参考） | `Info` |

状态胶囊的样式：

- 高 22px，水平内边距 8px，`rounded.pill`；
- 背景用 `status-*-bg`，可选 1px 边框（状态色 30% 透明度）；
- 圆点 6px，用状态色；
- 文字用 `caption-strong`。good、warning、serious 的文字可以用状态色本身（两种主题对比度均 ≥ 4.5:1）；critical 的文字用 `status-critical-text`。

### 2.4 双主题与对比度

- 上方 `colors` 定义默认深色，`light-colors` 定义浅色覆盖；`on-primary` 在深色为近黑字、浅色为白字，不允许在组件中写死文字颜色。
- 危险实心按钮统一使用 `on-destructive`；阴影随主题切换。浅色 `level-2` 为 `0 2px 12px rgba(23,38,31,0.08)`，`level-3` 为 `0 20px 60px rgba(23,38,31,0.18), 0 0 0 1px rgba(23,38,31,0.04) inset`，`popover` 为 `0 8px 24px rgba(23,38,31,0.12)`。
- 新增浅色组合经 WCAG 相对亮度计算：最弱文字 `mute / surface-selected` 为 **4.92:1**，控件边界 `border-control / surface-selected` 为 **3.23:1**，状态文字与各自背景至少 **5.13:1**（critical 使用 critical-text）。图表序列与基础画布至少 **4.5:1**。`test/design-tokens.test.mjs` 同时验证两套主题。
- 外观不会修改服务器配置或账号权限；无效值回退深色。禁用浏览器存储时允许本页切换，刷新后不保证保留。初始化脚本在首屏前执行，使用同源内容哈希资源兼容严格 CSP，无内联脚本。

## 3. 字体

- **自托管**（`@fontsource-variable/inter`、`@fontsource-variable/jetbrains-mono`），只引入 latin 子集。中文字形由系统字体（PingFang SC、微软雅黑、Noto Sans SC）回退显示。**禁止**引用 Google Fonts 或任何第三方字体 CDN，因为目标用户在中国大陆。
- 等宽字体用于：IP、端口、地址、ID、版本号、命令、令牌、表格中的数值，以及 KPI 数值（品牌规定“指标用等宽”）。
- 表格中的数值列加 `font-variant-numeric: tabular-nums` 并右对齐。
- 中文不使用大写和字距（`eyebrow` 只用于拉丁文字）。页面标题不放英文眉标签，这是对现有“forwards / nodes”小标签的改正。

## 4. 布局

### 4.1 应用外壳（≥1024px）

```
┌──────────────┬───────────────────────────────────────────────────────────────┐
│ ⚡ go-panel  ▸│ 转发规则                         [⌘K 搜索资源…]   ● 正常   [AD ▾] │ 56px，底部 hairline
│ v1.4.2       ├───────────────────────────────────────────────────────────────┤
│              │  (内容区：最大 1440px，左右各 24px，顶部 24px)                     │
│ 概览          │                                                               │
│ ▍控制台       │                                                               │
│ 资源          │                                                               │
│   节点        │                                                               │
│   隧道        │                                                               │
│   转发        │                                                               │
│   限速        │                                                               │
│ 用户与订阅     │                                                               │
│   用户        │                                                               │
│   订阅        │                                                               │
│ 系统          │                                                               │
│   系统配置     │                                                               │
│ ──────────── │                                                               │
│ 面板运行 3天4时 │                                                               │
└──────────────┴───────────────────────────────────────────────────────────────┘
  240px（可折叠为 56px 图标栏，状态持久化）
```

- 侧栏底色为 `canvas`，右侧 1px `hairline`。
  - 分组标题用 `caption`，颜色为 `mute`。
  - 菜单项高 36px，圆角 `sm`；悬停背景为 `surface-hover`；当前项文字为 `ink`，左侧有 2px 的 `primary` 指示条。
- 顶栏从左到右依次为：
  - 页面标题（`section-title`），在嵌套页面上改为面包屑；
  - 搜索触发器（不是输入框，是一个按钮，点击打开命令面板）；
  - 系统状态胶囊（仅管理员可见）；
  - 外观菜单（浅色、深色、跟随系统）；
  - 用户菜单（个人资料、偏好、退出登录）。
- 普通用户的菜单只有：控制台、我的转发、账户。

### 4.2 页面结构

```
页面标题（page-title）                                     [次要操作] [主操作]
一句话说明（body，mute；可省略）
────────────────────────────────────────────────────────────── 24px
工具栏：[搜索] [筛选…]                          计数（"共 126 条 · 3 异常"）
主体：DataTable / 卡片网格 / 表单分区
```

- 列表页**不再**在顶部放统计卡片。计数和异常数放在工具栏右侧，“异常 3”是一个可以点击的筛选。
- 区块之间的垂直间距为 24px，卡片内边距为 20px（紧凑）或 24px（表单）。

### 4.3 响应式

| 宽度 | 外壳 | 列表 | 表单 |
|---|---|---|---|
| < 768px | 侧栏变为左侧抽屉（汉堡按钮）；顶栏只保留标题、搜索图标和头像 | DataTable 自动切换为卡片列表 | 抽屉全屏；底部操作条吸底 |
| 768–1023px | 侧栏默认折叠为图标栏 | 表格可以横向滚动，但首列固定 | 抽屉宽 480px |
| ≥ 1024px | 完整外壳 | 完整表格 | 抽屉宽 560px |

页面本身任何宽度下都不能出现横向滚动条，只有表格容器内部可以横向滚动。

## 5. 组件规范（关键状态）

| 组件 | 规格 |
|---|---|
| Button | 尺寸：sm 32px、md 36px、lg 40px（登录页）；圆角 `sm`；文字 `button`。主按钮：`primary` 底、`on-primary` 字，悬停时亮度 +4%。描边按钮：透明底、1px `hairline` 边框、`ink` 字，悬停背景 `surface-hover`。幽灵按钮：无边框，`body` 字，悬停 `surface-hover`。危险按钮：描边 `status-critical`、`status-critical-text` 字；确认对话框中的最终按钮用 `destructive-solid` 底、`on-destructive` 字。加载中：左侧 14px 转圈，宽度不变，禁止重复点击。禁用：40% 透明度 |
| Input / Select / Textarea | 高 36px；`canvas-soft` 底；1px `border-control` 边框；圆角 `sm`；文字 `body` 14px，颜色 `ink`；占位符 `mute`。聚焦：边框 `primary-soft`，外加 2px 焦点环（`primary-soft` 35% 透明度）。错误：边框 `status-critical`，下方显示 12px 的 `status-critical-text` 错误文字，并设置 `aria-invalid` 和 `aria-describedby`。前后缀（单位、复制按钮）放在框内 |
| FormField | label（`caption-strong`，颜色 `body`）位于控件上方，必须通过 `htmlFor` 或 `aria-labelledby` 关联控件；必填项加 `*`（屏幕阅读器读作“必填”）；说明文字 12px `mute`；错误文字替换说明文字 |
| Switch / Checkbox / Radio | 关闭时边框为 `border-control`；开启时为 `primary` 底、`on-primary` 勾或滑块；点击区域 ≥ 32px |
| SegmentedControl | 用于时间范围、视图切换：高 32px，`canvas-soft` 底，选中段为 `canvas` 底加 1px `hairline` 边框、`ink` 字 |
| Tabs | 下划线式：选中项 `ink` 字，底部 2px `primary`；未选中项 `mute`。用于系统配置分区、详情抽屉 |
| Card / Section | `canvas` 底，1px `hairline`，圆角 `md`，无阴影。可点击的卡片悬停时使用 `level-2`。卡片标题用 `card-title`，右侧放操作 |
| DataTable | 外框 1px `hairline`，圆角 `md`。表头为 `canvas-soft` 底、`caption-strong` 字、`mute` 色，高 40px，滚动时吸顶。行高 44px，行分隔线为 `hairline-subtle`，悬停 `surface-hover`，选中 `surface-selected`。数值列右对齐，使用等宽字体加 tabular。操作列固定在最右，用 `⋯` 菜单，可以有 1–2 个常用的内联图标按钮。支持排序（表头点击，显示箭头和 `aria-sort`）、多选（首列复选框，选中后出现吸底的批量操作条）、空状态、加载骨架（5 行）、错误条。小于 768px 时渲染成卡片列表 |
| StatTile（KPI） | 标签用 `caption`、`mute` 色；数值用 `metric-lg`、`ink` 色；附注一行 `caption`（例如“共 14 个”“● 2 离线”）。**没有真实环比数据时，禁止显示趋势箭头**。可点击时跳转到对应列表并带上筛选条件 |
| Meter（配额条） | 高 6px，圆角 pill；轨道 `hairline-subtle`；填充 `status-good`，≥ 80% 变为 `status-warning`，≥ 95% 变为 `status-serious`，≥ 100% 变为 `status-critical`；右侧文字为“12.3 GB / 100 GB” |
| StatusPill | 见 §2.3 |
| Sheet（抽屉） | 用于新建和编辑复杂实体（转发、隧道、节点、用户详情）。从右侧滑出，宽度见 §4.3。背景 `canvas`，阴影 `level-3`，左侧 1px `hairline`。头部包含标题和关闭按钮，内容区按分区组织，底部操作条吸底（取消、主操作） |
| Dialog / AlertDialog | 用于简单表单（不超过 4 个字段）和确认。最大宽度 480px，阴影 `level-3`。危险确认写明后果，必要时要求输入名称 |
| DropdownMenu / Popover / Tooltip | `canvas-soft` 底，1px `hairline`，阴影 `popover`；菜单项高 32px；危险项文字为 `status-critical-text`，并与其他项用分隔线隔开 |
| Toast（sonner） | `canvas-soft` 底，1px `hairline`，阴影 `level-3`；左侧图标用状态色，文字 `ink`；位置在右上角，移动端为顶部居中 |
| CodeBlock | `canvas-soft` 底，`code` 字体，右上角放复制按钮（复制后变为“已复制”并保持 2 秒）；长命令自动换行，不出现横向滚动 |
| EmptyState | 居中显示 20px 图标（`mute`）、一句话标题（`body-strong`）、一句话说明，以及一个下一步操作按钮（例如“新建转发”）。筛选结果为空时，显示“没有匹配的结果”和“清除筛选”按钮 |
| ErrorState | 在区域内显示（不用 toast）：`status-critical-bg` 底，左侧图标，加中文错误和“重试”按钮 |
| Skeleton | `hairline-subtle` 底色加 1.2 秒的微弱呼吸动画（`prefers-reduced-motion` 时为静态） |
| CommandPalette（⌘K） | 用 cmdk 实现，居中显示，宽 640px、高不超过 680px，移动端全屏，阴影 `level-3`。头部说明搜索范围，输入区用整行底部焦点线，关闭按钮独立于输入框。提供全部、节点、隧道、转发、用户、页面与操作分类；全部视图每类预览 3 条并提供查看全部，分类最多 100 条且提示缩小关键词。结果分组显示总数，选中行使用 `surface-selected` 与 `primary` 左边线，底部固定结果计数和键盘提示。支持方向键循环选择、回车打开、清空、错误重试。选中后跳转到对应页面，并用 URL 参数定位到该条目（高亮 2 秒） |

## 6. 数据可视化

- 流量趋势是**折线图**：线宽 2px，圆角端点；下方面积填充为序列色的 10% 透明度。
  - 下行用 `chart-series-1`，上行用 `chart-series-2`；
  - 这组颜色已用 dataviz 校验器在 `#101010` 上验证：色盲区分 ΔE 19.6，正常视力区分 ΔE 20.9，与背景对比均 ≥ 3:1。
- 坐标轴：
  - y 轴 4–5 个整齐刻度，自动换算单位（0、10 GB、20 GB…），文字 `caption`、`mute` 色；
  - x 轴按时间范围显示 4–6 个标签；
  - 网格线用 `chart-grid` 实线（不用虚线），基线用 `chart-axis`。
- 交互：
  - 竖向十字线吸附到最近的时间点；
  - 提示框同时列出两个序列的数值，数值是视觉重点，名称次之，名称前用短线段表示序列颜色；
  - 键盘聚焦到图表后，左右方向键移动当前时间点，提示框内容同样显示。
- 两个序列时**必须有图例**（线段 + 文字），图例中的文字颜色用 `body`，不用序列色。
- **表格视图：** 图表右上角的分段控件可以在“图表”和“表格”之间切换，表格列出每个时间点的下行、上行和合计。
- **刷新时保持画面：** 重新拉取数据时，图表以 60% 透明度保留上一次的结果，不显示骨架，也不让布局跳动。
- **几何正确：** 不得用 `preserveAspectRatio="none"` 拉伸 SVG。用 ResizeObserver 取得容器的实际宽高，按像素坐标绘制，圆点不能变形。
- 时间范围和隧道筛选放在流量区块标题下方的一行里，只作用于流量区块（区块标题写明作用范围）。全局 KPI 不受影响。

## 7. 文案

- 使用简体中文，语气简洁，句末不加句号（多句说明除外）。按钮用“动词 + 名词”，例如“新建转发”“复制命令”，不要只写“确定”。
- 单位：流量和容量用 B / KB / MB / GB / TB（1024 进制，与后端一致）；速率用 B/s… 或 Mbps（测速）；数字和单位之间加空格。
- 时间：列表中用相对时间（例如“3 分钟前”“2 天后到期”），悬停时显示绝对时间（`YYYY-MM-DD HH:mm:ss`）。
- 危险操作的确认文案要写出影响范围，例如：“删除节点后，依赖该节点的 3 条隧道和 12 条转发将停止工作。”
- 不在界面上出现英文的系统标签（system nominal、syncing、last sync 等），一律改成中文。

## 8. 无障碍与动效

- 文字对比度 ≥ 4.5:1，控件边界和图形 ≥ 3:1。本规范中的令牌组合已经满足这些要求，新增组合时必须重新计算。
- 所有交互元素都可以用键盘访问，焦点环始终可见（`:focus-visible`）。图标按钮必须有 `aria-label`。
- 动效：悬停和按下 150ms；抽屉和对话框 200ms，ease-out。`prefers-reduced-motion` 时关闭位移动画。禁止使用 `animate-ping` 这类持续脉冲。
- 触控目标：移动端 ≥ 40px。

## 9. 要做与不要做

**要做：**

- 用 1px 细线区分层级；只有浮层使用阴影。
- 每个区域最多一个绿色主按钮。
- 状态一律用“圆点或图标 + 中文文字”表达。
- 空状态、加载、错误三种状态在每个数据区域都要设计。
- 数字等宽，表格中的数值右对齐。

**不要做：**

- 不使用渐变、光晕（`shadow-glow`）、网格背景、发光文字、径向渐变背景。
- 不使用 Tailwind 原生色板（`emerald-*`、`amber-*`、`blue-*` 等），只用本规范中的令牌。
- 不写伪数据：没有依据的趋势箭头、写死的“实时”标签、把创建时间当作“最近活动”。
- 不使用原生的 `confirm`、`prompt`、`alert`。
- 不使用 12px 以下的文字。
- 列表页顶部不堆放统计卡片。
