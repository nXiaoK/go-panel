# go-panel

[![CI and Release](https://github.com/nXiaoK/go-panel/actions/workflows/ci.yml/badge.svg)](https://github.com/nXiaoK/go-panel/actions/workflows/ci.yml)
[![GitHub Release](https://img.shields.io/github/v/release/nXiaoK/go-panel?display_name=tag)](https://github.com/nXiaoK/go-panel/releases)
[![GHCR](https://img.shields.io/badge/GHCR-go--panel-2496ED?logo=docker)](https://github.com/nXiaoK/go-panel/pkgs/container/go-panel)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

一个基于 **Go、React 和 SQLite** 的轻量网络转发管理面板。后端以单二进制内嵌前端，可管理 go-gost 与 nftables 节点，并通过 GitHub Actions 发布完整的多架构 Docker 镜像和节点程序。

> 项目仍在快速迭代。升级前请生成一致性数据库备份并阅读对应 Release Notes；旧版本可能无法识别新数据库结构。

## 主要能力

- 单进程面板：Go 后端内嵌 React SPA，SQLite 数据库无需额外服务。
- 节点管理：支持 amd64/arm64 的 go-gost、nft agent、规则工具和流量上报工具。
- 数据面收敛：节点离线或响应丢失时保留期望状态，重连后自动对账。
- 转发、隧道、限速、用户配额和订阅管理。
- nftables 隧道支持可选单中继的 `A → B → C` 三节点 IPv4 串联；三台节点必须全部使用 nftables 模式，流量只在入口节点计费。
- SQLite 一致性下载/恢复，以及 Cloudflare R2 定时异地备份和保留策略。
- GitHub Release 更新检查、真实构建版本展示和可选的一键容器更新。
- GHCR 多架构镜像：部署机不需要安装 Go、Node.js，也不需要本地构建节点程序。

## 发布通道

| 通道 | 镜像标签 | 用途 |
|---|---|---|
| 稳定版 | `latest`、`v0.1.0`、`v0.1`、`v0` | 由 `v*.*.*` Git Tag 发布，推荐生产环境使用 |
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

访问 `http://服务器地址:6365`。生产环境建议使用 HTTPS 反向代理，并将 `.env` 中的 `PANEL_BIND` 改成 `127.0.0.1`，避免 6365 端口直接暴露公网。

确认管理员已创建并完成密码轮换后，可以清空 `.env` 中的 `ADMIN_PASSWORD` 再执行 `docker compose up -d`。`JWT_SECRET` 必须长期保留；修改它会使所有现有登录令牌失效，也可能使已加密保存的 R2 凭据无法读取。

### Docker Run

Compose 是推荐方式；如只需要单容器，可以直接运行公开镜像：

```bash
docker volume create go-panel-data
docker run -d \
  --name go-panel \
  --restart unless-stopped \
  --pull always \
  -p 6365:6365 \
  -v go-panel-data:/app/data \
  -e ENVIRONMENT=production \
  -e JWT_SECRET='替换为至少32字节随机密钥' \
  -e ADMIN_PASSWORD='替换为唯一高强度密码' \
  -e CORS_ALLOW_ORIGIN='https://panel.example.com' \
  ghcr.io/nxiaok/go-panel:latest
```

## 升级与回滚

稳定版手动升级只需要拉取镜像并重建容器：

```bash
docker compose pull
docker compose up -d
```

升级不会删除 `panel_data` 卷。不要执行 `docker compose down -v`，其中的 `-v` 会永久删除数据库和本地更新备份。

如需回滚，将 `.env` 中的 `GO_PANEL_VERSION` 改为升级前的明确版本，例如 `v0.1.0`，然后再次运行上述两条命令。若新版本已经执行不兼容的数据迁移，还必须同时恢复升级前的数据库快照。

### 可选：界面一键更新

主面板不会直接挂载 Docker Socket。需要界面“立即更新”按钮时，再下载并启用独立更新扩展：

```bash
curl -fsSLO https://raw.githubusercontent.com/nXiaoK/go-panel/main/compose.update.yml
printf '\nUPDATE_TRIGGER_TOKEN=%s\n' "$(openssl rand -hex 32)" >> .env
docker compose -f compose.yml -f compose.update.yml up -d
```

更新流程会先把经过完整性校验的 SQLite 快照保存到数据卷的 `backups/`，再由受标签和 Token 限制的 Watchtower 侧车拉取并替换面板容器。Docker Socket 具备接近宿主机 root 的控制能力，只应在可信服务器上启用该扩展。

一键更新要求 `.env` 中的 `GO_PANEL_VERSION=latest`；如果固定为 `v0.1.0` 等不可变标签，Watchtower 只会重新检查该固定镜像，不会跨版本升级。一键更新只替换镜像，不能自动修改 Compose 或环境变量。Release Notes 标注需要部署配置变更时，请使用手动升级流程。

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

## 配置

完整、带风险说明的模板见 [`.env.example`](.env.example)。主要变量如下：

| 变量 | 默认值 | 说明 |
|---|---|---|
| `JWT_SECRET` | 无 | 远程部署必填，至少 32 字节；修改会使 JWT 和 R2 凭据受影响 |
| `ADMIN_USERNAME` | `admin_user` | 首次创建管理员使用的用户名 |
| `ADMIN_PASSWORD` | 空 | 新数据库远程启动时必填；管理员安全建立后可清空 |
| `CORS_ALLOW_ORIGIN` | 无 | 生产模式必填，多个明确来源用逗号分隔 |
| `PANEL_BIND` | `0.0.0.0` | 宿主机监听地址，反向代理部署建议 `127.0.0.1` |
| `PANEL_PORT` | `6365` | 宿主机映射端口 |
| `GO_PANEL_VERSION` | `latest` | 可固定为具体稳定版本以便回滚 |
| `TRUSTED_PROXIES` | 空 | 可信反向代理 CIDR；空值不信任转发头 |
| `UPDATE_CHECK_ENABLED` | `true` | 是否检查 GitHub 最新稳定 Release |
| `UPDATE_CHECK_INTERVAL` | `6h` | 成功检查结果缓存，最小 5 分钟、最大 7 天 |
| `ALLOW_INSECURE_NODE_DOWNLOADS` | `false` | 临时允许 HTTP 节点下载，存在泄密和篡改风险 |
| `ALLOW_LEGACY_NFT_REPORTS` | `false` | 临时兼容旧上报，缺少完整幂等保障 |

## 本地开发

需要 Go 1.26.5、Node.js 22 和 npm：

```bash
npm ci --prefix vite-frontend
npm run build --prefix vite-frontend
bash ./scripts/sync-web-dist.sh
go test ./...
go run .
```

完整验证入口：

```bash
./scripts/verify.sh
RUN_RACE=1 ./scripts/verify.sh
RUN_PACKAGING=1 ./scripts/verify.sh
```

节点产物仅在本地调试时需要手工生成：

```bash
./scripts/build-node-assets.sh
```

## 贡献与安全

欢迎通过 Issue 和 Pull Request 提交问题、修复和功能建议。提交前请运行后端、前端和相关并发测试，不要提交 `.env`、数据库、节点密钥或构建产物。

安全问题请优先使用 GitHub 仓库的私密 Security Advisory，不要在公开 Issue 中粘贴密钥、数据库或可直接利用的漏洞细节。

## License

项目主体使用 [MIT License](LICENSE)。内置修改版 go-gost 及其他依赖保留各自许可证，详见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) 和各依赖源码/发布包中的许可证文件。
