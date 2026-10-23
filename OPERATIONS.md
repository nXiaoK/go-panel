# 部署与运维指南

[返回项目首页](README.md) · [部署配置模板](.env.example) · [版本发布记录](https://github.com/nXiaoK/go-panel/releases)

本文保留完整的部署、升级、备份、节点兼容性与安全配置说明。首次使用可先按照 [README 快速开始](README.md#快速开始) 部署，再根据需要查看以下章节。

- [发布通道](#发布通道)
- [Docker Compose 与 Docker Run](#docker-compose-快速开始)
- [升级、回滚与一键更新](#升级与回滚)
- [数据备份](#数据备份)
- [节点程序与版本兼容](#节点程序)
- [订阅节点名称](#订阅节点名称)
- [环境变量、安全与诊断](#配置)
- [历史版本与接口兼容](#历史版本与兼容)

## 发布通道

| 通道 | 镜像标签 | 用途 |
|---|---|---|
| 稳定版 | `latest`、`vX.Y.Z`、`vX.Y`、`vX` | 由 `v*.*.*` Git Tag 发布，推荐生产环境使用 |
| 开发版 | `edge` | 每次 `main` Push 测试通过后发布，可能包含尚未发布的变更 |
| 不可变构建 | `sha-xxxxxxx` | 精确定位某次 Git Commit，适合审计和回滚 |

镜像地址：`ghcr.io/nxiaok/go-panel`，支持 `linux/amd64` 与 `linux/arm64`。

## Docker Compose 快速开始

需要 Docker Engine 24+ 和 Docker Compose v2。整个过程只下载 GitHub 上的配置与 GHCR 镜像，不在服务器编译源码。

```bash
mkdir -p go-panel && cd go-panel
curl -fsSLO https://raw.githubusercontent.com/nXiaoK/go-panel/main/compose.yml
curl -fsSL https://raw.githubusercontent.com/nXiaoK/go-panel/main/.env.example -o .env
```

编辑 `.env`，至少替换以下内容：

- `JWT_SECRET`：执行 `openssl rand -hex 32` 生成。
- `ADMIN_PASSWORD`：首次创建管理员所需的唯一高强度密码。
- `CORS_ALLOW_ORIGIN`：浏览器访问面板所用的完整来源，例如 `https://panel.example.com`。

启动并检查状态：

```bash
docker compose up -d
docker compose ps
docker compose logs -f panel
```

若直接访问 `http://服务器地址:6365`，请把 `CORS_ALLOW_ORIGIN` 设置为该实际来源（含协议和端口）；反向代理时则填写实际 HTTPS 域名。使用默认用户名 `admin_user`（或自定义 `ADMIN_USERNAME`）和首次设置的 `ADMIN_PASSWORD` 登录。生产环境建议使用 HTTPS 反向代理，并将 `.env` 中的 `PANEL_BIND` 改成 `127.0.0.1`，避免 6365 端口直接暴露公网。HSTS 由 HTTPS 反向代理按实际域名和证书配置；面板自身不发送 HSTS，以保留 HTTP 内网访问和代理终止 HTTPS 的兼容性。

确认管理员已创建并完成密码轮换后，可以清空 `.env` 中的 `ADMIN_PASSWORD` 再执行 `docker compose up -d`。`JWT_SECRET` 必须长期保留；修改它会使所有现有登录令牌失效，也可能使已加密保存的 R2 凭据无法读取。

### Docker Run

Compose 是推荐方式；如只需要单容器，可以直接运行公开镜像。下面使用本机 HTTPS 反向代理：端口只绑定到 `127.0.0.1`，`CORS_ALLOW_ORIGIN` 必须换成代理对外提供的实际来源。命名卷持久化数据库；`JWT_SECRET` 长期保留，`ADMIN_PASSWORD` 只用于新库初始化。

```bash
docker volume create go-panel-data
docker run -d \
  --name go-panel \
  --restart unless-stopped \
  --pull always \
  -p 127.0.0.1:6365:6365 \
  -v go-panel-data:/app/data \
  -e ENVIRONMENT=production \
  -e JWT_SECRET='替换为至少32字节随机密钥' \
  -e ADMIN_PASSWORD='替换为唯一高强度密码' \
  -e CORS_ALLOW_ORIGIN='https://panel.example.com' \
  ghcr.io/nxiaok/go-panel:latest
```

## 升级与回滚

先通过面板下载一致性数据库快照，并阅读目标版本的 Release Notes。

稳定版手动升级只需要拉取镜像并重建容器：

```bash
docker compose pull
docker compose up -d
```

升级不会删除 `panel_data` 卷。不要执行 `docker compose down -v`，其中的 `-v` 会永久删除数据库和本地更新备份。

如需回滚，将 `.env` 中的 `GO_PANEL_VERSION` 改为升级前使用的完整版本标签，然后再次运行上述两条命令。若新版本已经执行不兼容的数据迁移，还必须同时恢复升级前的数据库快照。

### 可选：界面一键更新

主面板不会直接挂载 Docker Socket。需要界面“立即更新”按钮时，再下载并启用独立更新扩展：

```bash
curl -fsSLO https://raw.githubusercontent.com/nXiaoK/go-panel/main/compose.update.yml
printf '\nUPDATE_TRIGGER_TOKEN=%s\n' "$(openssl rand -hex 32)" >> .env
docker compose -f compose.yml -f compose.update.yml up -d
```

更新流程会先把经过完整性校验的 SQLite 快照保存到数据卷的 `backups/`，再由受标签和 Token 限制的 Watchtower 侧车拉取并替换面板容器。Docker Socket 具备接近宿主机 root 的控制能力，只应在可信服务器上启用该扩展。

一键更新要求 `.env` 中的 `GO_PANEL_VERSION=latest`；如果固定为 `vX.Y.Z` 等完整版本标签，Watchtower 只会重新检查该固定镜像，不会跨版本升级。一键更新只替换镜像，不能自动修改 Compose 或环境变量。Release Notes 标注需要部署配置变更时，请使用手动升级流程。

## 数据备份

- 管理界面可以下载通过 `VACUUM INTO` 创建且经过 `PRAGMA integrity_check` 校验的 SQLite 快照。
- 系统配置支持 Cloudflare R2 每日自动备份、连接测试、立即上传和远端保留数量。
- 点击一键更新前会在数据卷内保留本地快照，默认保留最近 5 份。

不要直接复制运行中的主数据库文件。SQLite WAL 可能尚未合并，仅复制 `flux-panel.db` 不能保证得到一致备份。该历史文件名为兼容旧数据卷而保留。

## 节点程序

正式镜像和 GitHub Release 都包含以下 amd64/arm64 产物：

- `gost`
- `nft_agent`
- `nft_rule_payload`
- `nft_flow_reporter`

节点仍从自己的面板地址 `/api/v1/node/assets/<filename>` 下载程序，因此可以复用面板的 HTTPS、访问路径和节点认证；实际二进制由 GitHub Actions 构建并随镜像发布，不依赖服务器上的本地文件。

节点安装和升级默认要求 HTTPS。公网 HTTP 可能泄露节点密钥并允许程序被篡改，`ALLOW_INSECURE_NODE_DOWNLOADS=true` 只应在明确隔离的临时升级窗口使用。

中国大陆节点安装 `gost` 或 nftables Agent 时不会直接访问 GitHub：脚本和节点程序均从面板地址下载。订阅服务器脚本安装 Xray、sing-box 等上游组件时仍需要 GitHub；可在“系统配置 → GitHub 下载代理”填写可信的 HTTPS 全链接代理前缀。该代理不会修改 APT/DNF/APK 软件源，系统包下载仍应使用服务器所在地区可达且可信的发行版镜像。下载代理能够替换可执行文件，禁止使用来源不明的公共服务。

nftables 模式安装、远程组件升级和规则刷新都会写入 `/etc/sysctl.d/99-flux-nftables-forwarding.conf` 并立即启用 `net.ipv4.ip_forward=1`；否则 DNAT 规则虽然存在，数据包也不会进入 `forward` 链。卸载只删除这份持久配置，不会强制把运行时值改回 `0`，以免中断同机 Docker、VPN 或其他路由服务。

## 订阅节点名称

在“编辑节点”中修改名称并保存后，名称会持久保留；后续节点上报仍更新地址、端口和凭据，但不会覆盖手动名称。仅编辑其他字段时，未改名的节点继续按上报信息自动命名。升级时自动增加内部 `proxy_node.name_customized` 标记，旧记录默认不锁定名称，不改变已有连接信息；此前已被覆盖的名称需要重新填写并保存。

同服务商、地区、协议的节点可能有相同名称。Surge、Clash 和 v2rayN 订阅导出时会为重名项附加节点 ID（例如 `PrimeSecurity-JP-Snell-12`），若后缀仍有冲突则追加序号；Clash 策略组同步引用导出名称，sing-box 沿用自身的标签去重。此处理仅影响导出副本，不修改面板中的名称。更新面板后需刷新客户端订阅；客户端以前按旧名称记住的选择可能需要重新选择。

## 配置

完整、带风险说明的模板见 [`.env.example`](.env.example)。主要变量如下：

| 变量 | 默认值 | 说明 |
|---|---|---|
| `JWT_SECRET` | 无 | 远程部署必填，至少 32 字节；修改会使 JWT 和 R2 凭据受影响 |
| `JWT_TTL` | `2160h`（90 天） | 新令牌有效期，使用 Go duration 格式，范围 `1h`–`2160h`，非法或越界值拒绝启动。缩短会增加重新登录频率；已签发令牌仍按原 `exp` 失效。立即让全部令牌失效需轮换 `JWT_SECRET`，同时会影响已有 R2 凭据解密，须重新配置 |
| `ADMIN_USERNAME` | `admin_user` | 首次创建管理员使用的用户名 |
| `ADMIN_PASSWORD` | 空 | 新数据库远程启动时必填；管理员安全建立后可清空 |
| `CORS_ALLOW_ORIGIN` | 无 | 生产模式必填，多个明确来源用逗号分隔 |
| `CSP_MODE` | `report-only` | HTML 文档的内容安全策略；仅允许 `report-only`（浏览器报告违规）、`enforce`（强制拦截）、`off`（关闭 CSP 纵深防护），非法值拒绝启动。先在实际部署环境观察违规报告，确认兼容后显式改为 `enforce`；API、订阅及 JS/CSS 不发送该策略 |
| `PANEL_BIND` | `0.0.0.0` | 宿主机监听地址，反向代理部署建议 `127.0.0.1` |
| `PANEL_PORT` | `6365` | 宿主机映射端口 |
| `GO_PANEL_VERSION` | `latest` | 可固定为具体稳定版本以便回滚 |
| `TRUSTED_PROXIES` | 空 | 可信反向代理 CIDR；空值不信任转发头 |
| `UPDATE_CHECK_ENABLED` | `true` | 是否检查 GitHub 最新稳定 Release |
| `UPDATE_CHECK_INTERVAL` | `6h` | 成功检查结果缓存，最小 5 分钟、最大 7 天 |
| `ALLOW_INSECURE_NODE_DOWNLOADS` | `false` | 临时允许 HTTP 节点下载，存在泄密和篡改风险 |
| `ALLOW_LEGACY_NFT_REPORTS` | `false` | 临时兼容旧上报，缺少完整幂等保障 |
| `VITE_DEV_API_TARGET` | `http://127.0.0.1:6365` | 仅 Vite 本地开发代理的后端目标；生产容器不使用，不能指向不可信服务 |

HTML 内容安全策略只允许同源脚本、字体、API 和 WebSocket，禁止内联脚本及被其他页面嵌入。Radix/sonner 所需的内联样式和二维码的 `data:`/`blob:` 图片仍允许。首次部署保留 `CSP_MODE=report-only`，在实际 HTTPS 域名、反向代理及所用浏览器中检查控制台、节点实时指标、订阅二维码与备份下载；确认所用功能和浏览器均无违规后再设为 `enforce` 并重建容器。`off` 只应用于明确的临时排障，排障后恢复策略；基础安全头不受此开关影响。

面板在 `POST /api/v1/csp-report` 提供同源 CSP 违规报告端点，HTML 响应通过 `report-uri` 与 `report-to`（`Reporting-Endpoints`）指向它；`report-only` 与 `enforce` 都会上报，`off` 不发送。端点无需登录，按来源 IP 每分钟 20 次、全站每分钟 200 次限流，请求体上限 16 KB，只把指令、处置类型及被拦截资源和页面的来源（协议、主机、端口）写入日志 `CSP 违规报告：`，不入库；URL 路径、查询串、片段、用户信息及 data/blob 内容均不记录。匿名报告可能被伪造或触发限流，日志缺失不能单独证明无违规。观察期用 `docker compose logs panel | grep "CSP 违规报告"` 检查；`report-only` 不会拦截资源，Playwright WebKit 也不能替代原生 Safari 验收。

当前版本默认仍为 `report-only`。在正式域名、代理和实际浏览器中确认功能正常且无违规后，可在 `.env` 中显式设置 `CSP_MODE=enforce` 并重建容器；如需继续观察，保留 `CSP_MODE=report-only`。

诊断按节点分组：同一次诊断对同一节点串行探测，不同节点最多 4 路并发，结果保持原始顺序。HTTP 写期限按每条命令 10 秒回包等待加 15 秒余量计算，上限 5 分钟；预计超过预算的诊断在下发前整次拒绝（当前单节点最多 28 个探测目标），已有转发配置不受影响。浏览器为诊断单独预留 305 秒，部署反向代理时也需为诊断路由配置足够的响应等待时间。

## 历史版本与兼容

以下记录用于已有部署升级时核对行为；新部署应优先使用当前发布的面板与配套节点组件。

### 接口与会话

版本与运行状态兼容说明：`/api/v1/system/version` 和 `/api/v1/system/update/check` 仅允许管理员调用，普通用户返回业务 `code: 403`，界面不再显示精确版本或升级提示。所有已完成初始改密的登录用户仍可调用 `/api/v1/system/status`；该接口仅返回进程启动时间 `startedAt`（毫秒）和进程运行秒数 `uptimeSeconds`，重启后重新计时，不再使用最早数据库记录时间。上述接口调整不迁移数据库，依赖旧接口的外部客户端需按角色调整调用。

会话响应兼容说明：账号禁用统一返回业务 `code: 4031`，必须先修改初始密码返回 `code: 4032`；普通权限不足仍为 `403`，HTTP 状态维持原行为。新版前端同时兼容旧版后端的 `403` 中文文案。旧前端遇到新业务码时仅显示普通错误，因此升级时应同步更新内嵌前端；上述接口调整不迁移数据库，回滚不需要还原数据。受保护接口的任意业务或 HTTP `401` 都会清除本地会话并返回登录页，公开登录接口的认证失败不触发会话失效。

### 节点历史修复

GOST 节点 `1.2.7` 将内置 gRPC 升至 `v1.83.2`，修复 GO-2026-6443（缺失 authority/Host 引发服务端崩溃）和 GO-2026-6348（HTTP/2 DATA 帧碎片导致内存耗尽）。`go-gost/go.mod` 与 `go-gost/x/go.mod` 分别固定依赖基线，必须同步维护并独立扫描；仅更新面板不会替换节点二进制，发布后需在节点管理中升级 GOST 节点至 `1.2.7` 或更高版本。

GOST 节点 `1.2.8` 与 nftables 节点 `nftables-go-1.3.14` 使用 Go 1.26.10 和 `golang.org/x/net v0.60.0` 重建，修复网络服务、HTTP/2 处理和证书校验的已知安全问题；三个模块仅同步该安全依赖所需的加密、系统调用、文本和构建工具版本。面板最新版本与节点上报版本已同步递增，发布后仍需在节点管理中升级现有节点，才会替换旧二进制；仅升级面板无法修复远端节点。

GOST 节点 `1.2.9` 处理 `TcpPing` 诊断和面板“检测状态”这类只读命令后不再重写 `gost.json`，避免频繁截断重写配置文件，降低节点断电或磁盘写满时配置损坏的风险；转发规则行为不变。需要在节点管理中升级 GOST 节点后生效。

在 Debian 12 的 `nftables v1.0.6 (Lester Gooch #5)` 上，若添加中转报 `verify dormant capability probe: decode nft table inventory: EOF`，表示探测临时 dormant 表时未能读到完整 JSON；探测结束后手动执行 `nft -j list tables` 仍可能正常。节点规则工具对该精确版本成功命令返回的空输出或以 EOF 结束的截断 JSON 自动改用 `nft list tables`，再逐表读取文本核实 active/dormant 状态。此兼容无需配置；未知版本、其他 JSON 语法或结构错误、空文本清单或无法确认的表状态仍会阻止规则切换，避免覆盖现有转发。修复随 `nft_flow_reporter` 发布，更新到包含修复的面板镜像后，还需在节点管理中升级受影响节点的组件至 `nftables-go-1.3.13` 或更高版本。仅更新面板不会替换服务器上的节点二进制；面板最新节点版本和 Agent 上报版本必须随组件修复同步递增，否则同版本升级检查会阻止下发。`1.3.12` 仅处理完全空输出，`1.3.13` 补全非空截断情况；若仍失败，错误会附带 stdout 字节数及 stdout/stderr 各前 256 字节，便于确认实际失败分支。排查时应以本次添加返回的错误和近期日志为准，`systemctl status` 可能仍显示更新前的失败记录。
