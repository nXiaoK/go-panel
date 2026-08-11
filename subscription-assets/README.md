# 订阅配置模板说明

本目录包含面板渲染订阅时使用的配置模板。YAML、INI 和 Shell 文件的关键配置旁保留了中文注释；`sing-box-android.json` 属于严格 JSON，语法不允许注释，因此在本文集中说明其默认行为和风险。

## `sing-box-android.json`

适用于官方 sing-box for Android 的手机 VPN/TUN 模式。它是由面板注入节点的模板骨架，不是可以直接使用的成品配置；仓库中的 `proxy` 暂以 `direct` 占位以通过格式校验，公开订阅渲染时会移除此占位。如果没有兼容节点，面板会返回错误，不会让境外流量静默回退直连。

### DNS 模块

- AAAA 查询默认返回空结果，即默认关闭 IPv6，避免 IPv6 绕过代理；需要 IPv6 时必须同时检查节点、路由和泄漏风险。
- 私有域名、`.cn` 与中国域名规则使用 Android 当前网络提供的本地 DNS，因此运营商仍能看到国内 DNS 查询。
- 其他域名默认经 `proxy` 使用 Cloudflare DoH；DoH 服务器主机名和地址由模板固定。
- 模板劫持普通 TCP/UDP 53 并拒绝 DoT 853，但无法识别所有封装在 HTTPS 中的第三方 DoH。Android“私人 DNS”和应用内安全 DNS 建议关闭。

### 路由模块

- 私有地址、局域网域名、中国域名和中国 IP 默认直连，未匹配流量默认走 `proxy`。
- 常见广告规则默认拒绝。
- 常见 STUN/TURN 默认拒绝，以降低 WebRTC 直连泄漏；这可能影响语音、视频通话和游戏 NAT 穿透，删除相关规则前应明确接受泄漏风险。
- 常见独立 DoH 服务强制走代理，避免被后续国内规则误判为直连。

### 远程规则集

模板每天通过代理更新 SagerNet 官方二进制规则集。首次启动依赖代理可用且规则下载成功；无法访问规则源时配置可能启动失败。完全离线部署应改用本地 `.srs` 文件，并同步修改规则集类型和路径。

### 节点兼容性

面板当前为 VLESS、VMess、Trojan、Shadowsocks 和 SOCKS5 生成 sing-box 出站。sing-box 不支持的 Snell/XHTTP 节点会被跳过；只剩不兼容或凭据不完整的节点时，订阅会拒绝输出。

若节点服务器使用域名，建立隧道前需要通过本地 DNS 引导解析，该查询对本地网络可见。可以使用服务器 IPv4 时优先填写 IP，因为模板默认关闭 IPv6。

## 其他模板

- `clash.yml`：Clash 订阅骨架，关键 DNS、代理组与分流规则在文件内使用中文注释说明。
- `surge.config`：Surge 订阅骨架，代理与规则由面板渲染；修改 DNS、MITM 或直连规则前应评估泄漏与证书风险。
- `vless-server.sh`：VLESS/VMess/Trojan 等服务端管理脚本，涉及防火墙、软件源和系统服务修改；只应在受控节点以 root 执行，并先审阅脚本来源和 Release 校验值。

## GitHub 下载代理

系统配置中的 `github_download_proxy` 默认留空，此时订阅服务器脚本直接访问 GitHub。中国大陆或其他无法稳定访问 GitHub 的服务器可填写可信的 HTTPS 代理前缀；代理必须支持“代理前缀 + `/` + 完整 GitHub URL”，并同时代理 `github.com`、`api.github.com` 与 `raw.githubusercontent.com`。面板生成绑定命令时会把该值安全传入脚本，脚本再将其保存在 `/etc/vless-reality/sub-panel.env`，供后续安装 Xray、sing-box、AnyTLS、ShadowTLS、acme.sh、wgcf 和脚本更新时复用。

代理能看到并替换下载的可执行文件，错误或失陷的代理可能导致节点被植入恶意程序，因此必须优先使用自建或明确可信的服务，且只允许 HTTPS。清空该配置后，新生成的绑定命令不再下发代理；已绑定服务器需要重新执行绑定命令，或由管理员审阅后从 `sub-panel.env` 删除 `SUB_PANEL_GITHUB_PROXY`。操作系统的 APT/DNF/APK 软件源不经过此配置，仍需在服务器侧选择可达且可信的发行版镜像。
